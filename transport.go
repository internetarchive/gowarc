package warc

import (
	"compress/zlib"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	gzip "github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

type customTransport struct {
	t              http.Transport
	decompressBody bool
}

type readerAndCloser struct {
	io.Reader
	io.Closer
}

type multiCloser struct {
	closers []io.Closer
}

func newMultiCloser(closers ...io.Closer) *multiCloser {
	return &multiCloser{closers: closers}
}

func (m *multiCloser) Close() error {
	var err error
	for _, closer := range m.closers {
		if e := closer.Close(); e != nil {
			err = errors.Join(err, e)
		}
	}
	return err
}

func (t *customTransport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	req = req.Clone(req.Context())
	req.Header.Set("Accept-Encoding", "gzip, deflate, zstd")

	resp, err = t.t.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	// if the client have been created with decompressBody = true,
	// we decompress the resp.Body if we received a compressed body
	originalBody := resp.Body
	if t.decompressBody {
		switch strings.ToLower(resp.Header.Get("Content-Encoding")) {
		case "gzip":
			gzReader, err := gzip.NewReader(originalBody)
			if err != nil {
				originalBody.Close()
				return resp, err
			}
			resp.Body = &readerAndCloser{
				Reader: gzReader,
				Closer: newMultiCloser(originalBody, gzReader),
			}
		case "deflate":
			zlibReader, err := zlib.NewReader(originalBody)
			if err != nil {
				originalBody.Close()
				return resp, err
			}
			resp.Body = &readerAndCloser{
				Reader: zlibReader,
				Closer: newMultiCloser(originalBody, zlibReader),
			}
		case "zstd":
			zstdReader, err := zstd.NewReader(originalBody)
			if err != nil {
				originalBody.Close()
				return resp, err
			}
			zstdReaderCloser := zstdReader.IOReadCloser()
			resp.Body = &readerAndCloser{
				Reader: zstdReaderCloser,
				Closer: newMultiCloser(originalBody, zstdReaderCloser),
			}
		}
	}

	return resp, nil
}

func newCustomTransport(dialer *customDialer, decompressBody bool, TLSHandshakeTimeout time.Duration) (t *customTransport, err error) {
	t = new(customTransport)

	t.t = http.Transport{
		// configure HTTP transport
		Dial:           dialer.CustomDial,
		DialContext:    dialer.CustomDialContext,
		DialTLS:        dialer.CustomDialTLS,
		DialTLSContext: dialer.CustomDialTLSContext,

		// disable keep alive
		MaxConnsPerHost:       0,
		IdleConnTimeout:       -1,
		TLSHandshakeTimeout:   TLSHandshakeTimeout,
		ExpectContinueTimeout: 5 * time.Second,
		TLSNextProto:          make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
		DisableCompression:    true,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          -1,
		MaxIdleConnsPerHost:   -1,
		DisableKeepAlives:     true,
	}

	t.decompressBody = decompressBody

	return t, nil
}
