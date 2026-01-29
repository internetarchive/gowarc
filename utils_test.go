package warc

import (
	"net/url"
	"testing"
)

// Tests for the NewRotatorSettings function
func TestNewRotatorSettings(t *testing.T) {
	rotatorSettings := NewRotatorSettings()

	if rotatorSettings.Prefix != "WARC" {
		t.Error("Failed to set WARC rotator's filename prefix")
	}

	if rotatorSettings.WARCSize != 1000 {
		t.Error("Failed to set WARC rotator's WARC size")
	}

	if rotatorSettings.OutputDirectory != "./" {
		t.Error("Failed to set WARC rotator's output directory")
	}

	if rotatorSettings.Compression != "GZIP" {
		t.Error("Failed to set WARC rotator's compression algorithm")
	}

	if rotatorSettings.CompressionDictionary != "" {
		t.Error("Failed to set WARC rotator's compression dictionary")
	}
}

// Tests for the isLineStartingWithHTTPMethod function
func TestIsHTTPRequest(t *testing.T) {
	goodHTTPRequestHeaders := []string{
		"GET /index.html HTTP/1.1",
		"POST /api/login HTTP/1.1",
		"DELETE /api/products/456 HTTP/1.1",
		"HEAD /about HTTP/1.0",
		"OPTIONS / HTTP/1.1",
		"PATCH /api/item/789 HTTP/1.1",
		"GET /images/logo.png HTTP/1.1",
	}

	for _, header := range goodHTTPRequestHeaders {
		if !isHTTPRequest(header) {
			t.Error("Invalid HTTP Method parsing:", header)
		}
	}
}

// Tests for the proxyName function
func TestProxyName(t *testing.T) {
	tests := []struct {
		name     string
		urlStr   string
		expected string
	}{
		{
			name:     "domain with explicit port",
			urlStr:   "http://example.com:8080",
			expected: "example_com_8080",
		},
		{
			name:     "domain without port (http defaults to empty, should use 80)",
			urlStr:   "http://example.com",
			expected: "example_com_80",
		},
		{
			name:     "domain without port (https defaults to empty, should use 80)",
			urlStr:   "https://example.com",
			expected: "example_com_80",
		},
		{
			name:     "domain with subdomain and port",
			urlStr:   "http://api.example.com:3000",
			expected: "api_example_com_3000",
		},
		{
			name:     "domain with subdomain without port",
			urlStr:   "http://api.example.com",
			expected: "api_example_com_80",
		},
		{
			name:     "localhost with port",
			urlStr:   "http://localhost:8080",
			expected: "localhost_8080",
		},
		{
			name:     "localhost without port",
			urlStr:   "http://localhost",
			expected: "localhost_80",
		},
		{
			name:     "IPv4 address with port",
			urlStr:   "http://192.168.1.1:8080",
			expected: "192_168_1_1_8080",
		},
		{
			name:     "IPv4 address without port",
			urlStr:   "http://192.168.1.1",
			expected: "192_168_1_1_80",
		},
		{
			name:     "IPv6 address with port",
			urlStr:   "http://[2001:db8::1]:8080",
			expected: "2001_db8__1_8080",
		},
		{
			name:     "IPv6 address without port",
			urlStr:   "http://[2001:db8::1]",
			expected: "2001_db8__1_80",
		},
		{
			name:     "domain with port 443",
			urlStr:   "https://example.com:443",
			expected: "example_com_443",
		},
		{
			name:     "domain with multiple subdomains",
			urlStr:   "http://api.v2.example.com:9000",
			expected: "api_v2_example_com_9000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.urlStr)
			if err != nil {
				t.Fatalf("Failed to parse URL %s: %v", tt.urlStr, err)
			}

			result := proxyName(u)
			if result != tt.expected {
				t.Errorf("proxyName(%s) = %s, expected %s", tt.urlStr, result, tt.expected)
			}
		})
	}
}
