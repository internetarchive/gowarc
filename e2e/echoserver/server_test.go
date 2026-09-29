package echoserver

import (
	"crypto/tls"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/internetarchive/gowarc/e2e/echoserver/imperative"
)

type serverStack struct {
	name string
	tls  bool
	h2   bool
	ipv6 bool // false → 127.0.0.1, true → ::1
}

// All combinations of TLS, HTTP/2, IPv4 and IPv6
var serverStacks = []serverStack{
	{name: "plain_H1_IPv4", tls: false, h2: false, ipv6: false},
	{name: "plain_H1_IPv6", tls: false, h2: false, ipv6: true},
	{name: "plain_H2_IPv4", tls: false, h2: true, ipv6: false},
	{name: "plain_H2_IPv6", tls: false, h2: true, ipv6: true},
	{name: "TLS_H1_IPv4", tls: true, h2: false, ipv6: false},
	{name: "TLS_H1_IPv6", tls: true, h2: false, ipv6: true},
	{name: "TLS_H2_IPv4", tls: true, h2: true, ipv6: false},
	{name: "TLS_H2_IPv6", tls: true, h2: true, ipv6: true},
}

// TestServer runs the same imperative scenarios on every server stack (TLS × HTTP/2 flag × IPv4/IPv6).
// This should prove that the echoserver follows the imperative specification correctly.
func TestServer(t *testing.T) {
	for _, st := range serverStacks {
		st := st
		t.Run(st.name, func(t *testing.T) {
			t.Parallel()
			srv := newServerForStack(t, st)
			baseURL := srv.URL
			client := httpClient()

			t.Run("GET /huge.pdf (10MB)", func(t *testing.T) {
				imp := imperative.Imperative{
					Request:  imperative.Request{Method: "GET", Path: "/huge.pdf"},
					Response: imperative.Response{StatusCode: 200, Body: imperative.Body{Length: 1024 * 1024 * 10, Encoding: imperative.EncodingBinary, Seed: 42}, Headers: map[string]string{"Content-Type": "application/pdf"}},
				}
				req, err := imp.BuildRequest(baseURL)
				if err != nil {
					t.Fatalf("BuildRequest: %v", err)
				}
				resp, body, err := doAndReadBody(client, req)
				if err != nil {
					t.Fatalf("doAndReadBody: %v", err)
				}
				if err := imperative.AssertResponse(resp, body); err != nil {
					t.Errorf("AssertResponse: %v", err)
				}
				// flip a single bit in the body
				body[512] ^= 1
				if err := imperative.AssertResponse(resp, body); err == nil {
					t.Errorf("AssertResponse should have failed with body mismatch")
				}
			})

			t.Run("GET /foo DELAY", func(t *testing.T) {
				imp := imperative.Imperative{
					Request:   imperative.Request{Method: "GET", Path: "/foo"},
					Response:  imperative.Response{StatusCode: 404, Body: imperative.Body{Length: 1024 * 1024, Encoding: imperative.EncodingUTF8, Seed: 42}, Headers: map[string]string{"Content-Type": "text/plain"}},
					Transport: imperative.Transport{Delay: 1000},
				}
				req, err := imp.BuildRequest(baseURL)
				if err != nil {
					t.Fatalf("BuildRequest: %v", err)
				}
				start := time.Now()
				resp, body, err := doAndReadBody(client, req)
				elapsed := time.Since(start)
				if elapsed < 1000*time.Millisecond {
					t.Errorf("expected delay ≥ 1000ms, got %v", elapsed)
				}
				if err != nil {
					t.Fatalf("doAndReadBody: %v", err)
				}
				if err := imperative.AssertResponse(resp, body); err != nil {
					t.Errorf("AssertResponse: %v", err)
				}
				// mutate a header value, assert that it fails
				resp.Header.Set("Content-Type", "application/octet-stream")
				if err := imperative.AssertResponse(resp, body); err == nil {
					t.Errorf("AssertResponse should have failed with header mismatch")
				}
			})

			t.Run("POST /users", func(t *testing.T) {
				imp := imperative.Imperative{
					Request:  imperative.Request{Method: "POST", Path: "/users", Headers: map[string]string{"Content-Type": "application/json"}, Body: imperative.Body{Length: 1024 * 512, Encoding: imperative.EncodingUTF8, Seed: 42}},
					Response: imperative.Response{StatusCode: 200, Body: imperative.Body{Length: 1024 * 1024, Encoding: imperative.EncodingUTF8, Seed: 42}, Headers: map[string]string{"Content-Type": "application/json"}},
				}
				req, err := imp.BuildRequest(baseURL)
				if err != nil {
					t.Fatalf("BuildRequest: %v", err)
				}
				resp, body, err := doAndReadBody(client, req)
				if err != nil {
					t.Fatalf("doAndReadBody: %v", err)
				}
				if err := imperative.AssertResponse(resp, body); err != nil {
					t.Errorf("AssertResponse: %v", err)
				}
			})

			t.Run("POST /users ABORT", func(t *testing.T) {
				imp := imperative.Imperative{
					Request:   imperative.Request{Method: "POST", Path: "/users", Headers: map[string]string{"Content-Type": "application/json"}, Body: imperative.Body{Length: 1024 * 512, Encoding: imperative.EncodingUTF8, Seed: 42}},
					Response:  imperative.Response{StatusCode: 200, Body: imperative.Body{Length: 1024, Encoding: imperative.EncodingUTF8, Seed: 42}, Headers: map[string]string{"Content-Type": "application/json"}},
					Transport: imperative.Transport{Abort: true},
				}
				req, err := imp.BuildRequest(baseURL)
				if err != nil {
					t.Fatalf("BuildRequest: %v", err)
				}
				resp, body, err := doAndReadBody(client, req)
				if err == nil {
					t.Fatal("doAndReadBody: expected EOF-style error, got nil")
				}
				// HTTP/1.1 raises EOF, HTTP/2 raises INTERNAL_ERROR stream error
				if !strings.Contains(err.Error(), "EOF") && !strings.Contains(err.Error(), "INTERNAL_ERROR") {
					t.Errorf("doAndReadBody: expected abort/EOF-style error, got %v", err)
				}
				if resp != nil && body != nil {
					t.Errorf("doAndReadBody: on abort expected no usable resp/body, got resp=%v len(body)=%d", resp != nil, len(body))
				}
			})

			t.Run("GET local file", func(t *testing.T) {
				// create local file in temp dir
				tempDir := t.TempDir()
				tempFile := filepath.Join(tempDir, "2MB.jpg")
				// unique size because this what we will compare against
				if err := os.WriteFile(tempFile, make([]byte, 2*1024*1024+1337), 0644); err != nil {
					t.Fatalf("os.WriteFile: %v", err)
				}

				imp := imperative.Imperative{
					Request:  imperative.Request{Method: "GET", Path: "/2MB.jpg"},
					Response: imperative.Response{StatusCode: 200, Body: imperative.Body{Filepath: tempFile}, Headers: map[string]string{"Content-Type": "image/jpeg"}},
				}
				req, err := imp.BuildRequest(baseURL)
				if err != nil {
					t.Fatalf("BuildRequest: %v", err)
				}
				resp, body, err := doAndReadBody(client, req)
				if err != nil {
					t.Fatalf("doAndReadBody: %v", err)
				}
				if err := imperative.AssertResponse(resp, body); err != nil {
					t.Errorf("AssertResponse: %v", err)
				}
				// sanity check if the file is correctly read
				if len(body) != 2*1024*1024+1337 {
					t.Errorf("body should be 2MB + 1337 bytes, got %d bytes", len(body))
				}

			})
		})
	}
}

// doAndReadBody sends the request and returns the raw response body bytes.
func doAndReadBody(client *http.Client, req *http.Request) (*http.Response, []byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, nil, err
	}
	return resp, body, nil
}

// creates a new echoserver for the given configuration
func newServerForStack(t *testing.T, st serverStack) *EchoServer {
	t.Helper()
	if st.ipv6 {
		return NewIPv6(t, st.tls, st.h2)
	}
	return New(t, st.tls, st.h2)
}

// shorthand for our http.Client
func httpClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		ForceAttemptHTTP2: true,
	}}
}
