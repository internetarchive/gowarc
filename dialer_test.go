package warc

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

func TestGetNetworkType(t *testing.T) {
	d := &customDialer{}
	// Default: both disabled = default
	got := d.getNetworkType("tcp")
	if got != "tcp" {
		t.Errorf("expected tcp, got %s", got)
	}

	d.disableIPv4 = true
	got = d.getNetworkType("tcp")
	if got != "tcp6" {
		t.Errorf("expected tcp6, got %s", got)
	}

	d.disableIPv4 = false
	d.disableIPv6 = true
	got = d.getNetworkType("tcp")
	if got != "tcp4" {
		t.Errorf("expected tcp4, got %s", got)
	}

	got = d.getNetworkType("tcp4")
	if got != "tcp4" {
		t.Errorf("expected tcp4, got %s", got)
	}
	d.disableIPv4 = true
	got = d.getNetworkType("tcp4")
	if got != "" {
		t.Errorf("expected empty string, got %s", got)
	}
}

func TestParseRequestTargetURI(t *testing.T) {
	// valid minimal request
	raw := `GET /index.html HTTP/1.0
Host: example.com

`
	r := strings.NewReader(raw)
	uri, err := parseRequestTargetURI("http", io.NewSectionReader(r, 0, int64(len(raw))))
	if err != nil {
		t.Fatal(err)
	}
	expected := "http://example.com/index.html"
	if uri != expected {
		t.Errorf("expected %s, got %s", expected, uri)
	}

	// Request created by Chrome
	raw2 := `GET / HTTP/1.1
Host: foo.com
Connection: keep-alive
sec-ch-ua: "Chromium";v="126", "Not.A/Brand";v="8"
sec-ch-ua-mobile: ?0
sec-ch-ua-platform: "Windows"
Upgrade-Insecure-Requests: 1
User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36
Accept: text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8
Sec-Fetch-Site: none
Sec-Fetch-Mode: navigate
Sec-Fetch-User: ?1
Sec-Fetch-Dest: document
Accept-Encoding: gzip, deflate, br
Accept-Language: en-US,en;q=0.9
`
	r2 := strings.NewReader(raw2)
	uri2, err := parseRequestTargetURI("https", io.NewSectionReader(r2, 0, int64(len(raw))))
	if err != nil {
		t.Fatal(err)
	}
	expected2 := "https://foo.com/"
	if uri2 != expected2 {
		t.Errorf("expected %s, got %s", expected2, uri2)
	}

	// Invalid request, missing the Host header
	raw3 := `GET / HTTP/1.1
User-Agent: curl/7.79.1
Accept: */*
`
	r3 := strings.NewReader(raw3)
	uri3, err3 := parseRequestTargetURI("https", io.NewSectionReader(r3, 0, int64(len(raw))))
	if uri3 != "" {
		t.Fatalf("URI should be nil because the request is missing a Host. Found %v", uri3)
	}
	if err3.Error() != "parseRequestTargetURI: failed to parse host and target from request" {
		t.Fatalf("Unexpected error: %v", err3)
	}
}

func TestFindEndOfHeadersOffset(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
		wantErr  bool
	}{
		{
			name: "Simple headers with CRLF",
			input: "HTTP/1.1 200 OK\r\n" +
				"Content-Type: text/plain\r\n" +
				"\r\n" +
				"Body starts here",
			expected: 45,
			wantErr:  false,
		},
		{
			name:     "Headers with extra whitespace before end",
			input:    "GET / HTTP/1.1\r\nHost: test\r\n\r\nBody",
			expected: 30,
			wantErr:  false,
		},
		{
			name:     "Headers with no body",
			input:    "GET / HTTP/1.1\r\nHost: test\r\n\r\n",
			expected: 30,
			wantErr:  false,
		},
		{
			name:     "No end of headers",
			input:    "GET / HTTP/1.1\r\nHost: test\r\nNo end here",
			expected: -1,
			wantErr:  true,
		},
		{
			name:     "Multiple header blocks",
			input:    "GET / HTTP/1.1\r\nHost: test\r\n\r\nSecond block\r\n\r\n",
			expected: 30,
			wantErr:  false,
		},
		{
			name:     "End of headers at the very end",
			input:    "Header: value\r\n\r\n",
			expected: 17,
			wantErr:  false,
		},
		{
			name:     "LF only should not match",
			input:    "Header: value\n\n",
			expected: -1,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs := bytes.NewReader([]byte(tt.input))
			got, err := findEndOfHeadersOffset(rs)
			if (err != nil) != tt.wantErr {
				t.Errorf("findEndOfHeadersOffset() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.expected {
				t.Errorf("findEndOfHeadersOffset() = %v, expected %v", got, tt.expected)
			}
		})
	}
}

