package warc

import (
	"crypto/tls"
	"net/http"
	"time"

	gzip "github.com/klauspost/compress/gzip"
)

type customTransport struct {
	t              http.Transport
	decompressBody bool
}

func (t *customTransport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	req = req.Clone(req.Context())
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err = t.t.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	// if the client have been created with decompressBody = true,
	// we decompress the resp.Body if we received a compressed body
	if t.decompressBody {
		switch resp.Header.Get("Content-Encoding") {
		case "gzip":
			resp.Body, err = gzip.NewReader(resp.Body)
		}
	}

	return
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
		DisableCompression:    false,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          -1,
		MaxIdleConnsPerHost:   -1,
		DisableKeepAlives:     true,
	}

	t.decompressBody = decompressBody

	return t, nil
}
