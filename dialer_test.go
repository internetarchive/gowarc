package warc

import (
	"bytes"
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
