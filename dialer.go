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

var emptyPayloadDigests = []string{
	"sha1:3I42H3S6NNFQ2MSVX7XZKYAYSCX5QBYJ",
	"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	"sha256:4OYMIQUY7QOBJGX36TEJS35ZEQT24QPEMSNZGTFESWMRW6CSXBKQ====",
	"blake3:af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262",
}

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
	if ctx.Value("wrappedConn") != nil {
		connChan, ok := ctx.Value("wrappedConn").(chan *CustomConnection)
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

			digest, err := GetDigest(r.Content, d.client.digestAlgorithm)
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
	payloadDigest, err := GetDigest(resp.Body, d.client.digestAlgorithm)
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
				LocalDedupeTotalBytes.Add(int64(revisit.size))
				LocalDedupeTotal.Add(1)
			}
		}

		// If local dedupe does not find anything, we will check Doppelganger (if set) then CDX (if set).
		// TODO: Latest doppelganger dev branch does not support anything other than SHA1. This will be modified later.
		if d.client.dedupeOptions.DoppelgangerDedupe && d.client.digestAlgorithm == SHA1 && revisit.targetURI == "" {
			revisit, _ = checkDoppelgangerRevisit(d.client.dedupeOptions.DoppelgangerHost, payloadDigest, warcTargetURI)
			if revisit.targetURI != "" {
				DoppelgangerDedupeTotalBytes.Add(bytesCopied)
				DoppelgangerDedupeTotal.Add(1)
			}
		}

		// IA CDX dedupe does not support anything other than SHA1 at the moment. We should add a flag to support more.
		if d.client.dedupeOptions.CDXDedupe && d.client.digestAlgorithm == SHA1 && revisit.targetURI == "" {
			revisit, _ = checkCDXRevisit(d.client.dedupeOptions.CDXURL, payloadDigest, warcTargetURI, d.client.dedupeOptions.CDXCookie)
			if revisit.targetURI != "" {
				CDXDedupeTotalBytes.Add(bytesCopied)
				CDXDedupeTotal.Add(1)
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

	return nil
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
