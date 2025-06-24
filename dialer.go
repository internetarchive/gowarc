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

type customDialer struct {
	proxyDialer proxy.ContextDialer
	client      *CustomHTTPClient
	DNSConfig   *dns.ClientConfig
	DNSClient   *dns.Client
	DNSRecords  *otter.Cache[string, net.IP]
	net.Dialer
	disableIPv4 bool
	disableIPv6 bool
}

var emptyPayloadDigest = "3I42H3S6NNFQ2MSVX7XZKYAYSCX5QBYJ"

func newCustomDialer(httpClient *CustomHTTPClient, proxyURL string, DialTimeout, DNSRecordsTTL, DNSResolutionTimeout time.Duration, DNSCacheSize int, DNSServers []string, disableIPv4, disableIPv6 bool) (d *customDialer, err error) {
	d = new(customDialer)

	d.Timeout = DialTimeout
	d.client = httpClient
	d.disableIPv4 = disableIPv4
	d.disableIPv6 = disableIPv6

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

	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, err
		}

		var proxyDialer proxy.Dialer
		if proxyDialer, err = proxy.FromURL(u, d); err != nil {
			return nil, err
		}

		d.proxyDialer = proxyDialer.(proxy.ContextDialer)
	}

	return d, nil
}

type customConnection struct {
	net.Conn
	io.Reader
	io.Writer
	closers []io.Closer
	sync.WaitGroup
}

func (cc *customConnection) Read(b []byte) (int, error) {
	return cc.Reader.Read(b)
}

func (cc *customConnection) Write(b []byte) (int, error) {
	return cc.Writer.Write(b)
}

func (cc *customConnection) Close() error {
	for _, c := range cc.closers {
		err := c.Close()
		if err != nil {
			return err
		}
	}

	return cc.Conn.Close()
}

func (d *customDialer) wrapConnection(ctx context.Context, c net.Conn, scheme string) net.Conn {
	reqReader, reqWriter := io.Pipe()
	respReader, respWriter := io.Pipe()

	d.client.WaitGroup.Add(1)
	go d.writeWARCFromConnection(ctx, reqReader, respReader, scheme, c)

	return &customConnection{
		Conn:    c,
		closers: []io.Closer{reqWriter, respWriter},
		Reader:  io.TeeReader(c, respWriter),
		Writer:  io.MultiWriter(reqWriter, c),
	}
}

