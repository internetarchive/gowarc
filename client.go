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

type HTTPClientSettings struct {
	RotatorSettings       *RotatorSettings
	Proxy                 string
	TempDir               string
	DiscardHook           DiscardHook
	DNSServers            []string
	DedupeOptions         DedupeOptions
	DialTimeout           time.Duration
	ResponseHeaderTimeout time.Duration
	DNSResolutionTimeout  time.Duration
	DNSRecordsTTL         time.Duration
	DNSCacheSize          int
	TLSHandshakeTimeout   time.Duration
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
	MaxReadBeforeTruncate  int
	verifyCerts            bool
	FullOnDisk             bool
	closeDNSCache          func()
	// MaxRAMUsageFraction is the fraction of system RAM above which we'll force spooling to disk. For example, 0.5 = 50%.
	// If set to <= 0, the default value is DefaultMaxRAMUsageFraction.
	MaxRAMUsageFraction float64
	randomLocalIP       bool
	DataTotal           *int64

	CDXDedupeTotalBytes          *int64
	DoppelgangerDedupeTotalBytes *int64
	LocalDedupeTotalBytes        *int64

	CDXDedupeTotal          *int64
	DoppelgangerDedupeTotal *int64
	LocalDedupeTotal        *int64
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

	// Initialize counters
	httpClient.DataTotal = DataTotal

	httpClient.CDXDedupeTotalBytes = CDXDedupeTotalBytes
	httpClient.DoppelgangerDedupeTotalBytes = DoppelgangerDedupeTotalBytes
	httpClient.LocalDedupeTotalBytes = LocalDedupeTotalBytes

	httpClient.CDXDedupeTotal = CDXDedupeTotal
	httpClient.DoppelgangerDedupeTotal = DoppelgangerDedupeTotal
	httpClient.LocalDedupeTotal = LocalDedupeTotal

	// Configure random local IP
	httpClient.randomLocalIP = HTTPClientSettings.RandomLocalIP
	if httpClient.randomLocalIP {
		httpClient.interfacesWatcherStop = make(chan bool)
		httpClient.interfacesWatcherStarted = make(chan bool)
		go httpClient.getAvailableIPs(HTTPClientSettings.IPv6AnyIP)
		<-httpClient.interfacesWatcherStarted
	}

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

	// Configure custom dialer / transport
	customDialer, err := newCustomDialer(httpClient, HTTPClientSettings.Proxy, HTTPClientSettings.DialTimeout, HTTPClientSettings.DNSRecordsTTL, HTTPClientSettings.DNSResolutionTimeout, HTTPClientSettings.DNSCacheSize, HTTPClientSettings.DNSServers, HTTPClientSettings.DisableIPv4, HTTPClientSettings.DisableIPv6)
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
