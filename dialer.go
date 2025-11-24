package warc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/internetarchive/gowarc/pkg/spooledtempfile"
	"github.com/maypok86/otter"
	"github.com/miekg/dns"
	tls "github.com/refraction-networking/utls"
	"golang.org/x/net/proxy"
	"golang.org/x/sync/errgroup"
)

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

const (
	// ContextKeyFeedback is the context key for the feedback channel.
	// When provided, the channel will receive a signal once the WARC record
	// has been written to disk, making WARC writing synchronous.
	// Use WithFeedbackChannel() helper function for convenience.
	ContextKeyFeedback contextKey = "feedback"

	// ContextKeyWrappedConn is the context key for the wrapped connection channel.
	// This is used internally to retrieve the wrapped connection for advanced use cases.
	// Use WithWrappedConnection() helper function for convenience.
	ContextKeyWrappedConn contextKey = "wrappedConn"

	// ContextKeyProxyType is the context key for requesting a specific proxy type.
	// External callers (like Zeno) can set this to request a proxy of a specific type
	// (Mobile, Residential, or Datacenter).
	// Use WithProxyType() helper function for convenience.
	ContextKeyProxyType contextKey = "proxyType"
)

// WithFeedbackChannel adds a feedback channel to the request context.
// When provided, the channel will receive a signal once the WARC record
// has been written to disk, making WARC writing synchronous.
// Without this, WARC writing is asynchronous.
//
// Example:
//
//	feedbackChan := make(chan struct{}, 1)
//	req = req.WithContext(warc.WithFeedbackChannel(req.Context(), feedbackChan))
//	// ... perform request ...
//	<-feedbackChan // blocks until WARC is written
func WithFeedbackChannel(ctx context.Context, feedbackChan chan struct{}) context.Context {
	return context.WithValue(ctx, ContextKeyFeedback, feedbackChan)
}

// WithWrappedConnection adds a wrapped connection channel to the request context.
// This is used for advanced use cases where direct access to the connection is needed.
func WithWrappedConnection(ctx context.Context, wrappedConnChan chan *CustomConnection) context.Context {
	return context.WithValue(ctx, ContextKeyWrappedConn, wrappedConnChan)
}

// WithProxyType adds a proxy type preference to the request context.
// When set, the proxy selector will prefer proxies of the specified type
// (Mobile, Residential, or Datacenter).
// This is typically used by external callers like Zeno when they need a specific proxy type.
//
// Example:
//
//	req = req.WithContext(warc.WithProxyType(req.Context(), warc.ProxyTypeMobile))
func WithProxyType(ctx context.Context, proxyType ProxyType) context.Context {
	return context.WithValue(ctx, ContextKeyProxyType, proxyType)
}

// dnsExchanger is an interface for DNS clients that can exchange messages
type dnsExchanger interface {
	ExchangeContext(ctx context.Context, m *dns.Msg, address string) (r *dns.Msg, rtt time.Duration, err error)
}

// proxyDialerInfo holds information about a configured proxy
type proxyDialerInfo struct {
	dialer         proxy.ContextDialer
	needsHostname  bool // true if proxy requires hostname (socks5h, http), false if can use IP (socks5)
	proxyNetwork   ProxyNetwork
	proxyType      ProxyType
	allowedDomains []string // glob patterns
	url            string
	name           string
	stats          StatsRegistry
}

type customDialer struct {
	proxyDialers         []proxyDialerInfo
	proxyRoundRobinIndex atomic.Uint32
	allowDirectFallback  bool
	client               *CustomHTTPClient
	DNSConfig            *dns.ClientConfig
	DNSClient            dnsExchanger
	DNSRecords           *otter.Cache[string, net.IP]
	net.Dialer
	disableIPv4        bool
	disableIPv6        bool
	dnsConcurrency     int
	dnsRoundRobinIndex atomic.Uint32

	stats StatsRegistry
}

var emptyPayloadDigests = []string{
	"sha1:3I42H3S6NNFQ2MSVX7XZKYAYSCX5QBYJ",
	"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	"sha256:4OYMIQUY7QOBJGX36TEJS35ZEQT24QPEMSNZGTFESWMRW6CSXBKQ====",
	"blake3:af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262",
}