func (d *customDialer) CustomDialContext(ctx context.Context, network, address string) (conn net.Conn, err error) {
	// Determine the network based on IPv4/IPv6 settings
	network = d.getNetworkType(network)
	if network == "" {
		return nil, errors.New("no supported network type available")
	}

	IP, _, err := d.archiveDNS(ctx, address)
	if err != nil {
		return nil, err
	}

	if d.proxyDialer != nil {
		conn, err = d.proxyDialer.DialContext(ctx, network, address)
	} else {
		if d.client.randomLocalIP {
			localAddr := getLocalAddr(network, IP)
			if localAddr != nil {
				if network == "tcp" || network == "tcp4" || network == "tcp6" {
					d.LocalAddr = localAddr.(*net.TCPAddr)
				} else if network == "udp" || network == "udp4" || network == "udp6" {
					d.LocalAddr = localAddr.(*net.UDPAddr)
				}
			}
		}

		conn, err = d.DialContext(ctx, network, address)
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

	IP, _, err := d.archiveDNS(ctx, address)
	if err != nil {
		return nil, err
	}

	var plainConn net.Conn

	if d.proxyDialer != nil {
		plainConn, err = d.proxyDialer.DialContext(ctx, network, address)
	} else {
		if d.client.randomLocalIP {
			localAddr := getLocalAddr(network, IP)
			if localAddr != nil {
				if network == "tcp" || network == "tcp4" || network == "tcp6" {
					d.LocalAddr = localAddr.(*net.TCPAddr)
				} else if network == "udp" || network == "udp4" || network == "udp6" {
					d.LocalAddr = localAddr.(*net.UDPAddr)
				}
			}
		}

		plainConn, err = d.DialContext(ctx, network, address)
	}

	if err != nil {
		return nil, err
	}

	cfg := new(tls.Config)
	serverName := address[:strings.LastIndex(address, ":")]
	cfg.ServerName = serverName
	cfg.InsecureSkipVerify = d.client.verifyCerts

	tlsConn := tls.UClient(plainConn, cfg, tls.HelloCustom)

	if err := tlsConn.ApplyPreset(getCustomTLSSpec()); err != nil {
		return nil, err
	}

	errc := make(chan error, 2)
	timer := time.AfterFunc(d.client.TLSHandshakeTimeout, func() {
		errc <- errors.New("TLS handshake timeout")
	})

	go func() {
		err := tlsConn.HandshakeContext(ctx)
		timer.Stop()
		errc <- err
	}()
	if err := <-errc; err != nil {
		closeErr := plainConn.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("CustomDialTLS: TLS handshake failed and closing plain connection failed: %s", closeErr.Error())
		}

		return nil, err
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
	if ctx.Value("feedback") != nil {
		feedbackChan = ctx.Value("feedback").(chan struct{})
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
			if d.proxyDialer == nil {
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

			r.Header.Set("WARC-Block-Digest", "sha1:"+GetSHA1(r.Content))
			r.Header.Set("Content-Length", strconv.Itoa(getContentLength(r.Content)))

			if d.client.dedupeOptions.LocalDedupe {
				if r.Header.Get("WARC-Type") == "response" && r.Header.Get("WARC-Payload-Digest")[5:] != emptyPayloadDigest {
					d.client.dedupeHashTable.Store(r.Header.Get("WARC-Payload-Digest")[5:], revisitRecord{
						responseUUID: recordIDs[i],
						size:         getContentLength(r.Content),
						targetURI:    warcTargetURI,
						date:         batch.CaptureTime,
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
	responseRecord.Header.Set("WARC-Type", "response")
	responseRecord.Header.Set("Content-Type", "application/http; msgtype=response")

	// Read the response from the pipe
	bytesCopied, err := io.Copy(responseRecord.Content, respPipe)
	if err != nil {
		closeErr := responseRecord.Content.Close()
		if closeErr != nil {
			return fmt.Errorf("readResponse: io.Copy failed and closing content failed: %s", closeErr.Error())
		}

		return fmt.Errorf("readResponse: io.Copy failed: %s", err.Error())
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	resp, err := http.ReadResponse(bufio.NewReader(responseRecord.Content), nil)
	if err != nil {
		closeErr := responseRecord.Content.Close()
		if closeErr != nil {
			return fmt.Errorf("readResponse: http.ReadResponse failed and closing content failed: %s", closeErr.Error())
		}

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
		err = responseRecord.Content.Close()
		if err != nil {
			return &DiscardHookError{URL: warcTargetURI, Reason: reason, Err: fmt.Errorf("closing content failed: %w", err)}
		}

		return &DiscardHookError{URL: warcTargetURI, Reason: reason, Err: nil}
	}

	// Calculate the WARC-Payload-Digest
	payloadDigest := GetSHA1(resp.Body)
	if strings.HasPrefix(payloadDigest, "ERROR: ") {
		closeErr := responseRecord.Content.Close()
		if closeErr != nil {
			return fmt.Errorf("readResponse: SHA1 calculation failed and closing content failed: %s", closeErr.Error())
		}

		// This should _never_ happen.
		return fmt.Errorf("readResponse: SHA1 ran into an unrecoverable error: %s url: %s", payloadDigest, warcTargetURI)
	}

	err = resp.Body.Close()
	if err != nil {
		return fmt.Errorf("readResponse: closing body after SHA1 calculation failed: %s", err.Error())
	}

	responseRecord.Header.Set("WARC-Payload-Digest", "sha1:"+payloadDigest)

	// Write revisit record if local, CDX, or Doppelganger dedupe is activated and finds match.
	var revisit = revisitRecord{}
	if bytesCopied >= int64(d.client.dedupeOptions.SizeThreshold) && payloadDigest != emptyPayloadDigest {
		if d.client.dedupeOptions.LocalDedupe {
			revisit = d.checkLocalRevisit(payloadDigest)
			if revisit.targetURI != "" {
				atomic.AddInt64(LocalDedupeTotalBytes, int64(revisit.size))
				atomic.AddInt64(LocalDedupeTotal, 1)
			}
		}

		// If local dedupe does not find anything, we will check Doppelganger (if set) then CDX (if set).
		if d.client.dedupeOptions.DoppelgangerDedupe && revisit.targetURI == "" {
			revisit, _ = checkDoppelgangerRevisit(d.client.dedupeOptions.DoppelgangerHost, payloadDigest, warcTargetURI)
			if revisit.targetURI != "" {
				atomic.AddInt64(DoppelgangerDedupeTotalBytes, int64(bytesCopied))
				atomic.AddInt64(DoppelgangerDedupeTotal, 1)
			}
		}

		if d.client.dedupeOptions.CDXDedupe && revisit.targetURI == "" {
			revisit, _ = checkCDXRevisit(d.client.dedupeOptions.CDXURL, payloadDigest, warcTargetURI, d.client.dedupeOptions.CDXCookie)
			if revisit.targetURI != "" {
				atomic.AddInt64(CDXDedupeTotalBytes, int64(bytesCopied))
				atomic.AddInt64(CDXDedupeTotal, 1)
			}
		}
	}

	if revisit.targetURI != "" && payloadDigest != emptyPayloadDigest {
		responseRecord.Header.Set("WARC-Type", "revisit")
		responseRecord.Header.Set("WARC-Refers-To-Target-URI", revisit.targetURI)
		responseRecord.Header.Set("WARC-Refers-To-Date", revisit.date)

		if revisit.responseUUID != "" {
			responseRecord.Header.Set("WARC-Refers-To", "<urn:uuid:"+revisit.responseUUID+">")
		}

		responseRecord.Header.Set("WARC-Profile", "http://netpreserve.org/warc/1.1/revisit/identical-payload-digest")
		responseRecord.Header.Set("WARC-Truncated", "length")

		// Find the position of the end of the headers
		_, err := responseRecord.Content.Seek(0, 0)
		if err != nil {
			return fmt.Errorf("readResponse: could not seek to the beginning of the content: %s", err.Error())
		}

		found := false
		bigBlock := make([]byte, 0, 4)
		block := make([]byte, 1)
		endOfHeadersOffset := 0
		for {
			n, err := responseRecord.Content.Read(block)
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
				return err
			}
		}

		// This should really never happen! This could be the result of a malfunctioning HTTP server or something currently unknown!
		if endOfHeadersOffset == -1 {
			return errors.New("readResponse: could not find the end of the headers")
		}

		// Write the data up until the end of the headers to a temporary buffer
		tempBuffer := spooledtempfile.NewSpooledTempFile("warc", d.client.TempDir, -1, d.client.FullOnDisk, d.client.MaxRAMUsageFraction)
		block = make([]byte, 1)
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

	recordChan <- responseRecord

	return nil
}

func (d *customDialer) readRequest(ctx context.Context, scheme string, reqPipe *io.PipeReader, targetURITxCh chan string, recordChan chan *Record) error {
	defer close(targetURITxCh)

	var (
		warcTargetURI = scheme + "://"
		requestRecord = NewRecord(d.client.TempDir, d.client.FullOnDisk)
	)

	// Initialize the request record
	requestRecord.Header.Set("WARC-Type", "request")
	requestRecord.Header.Set("Content-Type", "application/http; msgtype=request")

	// Copy the content from the pipe
	_, err := io.Copy(requestRecord.Content, reqPipe)
	if err != nil {
		return fmt.Errorf("readRequest: io.Copy failed: %s", err.Error())
	}

	// Seek to the beginning of the content to allow reading
	if _, err := requestRecord.Content.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("readRequest: seek failed: %s", err.Error())
	}

	// Use a buffered reader for efficient parsing
	reader := bufio.NewReaderSize(requestRecord.Content, 4096) // 4KB buffer

	// State machine to parse the request
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
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("readRequest: failed to read line: %v", err)
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

			if strings.HasPrefix(line, "Host: ") {
				host = strings.TrimPrefix(line, "Host: ")
				foundHost = true
			}
		}

		// If we've found both the target and host, we can stop parsing
		if foundHost && foundTarget {
			break
		}
	}

	// Check that we successfully parsed all necessary data
	if host != "" && target != "" {
		// HTTP's request first line can include a complete path, we check that
		if strings.HasPrefix(target, scheme+"://"+host) {
			warcTargetURI = target
		} else {
			warcTargetURI += host + target
		}
	} else {
		return errors.New("unable to parse data necessary for WARC-Target-URI")
	}

	// Send the WARC-Target-URI to a channel so that it can be picked up
	// by the goroutine responsible for writing the response
	select {
	case <-ctx.Done():
		return ctx.Err()
	case targetURITxCh <- warcTargetURI:
	}

	// Send the request record to the channel for further processing
	select {
	case <-ctx.Done():
		return ctx.Err()
	case recordChan <- requestRecord:
	}

	return nil
}
