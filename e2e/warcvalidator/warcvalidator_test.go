package warcvalidator

import (
	"io"
	"math/rand/v2"
	"testing"

	warc "github.com/internetarchive/gowarc"
	"github.com/internetarchive/gowarc/e2e/echoserver"
	"github.com/internetarchive/gowarc/e2e/echoserver/imperative"
)

// TestCheck runs the Check method with real WARC files from gowarc.
func TestCheck(t *testing.T) {
	// Create output directories
	outDir := t.TempDir()
	spoolDir := t.TempDir()

	// Create and start echo server
	srv := echoserver.New(t, true, true)

	// Create gowarc client
	rs := warc.NewRotatorSettings()
	rs.OutputDirectory = outDir
	rs.Prefix = "TEST"
	rs.WARCSize = 10 << 20
	rs.WARCWriterPoolSize = 1

	client, err := warc.NewWARCWritingHTTPClient(warc.HTTPClientSettings{
		RotatorSettings: rs,
		TempDir:         spoolDir,
		VerifyCerts:     false,
		DedupeOptions:   warc.DedupeOptions{LocalDedupe: true, SizeThreshold: 512},
	})
	if err != nil {
		t.Fatalf("Failed to create WARC client: %v", err)
	}

	// Drain error channel
	go func() {
		for e := range client.ErrChan {
			t.Logf("WARC error: %v (func: %s)", e.Err, e.Func)
		}
	}()

	// Generate some imperatives
	var imps imperative.Imperatives
	for _ = range 100 {
		imps = append(imps, imperative.Imperative{
			Response: imperative.Response{
				StatusCode: randomDraw(200, 201, 404, 500),
				Headers:    randomHeaders(),
				Body: imperative.Body{
					Length: rand.IntN(1024 * 1024 * 5), // 5MB
				},
			},
			Request: imperative.Request{
				Path:    randomPath(),
				Headers: randomHeaders(),
				Method:  randomDraw("GET", "POST", "PUT", "DELETE", "OPTIONS"),
				Body: imperative.Body{
					Length: rand.IntN(1024 * 1024 * 1), // 1MB
				},
			},
			Transport: imperative.Transport{
				Abort: false,
			},
		})
	}

	// seed & shuffle
	imps = imps.Seed(1337).Shuffle(42)
	// double a few imperatives to test with duplicates
	imps = append(imps, imps[0].Multiply(10)...)
	// additional imperative to test with unfinished imperatives
	_ = append(imps, imperative.Imperative{
		Request: imperative.Request{
			Path:    "/test",
			Headers: map[string]string{"Content-Type": "text/html"},
			Method:  "GET",
		},
		Response: imperative.Response{
			StatusCode: 200,
			Headers:    map[string]string{"Content-Type": "text/html"},
			Body: imperative.Body{
				Length: 1024,
				Seed:   42,
			},
		},
	})

	// send requests using gowarc client
	for _, imp := range imps {
		req, err := imp.BuildRequest(srv.URL)
		if err != nil {
			t.Fatalf("Failed to build request: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			if imp.Aborts() {
				t.Logf("request error (expected when server aborts connection): %v", err)
				continue
			}
			t.Fatalf("Failed to send request: %v", err)
		}

		body, err := io.ReadAll(resp.Body)
		if closeErr := resp.Body.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		if err != nil {
			if imp.Aborts() {
				t.Logf("read response error (expected when server aborts connection): %v", err)
				continue
			}
			t.Fatalf("Failed to read response body: %v", err)
		}
		if err := imperative.AssertResponse(resp, body); err != nil {
			t.Fatalf("Failed to assert response: %v", err)
		}
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Failed to close client: %v", err)
	}

	// Run warcvalidator.Check
	errs, tracker := Check(outDir)
	if len(errs) > 0 {
		t.Fatalf("Check failed: %v", errs)
	}

	// log the tracker for debugging
	for impHash, count := range tracker {
		t.Logf("ImpHash: %s, Count: %+v\n", impHash, count)
	}

	// loop through all imperatives, look them up in the tracker and check if the counts are correct
	for hash, imps := range imps.Hash() {
		// how many imperatives with that hash are in our imps slice
		numImperatives := len(imps)
		// look up the counter for this imperative
		count := tracker[hash]

		// tifo: gowarc does not store request records for failed connections!
		if count.Request != numImperatives && !imps[0].Aborts() {
			t.Errorf("Request count mismatch: 1 got %d want %d", count.Request, 1)
		}
		// if the imperative does not abort, there should be one response
		// the other one's should be revisits
		if count.Response != 1 && !imps[0].Aborts() {
			t.Errorf("Response count mismatch: 2 got %d want %d", count.Response, 1)
		}
		if count.Response != 0 && imps[0].Aborts() {
			t.Errorf("Response count mismatch: imp aborts, got %d want 0", count.Response)
		}

		// yeah, complex logic here...
		if count.Revisit != numImperatives-1 && !imps[0].Aborts() || imps[0].Aborts() && count.Revisit != 0 {
			t.Errorf("Revisit count mismatch: 3 got %d want %d", count.Revisit, 1)
		}
	}
}

func stringWithCharset(length int, charset string) string {
	seededRand := rand.New(rand.NewPCG(1337, 42))
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[seededRand.IntN(len(charset))]
	}
	return string(b)
}

const headerCharset = "abcdefghijklmnopqrstuvwxyz0123456789-_"

// Returns a random map[string]string with header compatible values
func randomHeaders() map[string]string {
	headers := make(map[string]string)
	for i := 0; i < rand.IntN(3); i++ {
		headers[stringWithCharset(1+rand.IntN(16), headerCharset)] = stringWithCharset(1+rand.IntN(32), headerCharset)
	}
	return headers
}

const pathCharset = headerCharset + "/"

// Returns a random path string
func randomPath() string {
	return "/" + stringWithCharset(1+rand.IntN(16), pathCharset)
	// could also generate random query string
}

// randomDraw returns one random element from a slice of choices.
func randomDraw[T any](choices ...T) T {
	if len(choices) == 0 {
		var z T
		return z
	}
	return choices[rand.IntN(len(choices))]
}
