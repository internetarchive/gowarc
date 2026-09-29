package e2e

import (
	"bytes"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	warc "github.com/internetarchive/gowarc"
	"github.com/internetarchive/gowarc/e2e/echoserver"
	"github.com/internetarchive/gowarc/e2e/echoserver/imperative"
	"github.com/internetarchive/gowarc/e2e/proxy"
	"github.com/internetarchive/gowarc/e2e/warcvalidator"
)

// EchoServer configuration stacks
// 8 server stacks = plain/TLS × HTTP1/HTTP2 × IPv4/IPv6.
var loadStacks = []struct {
	name          string
	tls, h2, ipv6 bool
}{
	{"plain_H1_IPv4", false, false, false},
	{"plain_H1_IPv6", false, false, true},
	{"plain_H2_IPv4", false, true, false},
	{"plain_H2_IPv6", false, true, true},
	{"TLS_H1_IPv4", true, false, false},
	{"TLS_H1_IPv6", true, false, true},
	{"TLS_H2_IPv4", true, true, false},
	{"TLS_H2_IPv6", true, true, true},
}

// Set this env var to route the "direct" subtest's traffic through a
// proxy (e.g. Proxyman at http://127.0.0.1:9090) so request/responses can be
// inspected live. Empty = no proxy.
const proxyEnvVar = "GOWARC_E2E_PROXY"

// Runs twice: once without a proxy (opt. GOWARC_E2E_PROXY for debugging),
// and once through our counting SOCKS5 proxy.
func TestLoadWARCWritingHTTPClient(t *testing.T) {
	t.Run("direct", func(t *testing.T) {
		// you can supply your own proxy via the ENV var.
		// Good for debugging with e.g. Proxyman, mitmproxy et cetera.
		runLoad(t, os.Getenv(proxyEnvVar), nil)
	})
	t.Run("socks5", func(t *testing.T) {
		addr, counter := proxy.NewSOCKS5Proxy(t)
		runLoad(t, "socks5://"+addr, counter)
	})
}

// runLoad executes the full load scenario (>1k concurrent requests), asserts responses and the WARC files.
// optionally uses a proxy, and verifies that all requests were routed through it.
func runLoad(t *testing.T, proxyURL string, proxyCounter *atomic.Int64) {
	t.Helper()
	outDir, spoolDir := t.TempDir(), t.TempDir()

	if proxyURL != "" {
		t.Logf("routing through proxy: %s", proxyURL)
	}

	// Spin up an echoserver per stack.
	urls := make([]string, len(loadStacks))
	for i, s := range loadStacks {
		var srv *echoserver.EchoServer
		if s.ipv6 {
			srv = echoserver.NewIPv6(t, s.tls, s.h2)
		} else {
			srv = echoserver.New(t, s.tls, s.h2)
		}
		urls[i] = srv.URL
	}

	// 1 MiB static file to exercise imperative.Body.Filepath.
	staticFile := filepath.Join(t.TempDir(), "static.bin")
	if err := os.WriteFile(staticFile, bytes.Repeat([]byte{0xAB}, 1<<20), 0o644); err != nil {
		t.Fatalf("write static file: %v", err)
	}

	imps := buildLoadImperatives(staticFile)
	t.Logf("load: %d imperatives across %d stacks", len(imps), len(loadStacks))

	// gowarc client with many options turned on.
	rs := warc.NewRotatorSettings()
	rs.OutputDirectory = outDir
	rs.Prefix = "LOAD"
	rs.WARCSize = 20
	rs.WARCWriterPoolSize = 4
	rs.Compression = warc.CompressionGzip

	client, err := warc.NewWARCWritingHTTPClient(warc.HTTPClientSettings{
		RotatorSettings:       rs,
		TempDir:               spoolDir,
		Proxy:                 proxyURL,
		VerifyCerts:           false,
		FollowRedirects:       false,
		FullOnDisk:            false,
		MaxReadBeforeTruncate: 32 << 20,
		DialTimeout:           5 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		DedupeOptions:         warc.DedupeOptions{LocalDedupe: true, SizeThreshold: 1024},
		DigestAlgorithm:       warc.SHA1,
	})
	if err != nil {
		t.Fatalf("NewWARCWritingHTTPClient: %v", err)
	}
	var warcErrs atomic.Int64
	go func() {
		for e := range client.ErrChan {
			warcErrs.Add(1)
			t.Logf("WARC ErrChan: %v (func: %s)", e.Err, e.Func)
		}
	}()

	// Concurrent fan-out across all stacks.
	const workers = 32
	jobs := make(chan int, len(imps))
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				imp := imps[i]
				req, err := imp.BuildRequest(urls[i%len(urls)])
				if err != nil {
					t.Errorf("BuildRequest[%d]: %v", i, err)
					continue
				}
				resp, err := client.Do(req)
				if err != nil {
					if !imp.Aborts() {
						t.Errorf("Do[%d]: %v", i, err)
					}
					continue
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					if !imp.Aborts() {
						t.Errorf("read[%d]: %v", i, err)
					}
					continue
				}
				if !imp.Aborts() {
					if err := imperative.AssertResponse(resp, body); err != nil {
						t.Errorf("AssertResponse[%d]: %v", i, err)
					}
				}
			}
		}()
	}
	for i := range imps {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	if err := client.Close(); err != nil {
		t.Fatalf("client.Close: %v", err)
	}

	// Validate the WARC records, internally.
	verrs, tracker := warcvalidator.Check(outDir)
	for _, e := range verrs {
		t.Errorf("validation errors: %s", e.Error())
	}

	// Check the existence of every imperative in our resulting WARC files.
	for hash, group := range imps.Hash() {
		// requests that were aborted do not show up in the WARC at all.
		if group[0].Aborts() {
			continue
		}
		c := tracker[hash]
		// check that the sum of response + revisits records for that imperative
		// equals the number of times we sent it.
		if c.Response+c.Revisit != len(group) {
			t.Errorf("hash=%s: want %d response+revisit, got %d+%d", hash, len(group), c.Response, c.Revisit)
		}
		// check that at least one response record was written
		if c.Response == 0 {
			t.Errorf("hash=%s: no response record written", hash)
		}
		// check that the number of revisits equals the number of times we sent it, minus one (for the initial response)
		// WARNING: Since we race all these requests concurrently, it's possible that the local dedupe produces more than
		// one response record for equal payload digest. Added a safety margin of 20 to account for this.
		if c.Revisit < len(group)-21 {
			t.Errorf("hash=%s: want %d revisit records, got %d", hash, len(group)-1, c.Revisit)
		}
		// check that the number of request records equals the number of times we sent it.
		if c.Request != len(group) {
			t.Errorf("hash=%s: want %d request records, got %d", hash, len(group), c.Request)
		}
	}
	// we don't check the error counter against the number of imperatives that abort here,
	// because the loop above already checks the existence of every non-aborting imperative in the WARC files.
	if n := warcErrs.Load(); n > 0 {
		t.Logf("observed %d ErrChan errors (aborts expected)", n)
	}

	// If a proxy counter was supplied, check that the number of connections observed by the proxy
	// equals the number of imperatives that we sent.
	if proxyCounter != nil {
		n := proxyCounter.Load()
		if n < int64(len(imps)) {
			t.Errorf("proxy counter: want >=%d, got %d", len(imps), n)
		} else {
			t.Logf("proxy saw %d connections for %d imperatives", n, len(imps))
		}
	}
}

