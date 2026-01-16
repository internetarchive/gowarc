package warc

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
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
			logBackend:   &noopLogger{},
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
			logBackend: &noopLogger{},
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
			logBackend:          &noopLogger{},
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
			logBackend: &noopLogger{},
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
			logBackend: &noopLogger{},
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
			logBackend: &noopLogger{},
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
			logBackend: &noopLogger{},
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
			logBackend:          &noopLogger{},
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
			logBackend:          &noopLogger{},
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
			logBackend: &noopLogger{},
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

// TestProxyStatsMetricNames tests that proxy metrics use labels to distinguish between proxies
func TestProxyStatsMetricNames(t *testing.T) {
	registry := newLocalRegistry()

	tests := []struct {
		name      string
		proxyName string
	}{
		{
			name:      "simple proxy",
			proxyName: "example_com_8080",
		},
		{
			name:      "IPv4 proxy",
			proxyName: "192_168_1_1_3128",
		},
		{
			name:      "IPv6 proxy",
			proxyName: "2001_db8__1_8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Register counters for this proxy using labels
			requestsCounter := registry.RegisterCounter(proxyRequestsTotal, proxyRequestsTotalHelp, []string{"proxy"}).WithLabels(Labels{"proxy": tt.proxyName})
			errorsCounter := registry.RegisterCounter(proxyErrorsTotal, proxyErrorsTotalHelp, []string{"proxy"}).WithLabels(Labels{"proxy": tt.proxyName})
			lastUsedGauge := registry.RegisterGauge(proxyLastUsedNanoseconds, proxyLastUsedNanosecondsHelp, []string{"proxy"}).WithLabels(Labels{"proxy": tt.proxyName})

			// Verify counters start at 0
			if requestsCounter.Get() != 0 {
				t.Errorf("Expected requests counter to start at 0, got %d", requestsCounter.Get())
			}
			if errorsCounter.Get() != 0 {
				t.Errorf("Expected errors counter to start at 0, got %d", errorsCounter.Get())
			}

			// Increment counters
			requestsCounter.Add(5)
			errorsCounter.Add(2)
			lastUsedGauge.Set(123456789)

			// Verify values are independent per proxy
			if requestsCounter.Get() != 5 {
				t.Errorf("Expected requests counter to be 5, got %d", requestsCounter.Get())
			}
			if errorsCounter.Get() != 2 {
				t.Errorf("Expected errors counter to be 2, got %d", errorsCounter.Get())
			}
			if lastUsedGauge.Get() != 123456789 {
				t.Errorf("Expected last used gauge to be 123456789, got %d", lastUsedGauge.Get())
			}
		})
	}

	// Verify that different proxy labels create independent counters
	proxy1Counter := registry.RegisterCounter(proxyRequestsTotal, proxyRequestsTotalHelp, []string{"proxy"}).WithLabels(Labels{"proxy": "example_com_8080"})
	proxy2Counter := registry.RegisterCounter(proxyRequestsTotal, proxyRequestsTotalHelp, []string{"proxy"}).WithLabels(Labels{"proxy": "192_168_1_1_3128"})

	if proxy1Counter.Get() != 5 {
		t.Errorf("Expected proxy1 counter to be 5 (from earlier test), got %d", proxy1Counter.Get())
	}
	if proxy2Counter.Get() != 5 {
		t.Errorf("Expected proxy2 counter to be 5 (from earlier test), got %d", proxy2Counter.Get())
	}
}

// TestProxyStatsRequestCount tests that proxy request counts are incremented correctly
func TestProxyStatsRequestCount(t *testing.T) {
	registry := newLocalRegistry()

	d := &customDialer{
		proxyDialers: []proxyDialerInfo{
			{
				proxyNetwork: ProxyNetworkAny,
				proxyType:    ProxyTypeAny,
				url:          "socks5://proxy1:1080",
				name:         "proxy1_1080",
				stats:        registry,
			},
			{
				proxyNetwork: ProxyNetworkAny,
				proxyType:    ProxyTypeAny,
				url:          "socks5://proxy2:1080",
				name:         "proxy2_1080",
				stats:        registry,
			},
		},
		logBackend: &noopLogger{},
	}

	// Select proxies multiple times and verify request counts
	for i := 0; i < 5; i++ {
		proxy, err := d.selectProxy(context.Background(), "tcp", "example.com:80")
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
		if proxy == nil {
			t.Fatalf("iteration %d: expected proxy, got nil", i)
		}
	}

	// Verify both proxies were used (round-robin)
	// With 5 selections: proxy1 should be used 3 times, proxy2 should be used 2 times
	proxy1Counter := registry.RegisterCounter(proxyRequestsTotal, proxyRequestsTotalHelp, []string{"proxy"}).WithLabels(Labels{"proxy": "proxy1_1080"})
	proxy2Counter := registry.RegisterCounter(proxyRequestsTotal, proxyRequestsTotalHelp, []string{"proxy"}).WithLabels(Labels{"proxy": "proxy2_1080"})

	if proxy1Counter.Get() != 3 {
		t.Errorf("Expected proxy1 request count 3, got %d", proxy1Counter.Get())
	}
	if proxy2Counter.Get() != 2 {
		t.Errorf("Expected proxy2 request count 2, got %d", proxy2Counter.Get())
	}
}

