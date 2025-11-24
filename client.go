package warc

import (
	"net/http"
	"os"
	"sync"
	"time"
)

type Error struct {
	Err  error
	Func string
}

// ProxyNetwork defines the network layer (IPv4/IPv6) a proxy can support
type ProxyNetwork int

const (
	// ProxyNetworkUnset is the zero value and must not be used - forces explicit selection
	ProxyNetworkUnset ProxyNetwork = iota
	// ProxyNetworkAny means the proxy can be used for both IPv4 and IPv6 connections
	ProxyNetworkAny
	// ProxyNetworkIPv4 means the proxy should only be used for IPv4 connections
	ProxyNetworkIPv4
	// ProxyNetworkIPv6 means the proxy should only be used for IPv6 connections
	ProxyNetworkIPv6
)

// ProxyType defines the infrastructure type of a proxy
type ProxyType int

const (
	// ProxyTypeAny means the proxy can be used for any type of request
	ProxyTypeAny ProxyType = iota
	// ProxyTypeMobile means the proxy uses mobile network infrastructure
	ProxyTypeMobile
	// ProxyTypeResidential means the proxy uses residential IP addresses
	ProxyTypeResidential
	// ProxyTypeDatacenter means the proxy uses datacenter infrastructure
	ProxyTypeDatacenter
)

// ProxyConfig defines the configuration for a single proxy
type ProxyConfig struct {
	// URL is the proxy URL (e.g., "socks5://proxy.example.com:1080")
	URL string
	// Network specifies if this proxy supports IPv4, IPv6, or both
	Network ProxyNetwork
	// Type specifies the infrastructure type (Mobile, Residential, Datacenter, or Any)
	Type ProxyType
	// AllowedDomains is a list of glob patterns for domains this proxy should handle
	// Examples: "*.example.com", "api.*.org"
	// If empty, the proxy can be used for any domain
	AllowedDomains []string
}

type HTTPClientSettings struct {
	RotatorSettings       *RotatorSettings
	Proxies               []ProxyConfig
	AllowDirectFallback   bool
	TempDir               string
	DiscardHook           DiscardHook
	DNSServers            []string
	DedupeOptions         DedupeOptions
	DialTimeout           time.Duration
	ResponseHeaderTimeout time.Duration
	DNSResolutionTimeout  time.Duration
	DNSRecordsTTL         time.Duration
	DNSCacheSize          int
	DNSConcurrency        int
	TLSHandshakeTimeout   time.Duration
	ConnReadDeadline      time.Duration
	MaxReadBeforeTruncate int
	DecompressBody        bool
	FollowRedirects       bool
	FullOnDisk            bool
	MaxRAMUsageFraction   float64
	VerifyCerts           bool
	RandomLocalIP         bool
	DisableIPv4           bool
	DisableIPv6           bool
	IPv6AnyIP             bool
	DigestAlgorithm       DigestAlgorithm
	StatsRegistry         StatsRegistry
	LogBackend            LogBackend
}

type CustomHTTPClient struct {
	interfacesWatcherStop    chan bool
	WaitGroup                *WaitGroupWithCount
	dedupeHashTable          *sync.Map
	ErrChan                  chan *Error
	WARCWriter               chan *RecordBatch
	interfacesWatcherStarted chan bool
	http.Client
	TempDir                string
	WARCWriterDoneChannels []chan bool
	DiscardHook            DiscardHook
	dedupeOptions          DedupeOptions
	TLSHandshakeTimeout    time.Duration
	ConnReadDeadline       time.Duration
	MaxReadBeforeTruncate  int
	verifyCerts            bool
	FullOnDisk             bool
	DigestAlgorithm        DigestAlgorithm
	closeDNSCache          func()
	// MaxRAMUsageFraction is the fraction of system RAM above which we'll force spooling to disk. For example, 0.5 = 50%.
	// If set to <= 0, the default value is DefaultMaxRAMUsageFraction.
	MaxRAMUsageFraction float64
	randomLocalIP       bool

	statsRegistry StatsRegistry
	logBackend    LogBackend
}

func (c *CustomHTTPClient) Close() error {
	var wg sync.WaitGroup
	c.WaitGroup.Wait()
	c.CloseIdleConnections()

	close(c.WARCWriter)

	wg.Add(len(c.WARCWriterDoneChannels))
	for _, doneChan := range c.WARCWriterDoneChannels {
		go func(done chan bool) {
			defer wg.Done()
			<-done
		}(doneChan)
	}

	wg.Wait()
	close(c.ErrChan)

	if c.randomLocalIP {
		c.interfacesWatcherStop <- true
		close(c.interfacesWatcherStop)
	}

	c.closeDNSCache()

	return nil
}

