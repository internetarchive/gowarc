package proxy

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.EnableHTTP2 = false
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func testClient(proxyURL *url.URL) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:             http.ProxyURL(proxyURL),
			DisableKeepAlives: true, // disable keep-alive to avoid connection re-use
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		}}
}

func TestSOCKS5Proxy(t *testing.T) {
	addr, count := NewSOCKS5Proxy(t)
	proxyURL, _ := url.Parse("socks5://" + addr)
	client := testClient(proxyURL)

	srv := newTestServer(t)

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("request to %s failed: %v", srv.URL, err)
	}
	resp.Body.Close()

	if got := count.Load(); got != 1 {
		t.Errorf("expected 1 proxied connection, got %d", got)
	}
}

func TestSOCKS5ProxyCountsEachConnection(t *testing.T) {
	addr, count := NewSOCKS5Proxy(t)

	proxyURL, _ := url.Parse("socks5://" + addr)
	client := testClient(proxyURL)

	srv := newTestServer(t)
	requests := 5
	for i := 0; i < requests; i++ {
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("request to %s failed: %v", srv.URL, err)
		}
		resp.Body.Close()
	}

	if got := count.Load(); got != int64(requests) {
		t.Errorf("expected %d proxied connections, got %d", requests, got)
	}
}
