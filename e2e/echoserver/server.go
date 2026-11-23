package echoserver

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/internetarchive/gowarc/e2e/echoserver/imperative"
)

type EchoServer struct {
	*httptest.Server
}

// shorthand to create a IPv4 local server
func New(t *testing.T, tls, http2 bool) *EchoServer { return newOn(t, "127.0.0.1", tls, http2) }

// shorthand to create a IPv6 local server
func NewIPv6(t *testing.T, tls, http2 bool) *EchoServer { return newOn(t, "::1", tls, http2) }

func newOn(t *testing.T, host string, tls, http2 bool) *EchoServer {
	t.Helper()
	// port is automatically assigned by the OS
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatalf("echoserver: listen %s: %v", host, err)
	}
	ts := httptest.NewUnstartedServer(newHandler(t))
	// close the listener to avoid port conflicts
	_ = ts.Listener.Close()
	// set the listener to the new one
	ts.Listener = ln
	ts.EnableHTTP2 = http2
	if tls {
		ts.StartTLS()
	} else {
		ts.Start()
	}
	t.Cleanup(ts.Close)
	return &EchoServer{ts}
}

func newHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("echoserver: read body: %v", err)
			return
		}
		imp, err := imperative.ParseImperative(r.Header.Get(imperative.ImperativeHeader))
		if err != nil {
			t.Errorf("echoserver: parse imperative: %v", err)
			return
		}
		// validate imperative
		if err := imp.Validate(); err != nil {
			t.Errorf("echoserver: validate imperative: %v", err)
			return
		}
		// assert request
		if err := imperative.AssertRequest(r, body); err != nil {
			t.Errorf("echoserver: assert request: %v", err)
			return
		}
		// set headers (overwrite)
		for k, v := range imp.Response.Headers {
			w.Header().Set(k, v)
		}
		// always echo back the imperative in response headers
		w.Header().Set(imperative.ImperativeHeader, imp.Encode())

		// opt. sleep before sending headers
		// go's scheduler parks handler goroutine automatically with time.Sleep
		if imp.Transport.Delay > 0 {
			time.Sleep(time.Duration(imp.Transport.Delay) * time.Millisecond)
		}

		// send headers with status code
		if imp.Response.StatusCode != 0 {
			w.WriteHeader(imp.Response.StatusCode)
		}

		// opt. yank the connection after sending the headers
		if imp.Transport.Abort {
			panic(http.ErrAbortHandler)
		}

		// build response body
		specifiedBody, err := imp.Response.Body.Build()
		if err != nil {
			t.Errorf("echoserver: build response body: %v", err)
			return
		}
		// write body
		if len(specifiedBody) > 0 {
			if _, err := w.Write(specifiedBody); err != nil {
				t.Errorf("echoserver: write body: %v", err)
			}
		}
	}
}