func newCustomDialer(httpClient *CustomHTTPClient, proxies []ProxyConfig, allowDirectFallback bool, DialTimeout, DNSRecordsTTL, DNSResolutionTimeout time.Duration, DNSCacheSize int, DNSServers []string, DNSConcurrency int, disableIPv4, disableIPv6 bool) (d *customDialer, err error) {
	d = new(customDialer)

	d.stats = httpClient.statsRegistry

	d.Timeout = DialTimeout
	d.client = httpClient
	d.disableIPv4 = disableIPv4
	d.disableIPv6 = disableIPv6
	d.dnsConcurrency = DNSConcurrency
	d.allowDirectFallback = allowDirectFallback

	DNScache, err := otter.MustBuilder[string, net.IP](DNSCacheSize).
		// CollectStats(). // Uncomment this line to enable stats collection, can be useful later on
		WithTTL(DNSRecordsTTL).
		Build()
	if err != nil {
		panic(err)
	}

	d.DNSRecords = &DNScache

	d.DNSConfig, err = dns.ClientConfigFromFile("/etc/resolv.conf")
	if err != nil {
		return nil, err
	}

	if len(DNSServers) > 0 {
		d.DNSConfig.Servers = DNSServers
	}

	d.DNSClient = &dns.Client{
		Net:     "udp",
		Timeout: DNSResolutionTimeout,
	}

	// Initialize all proxies
	for _, proxyConfig := range proxies {
		if proxyConfig.URL == "" {
			continue
		}

		// Validate that Network is explicitly set
		if proxyConfig.Network == ProxyNetworkUnset {
			return nil, fmt.Errorf("proxy %s: Network must be explicitly set to ProxyNetworkAny, ProxyNetworkIPv4, or ProxyNetworkIPv6", proxyConfig.URL)
		}

		u, err := url.Parse(proxyConfig.URL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse proxy URL %s: %w", proxyConfig.URL, err)
		}

		var proxyDialer proxy.Dialer
		if proxyDialer, err = proxy.FromURL(u, d); err != nil {
			return nil, fmt.Errorf("failed to create proxy from URL %s: %w", proxyConfig.URL, err)
		}

		// Determine if this proxy requires hostname (remote DNS) or can use IP (local DNS)
		// Proxies with remote DNS: socks5h, socks4a, http, https
		// Proxies with local DNS: socks5, socks4
		needsHostname := u.Scheme == "socks5h" || u.Scheme == "socks4a" ||
			u.Scheme == "http" || u.Scheme == "https"

		d.proxyDialers = append(d.proxyDialers, proxyDialerInfo{
			dialer:         proxyDialer.(proxy.ContextDialer),
			needsHostname:  needsHostname,
			proxyNetwork:   proxyConfig.Network,
			proxyType:      proxyConfig.Type,
			allowedDomains: proxyConfig.AllowedDomains,
			url:            proxyConfig.URL,
			name:           proxyName(u),
			stats:          httpClient.statsRegistry,
		})
	}

	return d, nil
}

// selectProxy selects an appropriate proxy based on network type, domain, and context flags.
// Returns nil if no proxy is available or should be used (direct connection).
// Returns an error if proxies exist but none match the requirements and direct fallback is disabled.
func (d *customDialer) selectProxy(ctx context.Context, network, address string) (*proxyDialerInfo, error) {
	// No proxies configured, use direct connection
	if len(d.proxyDialers) == 0 {
		return nil, nil
	}

	// Extract hostname from address for domain matching
	hostname, _, err := net.SplitHostPort(address)
	if err != nil {
		// If no port, treat the whole address as hostname
		hostname = address
	}

	// Check if a specific proxy type is requested via context
	var requestedProxyType *ProxyType
	if ctx.Value(ContextKeyProxyType) != nil {
		if val, ok := ctx.Value(ContextKeyProxyType).(ProxyType); ok {
			requestedProxyType = &val
		}
	}

	// Filter eligible proxies
	var eligible []*proxyDialerInfo
	for i := range d.proxyDialers {
		proxy := &d.proxyDialers[i]

		// Filter by proxy type (Mobile, Residential, Datacenter)
		// If a specific type is requested, only use matching proxies
		// If no type is requested, only use ProxyTypeAny proxies
		if requestedProxyType != nil {
			// Specific type requested: only match that exact type
			if proxy.proxyType != *requestedProxyType {
				continue
			}
		} else {
			// No type requested: only use ProxyTypeAny proxies
			if proxy.proxyType != ProxyTypeAny {
				continue
			}
		}

		// Filter by network type (IPv4/IPv6)
		switch proxy.proxyNetwork {
		case ProxyNetworkIPv4:
			if strings.HasSuffix(network, "6") {
				continue // Skip IPv6 networks
			}
		case ProxyNetworkIPv6:
			if strings.HasSuffix(network, "4") {
				continue // Skip IPv4 networks
			}
		case ProxyNetworkAny:
			// Always eligible regardless of network type
		}

		// Filter by domain patterns
		if len(proxy.allowedDomains) > 0 {
			matched := false
			for _, pattern := range proxy.allowedDomains {
				// Use filepath.Match for glob pattern matching
				// Note: filepath.Match doesn't support '**' but supports '*' and '?'
				if match, _ := filepath.Match(pattern, hostname); match {
					matched = true
					break
				}
				// Also check if pattern matches a subdomain pattern
				// For example, "*.example.com" should match "api.example.com"
				if strings.HasPrefix(pattern, "*.") {
					suffix := pattern[1:] // Remove the leading '*'
					if strings.HasSuffix(hostname, suffix) || hostname == suffix[1:] {
						matched = true
						break
					}
				}
			}
			if !matched {
				continue // Skip if domain doesn't match any pattern
			}
		}

		eligible = append(eligible, proxy)
	}

	// No eligible proxies found
	if len(eligible) == 0 {
		if d.allowDirectFallback {
			return nil, nil // Use direct connection
		}
		proxyTypeStr := "any"
		if requestedProxyType != nil {
			proxyTypeStr = fmt.Sprintf("%v", *requestedProxyType)
		}
		return nil, fmt.Errorf("no eligible proxies found for network=%s, address=%s, proxyType=%s and direct fallback is disabled", network, address, proxyTypeStr)
	}

	// Round-robin selection among eligible proxies
	startIdx := int(d.proxyRoundRobinIndex.Add(1)-1) % len(eligible)
	selectedProxy := eligible[startIdx]

	// Update proxy statistics
	if selectedProxy.stats != nil {
		selectedProxy.stats.RegisterCounter(proxyRequestsTotal, proxyRequestsTotalHelp, []string{"proxy"}).WithLabels(Labels{"proxy": selectedProxy.name}).Add(1)
		selectedProxy.stats.RegisterGauge(proxyLastUsedNanoseconds, proxyLastUsedNanosecondsHelp, []string{"proxy"}).WithLabels(Labels{"proxy": selectedProxy.name}).Set(time.Now().UnixNano())
	}

	return selectedProxy, nil
}