func TestProxySelection(t *testing.T) {
	t.Run("NoProxies", func(t *testing.T) {
		d := &customDialer{
			proxyDialers: []proxyDialerInfo{},
		}
		proxy, err := d.selectProxy(context.Background(), "tcp", "example.com:80")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if proxy != nil {
			t.Error("expected nil proxy when no proxies configured")
		}
	})

	t.Run("IPv4ProxyWithIPv4Network", func(t *testing.T) {
		d := &customDialer{
			proxyDialers: []proxyDialerInfo{
				{
					proxyNetwork: ProxyNetworkIPv4,
					proxyType:    ProxyTypeAny,
					url:          "socks5://ipv4-proxy:1080",
				},
			},
		}
		proxy, err := d.selectProxy(context.Background(), "tcp4", "example.com:80")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if proxy == nil {
			t.Error("expected proxy for IPv4 network with IPv4 proxy")
		}
		if proxy != nil && proxy.url != "socks5://ipv4-proxy:1080" {
			t.Errorf("expected socks5://ipv4-proxy:1080, got %s", proxy.url)
		}
	})

	t.Run("IPv4ProxyWithIPv6Network", func(t *testing.T) {
		d := &customDialer{
			proxyDialers: []proxyDialerInfo{
				{
					proxyNetwork: ProxyNetworkIPv4,
					proxyType:    ProxyTypeAny,
					url:          "socks5://ipv4-proxy:1080",
				},
			},
			allowDirectFallback: true,
		}
		proxy, err := d.selectProxy(context.Background(), "tcp6", "example.com:80")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if proxy != nil {
			t.Error("expected nil proxy for IPv6 network with IPv4 proxy")
		}
	})

	t.Run("IPv6ProxyWithIPv6Network", func(t *testing.T) {
		d := &customDialer{
			proxyDialers: []proxyDialerInfo{
				{
					proxyNetwork: ProxyNetworkIPv6,
					proxyType:    ProxyTypeAny,
					url:          "socks5://ipv6-proxy:1080",
				},
			},
		}
		proxy, err := d.selectProxy(context.Background(), "tcp6", "example.com:80")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if proxy == nil {
			t.Error("expected proxy for IPv6 network with IPv6 proxy")
		}
	})

	t.Run("DomainFiltering", func(t *testing.T) {
		d := &customDialer{
			proxyDialers: []proxyDialerInfo{
				{
					proxyNetwork:   ProxyNetworkAny,
					proxyType:      ProxyTypeAny,
					allowedDomains: []string{"*.example.com"},
					url:            "socks5://domain-proxy:1080",
				},
			},
		}

		// Should match subdomain
		proxy, err := d.selectProxy(context.Background(), "tcp", "api.example.com:443")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if proxy == nil {
			t.Error("expected proxy for matching domain")
		}

		// Should match base domain
		d.allowDirectFallback = true
		proxy, err = d.selectProxy(context.Background(), "tcp", "example.com:443")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if proxy == nil {
			t.Error("expected proxy for base domain")
		}

		// Should not match different domain
		proxy, err = d.selectProxy(context.Background(), "tcp", "other.com:443")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if proxy != nil {
			t.Error("expected nil proxy for non-matching domain")
		}
	})

	t.Run("ProxyTypeSelection", func(t *testing.T) {
		d := &customDialer{
			proxyDialers: []proxyDialerInfo{
				{
					proxyNetwork: ProxyNetworkAny,
					proxyType:    ProxyTypeAny,
					url:          "socks5://any-proxy:1080",
				},
				{
					proxyNetwork: ProxyNetworkAny,
					proxyType:    ProxyTypeMobile,
					url:          "socks5://mobile-proxy:1080",
				},
				{
					proxyNetwork: ProxyNetworkAny,
					proxyType:    ProxyTypeResidential,
					url:          "socks5://residential-proxy:1080",
				},
			},
		}

		// Without proxy type context, should use ProxyTypeAny proxy
		proxy, err := d.selectProxy(context.Background(), "tcp", "example.com:80")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if proxy == nil || proxy.url != "socks5://any-proxy:1080" {
			t.Error("expected any-proxy without proxy type context")
		}

		// With mobile proxy type context, should use mobile proxy
		ctx := WithProxyType(context.Background(), ProxyTypeMobile)
		proxy, err = d.selectProxy(ctx, "tcp", "example.com:80")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if proxy == nil || proxy.url != "socks5://mobile-proxy:1080" {
			t.Error("expected mobile-proxy with mobile proxy type context")
		}

		// With residential proxy type context, should use residential proxy
		ctx = WithProxyType(context.Background(), ProxyTypeResidential)
		proxy, err = d.selectProxy(ctx, "tcp", "example.com:80")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if proxy == nil || proxy.url != "socks5://residential-proxy:1080" {
			t.Error("expected residential-proxy with residential proxy type context")
		}
	})

	t.Run("RoundRobinSelection", func(t *testing.T) {
		d := &customDialer{
			proxyDialers: []proxyDialerInfo{
				{
					proxyNetwork: ProxyNetworkAny,
					proxyType:    ProxyTypeAny,
					url:          "socks5://proxy1:1080",
				},
				{
					proxyNetwork: ProxyNetworkAny,
					proxyType:    ProxyTypeAny,
					url:          "socks5://proxy2:1080",
				},
				{
					proxyNetwork: ProxyNetworkAny,
					proxyType:    ProxyTypeAny,
					url:          "socks5://proxy3:1080",
				},
			},
		}

		// Expected order for 3 complete cycles (9 selections)
		expectedOrder := []string{
			"socks5://proxy1:1080",
			"socks5://proxy2:1080",
			"socks5://proxy3:1080",
			"socks5://proxy1:1080",
			"socks5://proxy2:1080",
			"socks5://proxy3:1080",
			"socks5://proxy1:1080",
			"socks5://proxy2:1080",
			"socks5://proxy3:1080",
		}

		// Select proxies 9 times and verify sequential round-robin order
		for i := 0; i < 9; i++ {
			proxy, err := d.selectProxy(context.Background(), "tcp", "example.com:80")
			if err != nil {
				t.Errorf("iteration %d: unexpected error: %v", i, err)
			}
			if proxy == nil {
				t.Errorf("iteration %d: expected proxy, got nil", i)
			} else if proxy.url != expectedOrder[i] {
				t.Errorf("iteration %d: expected %s, got %s", i, expectedOrder[i], proxy.url)
			}
		}
	})

	t.Run("NoEligibleProxiesWithFallback", func(t *testing.T) {
		d := &customDialer{
			proxyDialers: []proxyDialerInfo{
				{
					proxyNetwork: ProxyNetworkIPv6,
					proxyType:    ProxyTypeAny,
					url:          "socks5://ipv6-proxy:1080",
				},
			},
			allowDirectFallback: true,
		}

		// IPv4 network with IPv6 proxy should use direct connection
		proxy, err := d.selectProxy(context.Background(), "tcp4", "example.com:80")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if proxy != nil {
			t.Error("expected nil proxy with direct fallback")
		}
	})

	t.Run("NoEligibleProxiesWithoutFallback", func(t *testing.T) {
		d := &customDialer{
			proxyDialers: []proxyDialerInfo{
				{
					proxyNetwork: ProxyNetworkIPv6,
					proxyType:    ProxyTypeAny,
					url:          "socks5://ipv6-proxy:1080",
				},
			},
			allowDirectFallback: false,
		}

		// IPv4 network with IPv6 proxy and no fallback should error
		proxy, err := d.selectProxy(context.Background(), "tcp4", "example.com:80")
		if err == nil {
			t.Error("expected error when no eligible proxies and no fallback")
		}
		if proxy != nil {
			t.Error("expected nil proxy")
		}
	})

	t.Run("ComplexFiltering", func(t *testing.T) {
		d := &customDialer{
			proxyDialers: []proxyDialerInfo{
				{
					proxyNetwork:   ProxyNetworkIPv4,
					proxyType:      ProxyTypeAny,
					allowedDomains: []string{"*.api.example.com"},
					url:            "socks5://api-ipv4-proxy:1080",
				},
				{
					proxyNetwork:   ProxyNetworkIPv6,
					proxyType:      ProxyTypeAny,
					allowedDomains: []string{"*.media.example.com"},
					url:            "socks5://media-ipv6-proxy:1080",
				},
				{
					proxyNetwork: ProxyNetworkAny,
					proxyType:    ProxyTypeMobile,
					url:          "socks5://mobile-proxy:1080",
				},
				{
					proxyNetwork: ProxyNetworkAny,
					proxyType:    ProxyTypeResidential,
					url:          "socks5://residential-proxy:1080",
				},
			},
		}

		// Test IPv4 API domain
		proxy, err := d.selectProxy(context.Background(), "tcp4", "service.api.example.com:443")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if proxy == nil || proxy.url != "socks5://api-ipv4-proxy:1080" {
			t.Error("expected api-ipv4-proxy for IPv4 API domain")
		}

		// Test IPv6 media domain
		proxy, err = d.selectProxy(context.Background(), "tcp6", "cdn.media.example.com:443")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if proxy == nil {
			t.Error("expected proxy for IPv6 media domain, got nil")
		} else if proxy.url != "socks5://media-ipv6-proxy:1080" {
			t.Errorf("expected media-ipv6-proxy for IPv6 media domain, got %s", proxy.url)
		}

		// Test mobile proxy type context
		ctx := WithProxyType(context.Background(), ProxyTypeMobile)
		proxy, err = d.selectProxy(ctx, "tcp", "example.com:80")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if proxy == nil || proxy.url != "socks5://mobile-proxy:1080" {
			t.Error("expected mobile-proxy with mobile proxy type context")
		}

		// Test residential proxy type context
		ctx = WithProxyType(context.Background(), ProxyTypeResidential)
		proxy, err = d.selectProxy(ctx, "tcp", "example.com:80")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if proxy == nil || proxy.url != "socks5://residential-proxy:1080" {
			t.Error("expected residential-proxy with residential proxy type context")
		}
	})
}