// TestProxyStatsLastUsed tests that proxy last used timestamps are updated
func TestProxyStatsLastUsed(t *testing.T) {
	registry := newLocalRegistry()

	d := &customDialer{
		proxyDialers: []proxyDialerInfo{
			{
				proxyNetwork: ProxyNetworkAny,
				proxyType:    ProxyTypeAny,
				url:          "socks5://proxy:1080",
				name:         "proxy_1080",
				stats:        registry,
			},
		},
		logBackend: &noopLogger{},
	}

	// Record time before selection
	timeBefore := time.Now().UnixNano()

	// Select proxy
	proxy, err := d.selectProxy(context.Background(), "tcp", "example.com:80")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proxy == nil {
		t.Fatal("expected proxy, got nil")
	}

	// Record time after selection
	timeAfter := time.Now().UnixNano()

	// Verify last used timestamp is within expected range
	lastUsedGauge := registry.RegisterGauge(proxyLastUsedNanoseconds, proxyLastUsedNanosecondsHelp, []string{"proxy"}).WithLabels(Labels{"proxy": "proxy_1080"})
	lastUsed := lastUsedGauge.Get()

	if lastUsed < timeBefore || lastUsed > timeAfter {
		t.Errorf("Expected last used timestamp between %d and %d, got %d", timeBefore, timeAfter, lastUsed)
	}

	// Wait a bit and select again
	time.Sleep(10 * time.Millisecond)
	timeBeforeSecond := time.Now().UnixNano()

	proxy, err = d.selectProxy(context.Background(), "tcp", "example.com:80")
	if err != nil {
		t.Fatalf("unexpected error on second selection: %v", err)
	}
	if proxy == nil {
		t.Fatal("expected proxy on second selection, got nil")
	}

	timeAfterSecond := time.Now().UnixNano()

	// Verify last used timestamp was updated
	lastUsedSecond := lastUsedGauge.Get()
	if lastUsedSecond < timeBeforeSecond || lastUsedSecond > timeAfterSecond {
		t.Errorf("Expected updated last used timestamp between %d and %d, got %d", timeBeforeSecond, timeAfterSecond, lastUsedSecond)
	}
	if lastUsedSecond <= lastUsed {
		t.Errorf("Expected last used timestamp to be updated, but %d <= %d", lastUsedSecond, lastUsed)
	}
}

// TestProxyStatsWithNilRegistry tests that proxy selection works when stats registry is nil
func TestProxyStatsWithNilRegistry(t *testing.T) {
	d := &customDialer{
		proxyDialers: []proxyDialerInfo{
			{
				proxyNetwork: ProxyNetworkAny,
				proxyType:    ProxyTypeAny,
				url:          "socks5://proxy:1080",
				name:         "proxy_1080",
				stats:        nil, // No stats registry
			},
		},
		logBackend: &noopLogger{},
	}

	// Should not panic when stats is nil
	proxy, err := d.selectProxy(context.Background(), "tcp", "example.com:80")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proxy == nil {
		t.Fatal("expected proxy, got nil")
	}
}

// TestProxyStatsMultipleProxiesRoundRobin tests that stats are correctly tracked across multiple proxies in round-robin
func TestProxyStatsMultipleProxiesRoundRobin(t *testing.T) {
	registry := newLocalRegistry()

	d := &customDialer{
		proxyDialers: []proxyDialerInfo{
			{
				proxyNetwork: ProxyNetworkAny,
				proxyType:    ProxyTypeAny,
				url:          "socks5://proxy1:1080",
				name:         "proxy1",
				stats:        registry,
			},
			{
				proxyNetwork: ProxyNetworkAny,
				proxyType:    ProxyTypeAny,
				url:          "socks5://proxy2:1080",
				name:         "proxy2",
				stats:        registry,
			},
			{
				proxyNetwork: ProxyNetworkAny,
				proxyType:    ProxyTypeAny,
				url:          "socks5://proxy3:1080",
				name:         "proxy3",
				stats:        registry,
			},
		},
		logBackend: &noopLogger{},
	}

	// Select proxies 12 times (4 complete round-robin cycles)
	for i := 0; i < 12; i++ {
		proxy, err := d.selectProxy(context.Background(), "tcp", "example.com:80")
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
		if proxy == nil {
			t.Fatalf("iteration %d: expected proxy, got nil", i)
		}
	}

	// Verify each proxy was used exactly 4 times
	for i := 1; i <= 3; i++ {
		proxyName := "proxy" + string(rune('0'+i))
		counter := registry.RegisterCounter(proxyRequestsTotal, proxyRequestsTotalHelp, []string{"proxy"}).WithLabels(Labels{"proxy": proxyName})

		expectedCount := int64(4)
		if counter.Get() != expectedCount {
			t.Errorf("Expected %s request count %d, got %d", proxyName, expectedCount, counter.Get())
		}
	}
}