type CustomConnection struct {
	net.Conn
	io.Reader
	io.Writer
	closers []*io.PipeWriter
	sync.WaitGroup
	connReadDeadline time.Duration
	firstRead        sync.Once // Indicates if the first read has been performed, used to set the read deadline
}

func (cc *CustomConnection) setReadDeadline() error {
	if cc.connReadDeadline > 0 {
		if err := cc.Conn.SetReadDeadline(time.Now().Add(cc.connReadDeadline)); err != nil {
			return errors.New("CustomConnection.Read: SetReadDeadline failed: " + err.Error())
		}
	}
	return nil
}

func (cc *CustomConnection) Read(b []byte) (int, error) {
	cc.firstRead.Do(func() { // apply read deadline for the first read
		if err := cc.setReadDeadline(); err != nil {
			cc.CloseWithError(err)
		}
	})
	c, err := cc.Reader.Read(b)
	if err != nil {
		cc.CloseWithError(err)
		return c, err
	}
	// apply read deadline for the next read
	cc.setReadDeadline() // ignore error, will be triggered on next read
	return c, err
}

func (cc *CustomConnection) Write(b []byte) (int, error) {
	return cc.Writer.Write(b)
}

func (cc *CustomConnection) Close() error {
	return cc.CloseWithError(nil)
}

func (cc *CustomConnection) CloseWithError(err error) error {
	var closeErrors []error

	for _, c := range cc.closers {
		if closeErr := c.CloseWithError(err); closeErr != nil {
			closeErrors = append(closeErrors, fmt.Errorf("closing pipe writer failed: %w", closeErr))
		}
	}

	if connErr := cc.Conn.Close(); connErr != nil {
		closeErrors = append(closeErrors, fmt.Errorf("closing connection failed: %w", connErr))
	}

	return errors.Join(closeErrors...)
}

func (d *customDialer) wrapConnection(ctx context.Context, c net.Conn, scheme string) *CustomConnection {
	reqReader, reqWriter := io.Pipe()
	respReader, respWriter := io.Pipe()

	d.client.WaitGroup.Add(1)
	go d.writeWARCFromConnection(ctx, reqReader, respReader, scheme, c)

	wrappedConn := &CustomConnection{
		Conn:             c,
		closers:          []*io.PipeWriter{reqWriter, respWriter},
		Reader:           io.TeeReader(c, respWriter),
		Writer:           io.MultiWriter(reqWriter, c),
		connReadDeadline: d.client.ConnReadDeadline,
	}
	if ctx.Value(ContextKeyWrappedConn) != nil {
		connChan, ok := ctx.Value(ContextKeyWrappedConn).(chan *CustomConnection)
		if !ok {
			panic("wrapConnection: wrappedConn channel is not of type chan *CustomConnection")
		}
		connChan <- wrappedConn
		close(connChan)
	}
	return wrappedConn
}