// Produces >1k imperatives covering every attribute that the Imperative struct supports.
func buildLoadImperatives(staticFile string) imperative.Imperatives {
	var (
		methods      = []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"}
		codes        = []int{200, 201, 400, 404, 418, 500, 503}
		contentTypes = []string{"application/json", "text/plain", "application/octet-stream", "text/html"}
		paths        = []string{"users", "events", "items", "docs", "stream"}
		encodings    = []string{imperative.EncodingUTF8, imperative.EncodingBinary}
		compressions = []string{"", "gzip", "deflate", "zstd"}
	)
	r := rand.New(rand.NewPCG(1337, 42))

	var imps imperative.Imperatives

	// 1000 diverse imperatives.
	for i := 0; i < 1000; i++ {
		method := methods[r.IntN(len(methods))]
		reqLen, reqSeed := 0, 0
		// Only methods that conventionally carry a body – HTTP/2 client transports
		// reject request bodies on GET/DELETE/OPTIONS.
		if method == "POST" || method == "PUT" || method == "PATCH" {
			reqLen = r.IntN(32 << 10)
			if reqLen > 0 {
				reqSeed = 1 + r.IntN(1_000_000)
			}
		}
		imps = append(imps, imperative.Imperative{
			Request: imperative.Request{
				Method:  method,
				Path:    "/api/" + paths[r.IntN(len(paths))] + "/" + strconv.Itoa(i),
				Headers: map[string]string{"X-Test-Run": "load", "X-Idx": strconv.Itoa(i)},
				Body:    imperative.Body{Length: reqLen, Seed: reqSeed, Encoding: encodings[r.IntN(len(encodings))]},
			},
			Response: imperative.Response{
				StatusCode: codes[r.IntN(len(codes))],
				Headers: map[string]string{
					"Content-Type":    contentTypes[r.IntN(len(contentTypes))],
					"X-Server-Marker": strconv.Itoa(i),
				},
				Body: imperative.Body{
					Length:   1 + r.IntN(128<<10),
					Seed:     1 + r.IntN(1_000_000),
					Encoding: encodings[r.IntN(len(encodings))],
					Compress: compressions[r.IntN(len(compressions))],
				},
			},
			Transport: imperative.Transport{Delay: r.IntN(1000)},
		})
	}

	// 100 identical requests → 1 response + 99 revisits with local dedupe enabled.
	// The .Seed() shorthand creates a number between 0 and 1 million, we use 2 million to prevent random collisions.
	dedupe := imperative.Imperative{
		Request: imperative.Request{Method: "GET", Path: "/cached/doc"},
		Response: imperative.Response{
			StatusCode: 200,
			Headers:    map[string]string{"Content-Type": "application/octet-stream"},
			Body:       imperative.Body{Length: 64*1024 + 7, Seed: 2_000_000_001, Encoding: imperative.EncodingBinary},
		},
	}
	imps = append(imps, dedupe.Multiply(100)...)

	// 20 mid-response disconnects – the WARC client must not crash or hang.
	abort := imperative.Imperative{
		Request: imperative.Request{Method: "GET", Path: "/flaky"},
		Response: imperative.Response{
			StatusCode: 200,
			Headers:    map[string]string{"Content-Type": "application/octet-stream"},
			Body:       imperative.Body{Length: 128*1024 + 11, Seed: 2_000_000_002, Encoding: imperative.EncodingBinary},
		},
		Transport: imperative.Transport{Abort: true},
	}
	imps = append(imps, abort.Multiply(20)...)

	// One Filepath-backed body
	imps = append(imps, imperative.Imperative{
		Request:  imperative.Request{Method: "GET", Path: "/static/blob"},
		Response: imperative.Response{StatusCode: 200, Headers: map[string]string{"Content-Type": "application/octet-stream"}, Body: imperative.Body{Filepath: staticFile}},
	})

	// randomly shuffle the imperatives
	return imps.Shuffle(77)
}