// TestProxyStatsWithDomainFiltering tests that stats are only updated for eligible proxies
func TestProxyStatsWithDomainFiltering(t *testing.T) {
	registry := newLocalRegistry()

	d := &customDialer{
		proxyDialers: []proxyDialerInfo{
			{
				proxyNetwork:   ProxyNetworkAny,
				proxyType:      ProxyTypeAny,
				allowedDomains: []string{"*.example.com"},
				url:            "socks5://example-proxy:1080",
				name:           "example_proxy",
				stats:          registry,
			},
			{
				proxyNetwork:   ProxyNetworkAny,
				proxyType:      ProxyTypeAny,
				allowedDomains: []string{"*.test.com"},
				url:            "socks5://test-proxy:1080",
				name:           "test_proxy",
				stats:          registry,
			},
		},
		logBackend:          &noopLogger{},
		allowDirectFallback: true,
	}

	// Select proxy for example.com domain - should use example-proxy
	proxy, err := d.selectProxy(context.Background(), "tcp", "api.example.com:443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proxy == nil || proxy.name != "example_proxy" {
		t.Fatal("expected example-proxy")
	}

	// Select proxy for test.com domain - should use test-proxy
	proxy, err = d.selectProxy(context.Background(), "tcp", "api.test.com:443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proxy == nil || proxy.name != "test_proxy" {
		t.Fatal("expected test-proxy")
	}

	// Verify stats
	exampleCounter := registry.RegisterCounter(proxyRequestsTotal, proxyRequestsTotalHelp, []string{"proxy"}).WithLabels(Labels{"proxy": "example_proxy"})
	testCounter := registry.RegisterCounter(proxyRequestsTotal, proxyRequestsTotalHelp, []string{"proxy"}).WithLabels(Labels{"proxy": "test_proxy"})

	if exampleCounter.Get() != 1 {
		t.Errorf("Expected example_proxy request count 1, got %d", exampleCounter.Get())
	}
	if testCounter.Get() != 1 {
		t.Errorf("Expected test_proxy request count 1, got %d", testCounter.Get())
	}
}

// TestProxyStatsProxyTypeFiltering tests that stats work correctly with proxy type filtering
func TestProxyStatsProxyTypeFiltering(t *testing.T) {
	registry := newLocalRegistry()

	d := &customDialer{
		proxyDialers: []proxyDialerInfo{
			{
				proxyNetwork: ProxyNetworkAny,
				proxyType:    ProxyTypeMobile,
				url:          "socks5://mobile-proxy:1080",
				name:         "mobile_proxy",
				stats:        registry,
			},
			{
				proxyNetwork: ProxyNetworkAny,
				proxyType:    ProxyTypeResidential,
				url:          "socks5://residential-proxy:1080",
				name:         "residential_proxy",
				stats:        registry,
			},
		},
		logBackend: &noopLogger{},
	}

	// Select mobile proxy
	ctx := WithProxyType(context.Background(), ProxyTypeMobile)
	proxy, err := d.selectProxy(ctx, "tcp", "example.com:80")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proxy == nil || proxy.proxyType != ProxyTypeMobile {
		t.Fatal("expected mobile proxy")
	}

	// Select residential proxy
	ctx = WithProxyType(context.Background(), ProxyTypeResidential)
	proxy, err = d.selectProxy(ctx, "tcp", "example.com:80")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proxy == nil || proxy.proxyType != ProxyTypeResidential {
		t.Fatal("expected residential proxy")
	}

	// Verify stats
	mobileCounter := registry.RegisterCounter(proxyRequestsTotal, proxyRequestsTotalHelp, []string{"proxy"}).WithLabels(Labels{"proxy": "mobile_proxy"})
	residentialCounter := registry.RegisterCounter(proxyRequestsTotal, proxyRequestsTotalHelp, []string{"proxy"}).WithLabels(Labels{"proxy": "residential_proxy"})

	if mobileCounter.Get() != 1 {
		t.Errorf("Expected mobile_proxy request count 1, got %d", mobileCounter.Get())
	}
	if residentialCounter.Get() != 1 {
		t.Errorf("Expected residential_proxy request count 1, got %d", residentialCounter.Get())
	}
}