func (d *customDialer) CustomDialContext(ctx context.Context, network, address string) (conn net.Conn, err error) {
	// Determine the network based on IPv4/IPv6 settings
	network = d.getNetworkType(network)
	if network == "" {
		return nil, errors.New("no supported network type available")
	}

	// Select appropriate proxy based on context, network type, and domain
	selectedProxy, err := d.selectProxy(ctx, network, address)
	if err != nil {
		return nil, err
	}

	var dialAddr string
	var IP net.IP

	if selectedProxy != nil && selectedProxy.needsHostname {
		// Remote DNS proxy (socks5h, socks4a, http, https)
		// Skip DNS archiving to avoid privacy leak and ensure accuracy.
		// The proxy will handle DNS resolution on its end, and we don't want to:
		// 1. Leak DNS queries to local DNS servers (defeats purpose of socks5h)
		// 2. Archive potentially incorrect DNS results (local DNS may differ from proxy's DNS)
		dialAddr = address
	} else {
		// Direct connection or local DNS proxy (socks5, socks4)
		// Archive DNS and use resolved IP
		IP, _, err = d.archiveDNS(ctx, address)
		if err != nil {
			return nil, err
		}

		// Extract port from address for IP:port construction
		var port string
		_, port, err = net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("failed to extract port from address %s: %w", address, err)
		}

		dialAddr = net.JoinHostPort(IP.String(), port)
	}

	if selectedProxy != nil {
		conn, err = selectedProxy.dialer.DialContext(ctx, network, dialAddr)
		if err != nil && selectedProxy.stats != nil {
			selectedProxy.stats.RegisterCounter(proxyErrorsTotal, proxyErrorsTotalHelp, []string{"proxy"}).WithLabels(Labels{"proxy": selectedProxy.name}).Add(1)
		}
	} else {
		if d.client.randomLocalIP {
			localAddr := getLocalAddr(network, IP)
			if localAddr != nil {
				switch network {
				case "tcp", "tcp4", "tcp6":
					d.LocalAddr = localAddr.(*net.TCPAddr)
				case "udp", "udp4", "udp6":
					d.LocalAddr = localAddr.(*net.UDPAddr)
				}
			}
		}

		conn, err = d.DialContext(ctx, network, dialAddr)
	}

	if err != nil {
		return nil, err
	}

	return d.wrapConnection(ctx, conn, "http"), nil
}

func (d *customDialer) CustomDial(network, address string) (net.Conn, error) {
	return d.CustomDialContext(context.Background(), network, address)
}

func (d *customDialer) CustomDialTLSContext(ctx context.Context, network, address string) (net.Conn, error) {
	// Determine the network based on IPv4/IPv6 settings
	network = d.getNetworkType(network)
	if network == "" {
		return nil, errors.New("no supported network type available")
	}

	// Select appropriate proxy based on context, network type, and domain
	selectedProxy, err := d.selectProxy(ctx, network, address)
	if err != nil {
		return nil, err
	}

	var dialAddr string
	var IP net.IP

	if selectedProxy != nil && selectedProxy.needsHostname {
		// Remote DNS proxy (socks5h, socks4a, http, https)
		// Skip DNS archiving to avoid privacy leak and ensure accuracy.
		// The proxy will handle DNS resolution on its end, and we don't want to:
		// 1. Leak DNS queries to local DNS servers (defeats purpose of socks5h)
		// 2. Archive potentially incorrect DNS results (local DNS may differ from proxy's DNS)
		dialAddr = address
	} else {
		// Direct connection or local DNS proxy (socks5, socks4)
		// Archive DNS and use resolved IP
		IP, _, err = d.archiveDNS(ctx, address)
		if err != nil {
			return nil, err
		}

		// Extract port from address for IP:port construction
		var port string
		_, port, err = net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("failed to extract port from address %s: %w", address, err)
		}

		dialAddr = net.JoinHostPort(IP.String(), port)
	}

	var plainConn net.Conn

	if selectedProxy != nil {
		plainConn, err = selectedProxy.dialer.DialContext(ctx, network, dialAddr)
		if err != nil && selectedProxy.stats != nil {
			selectedProxy.stats.RegisterCounter(proxyErrorsTotal, proxyErrorsTotalHelp, []string{"proxy"}).WithLabels(Labels{"proxy": selectedProxy.name}).Add(1)
		}
	} else {
		if d.client.randomLocalIP {
			localAddr := getLocalAddr(network, IP)
			if localAddr != nil {
				switch network {
				case "tcp", "tcp4", "tcp6":
					d.LocalAddr = localAddr.(*net.TCPAddr)
				case "udp", "udp4", "udp6":
					d.LocalAddr = localAddr.(*net.UDPAddr)
				}
			}
		}

		plainConn, err = d.DialContext(ctx, network, dialAddr)
	}

	if err != nil {
		return nil, err
	}

	cfg := &tls.Config{
		ServerName:         address[:strings.LastIndex(address, ":")],
		InsecureSkipVerify: d.client.verifyCerts,
	}

	tlsConn := tls.UClient(plainConn, cfg, tls.HelloCustom)

	if err := tlsConn.ApplyPreset(getCustomTLSSpec()); err != nil {
		return nil, err
	}

	handshakeCtx, cancel := context.WithTimeout(ctx, d.client.TLSHandshakeTimeout)
	defer cancel()

	if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
		// Track TLS handshake errors for proxy connections
		if selectedProxy != nil && selectedProxy.stats != nil {
			selectedProxy.stats.RegisterCounter(proxyErrorsTotal, proxyErrorsTotalHelp, []string{"proxy"}).WithLabels(Labels{"proxy": selectedProxy.name}).Add(1)
		}
		closeErr := plainConn.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("CustomDialTLS: TLS handshake failed and closing plain connection failed: %s", closeErr.Error())
		}
		return nil, fmt.Errorf("CustomDialTLS: TLS handshake failed: %w", err)
	}

	return d.wrapConnection(ctx, tlsConn, "https"), nil
}