func NewWARCWritingHTTPClient(HTTPClientSettings HTTPClientSettings) (httpClient *CustomHTTPClient, err error) {
	httpClient = new(CustomHTTPClient)

	// Initialize stats registry
	if HTTPClientSettings.StatsRegistry != nil {
		httpClient.statsRegistry = HTTPClientSettings.StatsRegistry
		HTTPClientSettings.RotatorSettings.StatsRegistry = HTTPClientSettings.StatsRegistry
	} else {
		localStatsRegistry := newLocalRegistry()
		httpClient.statsRegistry = localStatsRegistry
		HTTPClientSettings.RotatorSettings.StatsRegistry = localStatsRegistry
	}

	// Initialize log backend
	if HTTPClientSettings.LogBackend != nil {
		httpClient.logBackend = HTTPClientSettings.LogBackend
		HTTPClientSettings.RotatorSettings.LogBackend = HTTPClientSettings.LogBackend
	} else {
		httpClient.logBackend = &noopLogger{}
		HTTPClientSettings.RotatorSettings.LogBackend = &noopLogger{}
	}

	// Configure random local IP
	httpClient.randomLocalIP = HTTPClientSettings.RandomLocalIP
	if httpClient.randomLocalIP {
		httpClient.interfacesWatcherStop = make(chan bool)
		httpClient.interfacesWatcherStarted = make(chan bool)
		go httpClient.getAvailableIPs(HTTPClientSettings.IPv6AnyIP)
		<-httpClient.interfacesWatcherStarted
	}

	// Set block and payload digest algorithm (it's important to set that before we configure the WARC writer)
	httpClient.DigestAlgorithm = HTTPClientSettings.DigestAlgorithm
	HTTPClientSettings.RotatorSettings.digestAlgorithm = HTTPClientSettings.DigestAlgorithm

	// Toggle deduplication options and create map for deduplication records.
	httpClient.dedupeOptions = HTTPClientSettings.DedupeOptions
	httpClient.dedupeHashTable = new(sync.Map)

	// Set default deduplication threshold to 2048 bytes
	if httpClient.dedupeOptions.SizeThreshold == 0 {
		httpClient.dedupeOptions.SizeThreshold = 2048
	}

	// Set a hook to determine if we should discard a response
	httpClient.DiscardHook = HTTPClientSettings.DiscardHook

	// Create an error channel for sending WARC errors through
	httpClient.ErrChan = make(chan *Error)

	// Toggle verification of certificates
	// InsecureSkipVerify expects the opposite of the verifyCerts flag, as such we flip it.
	httpClient.verifyCerts = !HTTPClientSettings.VerifyCerts

	// Configure WARC temporary file directory
	if HTTPClientSettings.TempDir != "" {
		httpClient.TempDir = HTTPClientSettings.TempDir
		err = os.MkdirAll(httpClient.TempDir, os.ModePerm)
		if err != nil {
			return nil, err
		}
	}

	// Configure if we are only storing responses only on disk or in memory and on disk.
	httpClient.FullOnDisk = HTTPClientSettings.FullOnDisk

	// Configure the maximum RAM usage fraction
	httpClient.MaxRAMUsageFraction = HTTPClientSettings.MaxRAMUsageFraction

	// Configure our max read before we start truncating records
	if HTTPClientSettings.MaxReadBeforeTruncate == 0 {
		httpClient.MaxReadBeforeTruncate = 1000000000
	} else {
		httpClient.MaxReadBeforeTruncate = HTTPClientSettings.MaxReadBeforeTruncate
	}

	// Configure the waitgroup
	httpClient.WaitGroup = new(WaitGroupWithCount)

	// Configure WARC writer
	httpClient.WARCWriter, httpClient.WARCWriterDoneChannels, err = HTTPClientSettings.RotatorSettings.NewWARCRotator()
	if err != nil {
		return nil, err
	}

	// Configure HTTP client
	if !HTTPClientSettings.FollowRedirects {
		httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	// Verify timeouts and set default values
	if HTTPClientSettings.DialTimeout == 0 {
		HTTPClientSettings.DialTimeout = 10 * time.Second
	}

	if HTTPClientSettings.ResponseHeaderTimeout == 0 {
		HTTPClientSettings.ResponseHeaderTimeout = 10 * time.Second
	}

	if HTTPClientSettings.TLSHandshakeTimeout == 0 {
		HTTPClientSettings.TLSHandshakeTimeout = 10 * time.Second
	}

	if HTTPClientSettings.DNSResolutionTimeout == 0 {
		HTTPClientSettings.DNSResolutionTimeout = 5 * time.Second
	}

	if HTTPClientSettings.DNSRecordsTTL == 0 {
		HTTPClientSettings.DNSRecordsTTL = 5 * time.Minute
	}

	if HTTPClientSettings.DNSCacheSize == 0 {
		HTTPClientSettings.DNSCacheSize = 10_000
	}

	httpClient.TLSHandshakeTimeout = HTTPClientSettings.TLSHandshakeTimeout
	httpClient.ConnReadDeadline = HTTPClientSettings.ConnReadDeadline

	// Configure custom dialer / transport
	customDialer, err := newCustomDialer(httpClient, HTTPClientSettings.Proxies, HTTPClientSettings.AllowDirectFallback, HTTPClientSettings.DialTimeout, HTTPClientSettings.DNSRecordsTTL, HTTPClientSettings.DNSResolutionTimeout, HTTPClientSettings.DNSCacheSize, HTTPClientSettings.DNSServers, HTTPClientSettings.DNSConcurrency, HTTPClientSettings.DisableIPv4, HTTPClientSettings.DisableIPv6)
	if err != nil {
		return nil, err
	}

	httpClient.closeDNSCache = func() {
		customDialer.DNSRecords.Close()
		time.Sleep(1 * time.Second)
	}

	customTransport, err := newCustomTransport(customDialer, HTTPClientSettings.DecompressBody, HTTPClientSettings.TLSHandshakeTimeout)
	if err != nil {
		return nil, err
	}

	httpClient.Transport = customTransport

	return httpClient, nil
}