func (d *customDialer) CustomDialTLS(network, address string) (net.Conn, error) {
	return d.CustomDialTLSContext(context.Background(), network, address)
}

func (d *customDialer) getNetworkType(network string) string {
	switch network {
	case "tcp", "udp":
		if d.disableIPv4 && !d.disableIPv6 {
			return network + "6"
		}
		if !d.disableIPv4 && d.disableIPv6 {
			return network + "4"
		}
		return network // Both enabled or both disabled, use default
	case "tcp4", "udp4":
		if d.disableIPv4 {
			return ""
		}
		return network
	case "tcp6", "udp6":
		if d.disableIPv6 {
			return ""
		}
		return network
	default:
		return "" // Unsupported network type
	}
}

func (d *customDialer) writeWARCFromConnection(ctx context.Context, reqPipe, respPipe *io.PipeReader, scheme string, conn net.Conn) {
	defer d.client.WaitGroup.Done()

	// Check if a feedback channel has been provided in the context
	// Defer the closing of the channel in case of an early return without mixing signals when the batch was properly sent
	var feedbackChan chan struct{}
	batchSent := false
	if ctx.Value(ContextKeyFeedback) != nil {
		feedbackChan = ctx.Value(ContextKeyFeedback).(chan struct{})
		defer func() {
			if !batchSent {
				close(feedbackChan)
			}
		}()
	}

	var (
		batch      = NewRecordBatch(feedbackChan)
		recordChan = make(chan *Record, 2)
		recordIDs  []string
		err        = new(Error)
		errs       = errgroup.Group{}
		// Channels for passing the WARC-Target-URI between the request and response readers
		// These channels are used in a way so that both readers can synhronize themselves
		targetURIReqCh  = make(chan string, 1) // readRequest() -> readResponse() : readRequest() sends the WARC-Target-URI then closes the channel or closes without sending anything if an error occurs, readResponse() reads the WARC-Target-URI
		targetURIRespCh = make(chan string, 1) // readResponse() -> writeWARCFromConnection() : readResponse() sends the WARC-Target-URI then closes the channel or closes without sending anything if an error occurs, writeWARCFromConnection() reads the WARC-Target-URI
	)

	// Run request and response readers in parallel, respecting context
	errs.Go(func() error {
		return d.readRequest(ctx, scheme, reqPipe, targetURIReqCh, recordChan)
	})

	errs.Go(func() error {
		return d.readResponse(ctx, respPipe, targetURIReqCh, targetURIRespCh, recordChan)
	})

	// Wait for both goroutines to finish
	readErr := errs.Wait()
	close(recordChan)

	if readErr != nil {
		d.client.ErrChan <- &Error{
			Err:  readErr,
			Func: "writeWARCFromConnection",
		}

		for record := range recordChan {
			if closeErr := record.Content.Close(); closeErr != nil {
				d.client.ErrChan <- &Error{
					Err:  closeErr,
					Func: "writeWARCFromConnection",
				}
			}
		}

		return
	}

	for record := range recordChan {
		select {
		case <-ctx.Done():
			return
		default:
			recordIDs = append(recordIDs, uuid.NewString())
			batch.Records = append(batch.Records, record)
		}
	}

	if len(batch.Records) != 2 {
		err.Err = errors.New("warc: there was an unspecified problem creating one of the WARC records")
		d.client.ErrChan <- err

		for _, record := range batch.Records {
			if closeErr := record.Content.Close(); closeErr != nil {
				d.client.ErrChan <- &Error{
					Err:  closeErr,
					Func: "writeWARCFromConnection",
				}
			}
		}

		return
	}

	if batch.Records[0].Header.Get("WARC-Type") != "response" {
		slices.Reverse(batch.Records)
	}

	var warcTargetURI string
	select {
	case recv, ok := <-targetURIRespCh:
		if !ok {
			panic("writeWARCFromConnection: targetURIRespCh closed unexpectedly due to unhandled readRequest error or faulty code logic")
		}
		warcTargetURI = recv
	case <-ctx.Done():
		return
	}

	for i, r := range batch.Records {
		select {
		case <-ctx.Done():
			return
		default:
			if len(d.proxyDialers) == 0 {
				switch addr := conn.RemoteAddr().(type) {
				case *net.TCPAddr:
					IP := addr.IP.String()
					r.Header.Set("WARC-IP-Address", IP)
				}
			}

			r.Header.Set("WARC-Record-ID", "<urn:uuid:"+recordIDs[i]+">")

			if i == len(recordIDs)-1 {
				r.Header.Set("WARC-Concurrent-To", "<urn:uuid:"+recordIDs[0]+">")
			} else {
				r.Header.Set("WARC-Concurrent-To", "<urn:uuid:"+recordIDs[1]+">")
			}

			r.Header.Set("WARC-Target-URI", warcTargetURI)

			if _, seekErr := r.Content.Seek(0, 0); seekErr != nil {
				d.client.ErrChan <- &Error{
					Err:  seekErr,
					Func: "writeWARCFromConnection",
				}
				return
			}

			digest, err := GetDigest(r.Content, d.client.DigestAlgorithm)
			if err != nil {
				d.client.ErrChan <- &Error{
					Err:  err,
					Func: "writeWARCFromConnection",
				}
				return
			}

			r.Header.Set("WARC-Block-Digest", digest)
			r.Header.Set("Content-Length", strconv.Itoa(getContentLength(r.Content)))

			if d.client.dedupeOptions.LocalDedupe {
				if r.Header.Get("WARC-Type") == "response" && !slices.Contains(emptyPayloadDigests, r.Header.Get("WARC-Payload-Digest")) {
					captureTime, timeConversionErr := time.Parse(time.RFC3339, batch.CaptureTime)
					if timeConversionErr != nil {
						d.client.ErrChan <- &Error{
							Err:  timeConversionErr,
							Func: "writeWARCFromConnection.timeConversionErr",
						}
						return
					}
					d.client.dedupeHashTable.Store(r.Header.Get("WARC-Payload-Digest"), revisitRecord{
						responseUUID: recordIDs[i],
						size:         getContentLength(r.Content),
						targetURI:    warcTargetURI,
						date:         captureTime,
					})
				}
			}
		}
	}

	select {
	case d.client.WARCWriter <- batch:
		batchSent = true
	case <-ctx.Done():
		return
	}
}

func (d *customDialer) readResponse(ctx context.Context, respPipe *io.PipeReader, targetURIRxCh chan string, targetURITxCh chan string, recordChan chan *Record) error {
	defer close(targetURITxCh)

	// Initialize the response record
	var responseRecord = NewRecord(d.client.TempDir, d.client.FullOnDisk)

	recordChan <- responseRecord

	responseRecord.Header.Set("WARC-Type", "response")
	responseRecord.Header.Set("Content-Type", "application/http; msgtype=response")

	// Read the response from the pipe
	bytesCopied, err := io.Copy(responseRecord.Content, respPipe)
	if err != nil {
		return fmt.Errorf("readResponse: io.Copy failed: %s", err.Error())
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	resp, err := http.ReadResponse(bufio.NewReader(responseRecord.Content), nil)
	if err != nil {
		return err
	}

	// Grab the WARC-Target-URI and send it back for records post-processing
	var warcTargetURI, ok = <-targetURIRxCh
	if !ok {
		return errors.New("readResponse: WARC-Target-URI channel closed due to readRequest error")
	}

	targetURITxCh <- warcTargetURI

	// If the Discard Hook is set and returns true, discard the response
	if d.client.DiscardHook == nil {
		// no hook, do nothing
	} else if discarded, reason := d.client.DiscardHook(resp); discarded {
		err = resp.Body.Close()
		if err != nil {
			return &DiscardHookError{URL: warcTargetURI, Reason: reason, Err: fmt.Errorf("closing body failed: %w", err)}
		}

		return &DiscardHookError{URL: warcTargetURI, Reason: reason, Err: nil}
	}

	// Calculate the WARC-Payload-Digest
	payloadDigest, err := GetDigest(resp.Body, d.client.DigestAlgorithm)
	if err != nil {
		return fmt.Errorf("readResponse: payload digest calculation failed: %s", err.Error())
	}

	err = resp.Body.Close()
	if err != nil {
		return fmt.Errorf("readResponse: closing body after digest calculation failed: %s", err.Error())
	}

	responseRecord.Header.Set("WARC-Payload-Digest", payloadDigest)

	// Write revisit record if local, CDX, or Doppelganger dedupe is activated and finds match.
	var revisit = revisitRecord{}
	if bytesCopied >= int64(d.client.dedupeOptions.SizeThreshold) && !slices.Contains(emptyPayloadDigests, payloadDigest) {
		if d.client.dedupeOptions.LocalDedupe {
			revisit = d.checkLocalRevisit(payloadDigest)
			if revisit.targetURI != "" {
				d.stats.RegisterCounter(localDedupedBytesTotal, localDedupedBytesTotalHelp, nil).WithLabels(nil).Add(int64(revisit.size))
				d.stats.RegisterCounter(localDedupedTotal, localDedupedTotalHelp, nil).WithLabels(nil).Add(1)
			}
		}

		// If local dedupe does not find anything, we will check Doppelganger (if set) then CDX (if set).
		// TODO: Latest doppelganger dev branch does not support anything other than SHA1. This will be modified later.
		if d.client.dedupeOptions.DoppelgangerDedupe && d.client.DigestAlgorithm == SHA1 && revisit.targetURI == "" {
			revisit, _ = checkDoppelgangerRevisit(d.client.dedupeOptions.DoppelgangerHost, payloadDigest, warcTargetURI)
			if revisit.targetURI != "" {
				d.stats.RegisterCounter(doppelgangerDedupedBytesTotal, doppelgangerDedupedBytesTotalHelp, nil).WithLabels(nil).Add(bytesCopied)
				d.stats.RegisterCounter(doppelgangerDedupedTotal, doppelgangerDedupedTotalHelp, nil).WithLabels(nil).Add(1)
			}
		}

		// IA CDX dedupe does not support anything other than SHA1 at the moment. We should add a flag to support more.
		if d.client.dedupeOptions.CDXDedupe && d.client.DigestAlgorithm == SHA1 && revisit.targetURI == "" {
			revisit, _ = checkCDXRevisit(d.client.dedupeOptions.CDXURL, payloadDigest, warcTargetURI, d.client.dedupeOptions.CDXCookie)
			if revisit.targetURI != "" {
				d.stats.RegisterCounter(cdxDedupedBytesTotal, cdxDedupedBytesTotalHelp, nil).WithLabels(nil).Add(bytesCopied)
				d.stats.RegisterCounter(cdxDedupedTotal, cdxDedupedTotalHelp, nil).WithLabels(nil).Add(1)
			}
		}
	}

	if revisit.targetURI != "" && !slices.Contains(emptyPayloadDigests, payloadDigest) {
		responseRecord.Header.Set("WARC-Type", "revisit")
		responseRecord.Header.Set("WARC-Refers-To-Target-URI", revisit.targetURI)
		responseRecord.Header.Set("WARC-Refers-To-Date", revisit.date.Format(time.RFC3339Nano))

		if revisit.responseUUID != "" {
			responseRecord.Header.Set("WARC-Refers-To", "<urn:uuid:"+revisit.responseUUID+">")
		}

		responseRecord.Header.Set("WARC-Profile", "http://netpreserve.org/warc/1.1/revisit/identical-payload-digest")
		responseRecord.Header.Set("WARC-Truncated", "length")

		// Find the position of the end of the headers
		endOfHeadersOffset, err := findEndOfHeadersOffset(responseRecord.Content)
		if err != nil {
			return fmt.Errorf("readResponse: %s", err.Error())
		}
		// This should really never happen! This could be the result of a malfunctioning HTTP server or something currently unknown!
		if endOfHeadersOffset == -1 {
			return errors.New("readResponse: could not find the end of the headers")
		}

		// Write the data up until the end of the headers to a temporary buffer
		tempBuffer := spooledtempfile.NewSpooledTempFile("warc", d.client.TempDir, -1, d.client.FullOnDisk, d.client.MaxRAMUsageFraction)
		block := make([]byte, 1)
		wrote := 0
		responseRecord.Content.Seek(0, 0)
		for {
			n, err := responseRecord.Content.Read(block)
			if n > 0 {
				_, err = tempBuffer.Write(block)
				if err != nil {
					return fmt.Errorf("readResponse: could not write to temporary buffer: %s", err.Error())
				}
			}

			if err == io.EOF {
				break
			}

			if err != nil {
				return fmt.Errorf("readResponse: could not read from response content: %s", err.Error())
			}

			wrote++

			if wrote == endOfHeadersOffset {
				break
			}
		}

		// Close old buffer
		err = responseRecord.Content.Close()
		if err != nil {
			return fmt.Errorf("readResponse: could not close old content buffer: %s", err.Error())
		}
		responseRecord.Content = tempBuffer
	}

	return nil
}

// Scan a ReadSeeker for the sequence \r\n\r\n and return the offset just after it
func findEndOfHeadersOffset(content io.ReadSeeker) (int, error) {
	// Ensure reader is at the beginning
	if _, err := content.Seek(0, io.SeekStart); err != nil {
		return -1, fmt.Errorf("FindEndOfHeadersOffset: seek failed: %w", err)
	}

	found := false
	bigBlock := make([]byte, 0, 4)
	block := make([]byte, 1)
	endOfHeadersOffset := 0

	for {
		n, err := content.Read(block)
		if n > 0 {
			switch len(bigBlock) {
			case 0:
				if string(block) == "\r" {
					bigBlock = append(bigBlock, block...)
				}
			case 1:
				if string(block) == "\n" {
					bigBlock = append(bigBlock, block...)
				} else {
					bigBlock = nil
				}
			case 2:
				if string(block) == "\r" {
					bigBlock = append(bigBlock, block...)
				} else {
					bigBlock = nil
				}
			case 3:
				if string(block) == "\n" {
					bigBlock = append(bigBlock, block...)
					found = true
				} else {
					bigBlock = nil
				}
			}

			endOfHeadersOffset++

			if found {
				break
			}
		}

		if err == io.EOF {
			break
		}

		if err != nil {
			return -1, err
		}
	}

	if !found {
		return -1, errors.New("FindEndOfHeadersOffset: could not find the end of the headers")
	}

	return endOfHeadersOffset, nil
}

func parseRequestTargetURI(scheme string, content io.ReadSeeker) (string, error) {
	// Ensure the reader is at the beginning
	if _, err := content.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("parseRequestTargetURI: seek failed: %w", err)
	}

	reader := bufio.NewReaderSize(content, 4096)

	const (
		stateRequestLine = iota
		stateHeaders
	)

	var (
		target      string
		host        string
		state       = stateRequestLine
		foundHost   = false
		foundTarget = false
	)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", fmt.Errorf("parseRequestTargetURI: read line failed: %w", err)
		}

		line = strings.TrimSpace(line)

		switch state {
		case stateRequestLine:
			// Parse the request line (e.g., "GET /path HTTP/1.1")
			if isHTTPRequest(line) {
				parts := strings.Split(line, " ")
				if len(parts) >= 2 {
					target = parts[1] // Extract the target (path)
					foundTarget = true
				}
				state = stateHeaders
			}
		case stateHeaders:
			// Parse headers (e.g., "Host: example.com")
			if line == "" {
				break // End of headers
			}
			if strings.HasPrefix(strings.ToLower(line), "host: ") {
				host = strings.TrimSpace(line[6:])
				foundHost = true
			}
		}

		// If we've found both the target and host, we can stop parsing
		if foundHost && foundTarget {
			break
		}
	}

	if !foundTarget || !foundHost {
		return "", errors.New("parseRequestTargetURI: failed to parse host and target from request")
	}

	if strings.HasPrefix(target, scheme+"://"+host) {
		return target, nil
	}
	return fmt.Sprintf("%s://%s%s", scheme, host, target), nil
}

func (d *customDialer) readRequest(ctx context.Context, scheme string, reqPipe *io.PipeReader, targetURITxCh chan string, recordChan chan *Record) error {
	defer close(targetURITxCh)

	// Initialize the request record
	requestRecord := NewRecord(d.client.TempDir, d.client.FullOnDisk)

	recordChan <- requestRecord

	requestRecord.Header.Set("WARC-Type", "request")
	requestRecord.Header.Set("Content-Type", "application/http; msgtype=request")

	// Copy the content from the pipe
	_, err := io.Copy(requestRecord.Content, reqPipe)
	if err != nil {
		return fmt.Errorf("readRequest: io.Copy failed: %s", err.Error())
	}

	warcTargetURI, err := parseRequestTargetURI(scheme, requestRecord.Content)
	if err != nil {
		return fmt.Errorf("readRequest: %w", err)
	}

	// Send the WARC-Target-URI to a channel so that it can be picked up
	// by the goroutine responsible for writing the response
	select {
	case <-ctx.Done():
		return ctx.Err()
	case targetURITxCh <- warcTargetURI:
	}

	return nil
}
