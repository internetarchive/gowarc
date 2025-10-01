# warc

[![GoDoc](https://godoc.org/github.com/internetarchive/gowarc?status.svg)](https://godoc.org/github.com/internetarchive/gowarc)
[![Go Report Card](https://goreportcard.com/badge/github.com/internetarchive/gowarc)](https://goreportcard.com/report/github.com/internetarchive/gowarc)

A Go library for reading and writing [WARC files](https://iipc.github.io/warc-specifications/), with advanced features for web archiving.

## Features

- Read and write WARC files with support for multiple compression formats (GZIP, ZSTD)
- HTTP client with built-in WARC recording capabilities
- Content deduplication (local URL-agnostic and CDX-based)
- Configurable file rotation and size limits
- DNS caching and custom DNS resolution (with DNS archiving)
- Support for socks5 proxies and custom TLS configurations
- Random local IP assignment for distributed crawling (including Linux kernel AnyIP feature)
- Smart memory management with disk spooling options
- IPv4/IPv6 support with configurable preferences

## Installation

```bash
go get github.com/internetarchive/gowarc
```

## Usage

This library's biggest feature is to provide a standard HTTP client through which you can execute requests that will be recorded automatically to WARC files. It's the basis of [Zeno](https://github.com/internetarchive/Zeno).

### HTTP Client with WARC Recording

```go
package main

import (
	"context"
	"fmt"
	"github.com/internetarchive/gowarc"
	"io"
	"net/http"
	"time"
)

func main() {
	// Configure WARC settings
	rotatorSettings := &warc.RotatorSettings{
		WarcinfoContent: warc.Header{
			"software": "My WARC writing client v1.0",
		},
		Prefix:             "WEB",
		Compression:        "gzip",
		WARCWriterPoolSize: 4, // Records will be written to 4 WARC files in parallel, it helps maximize the disk IO on some hardware. To be noted, even if we have multiple WARC writers, WARCs are ALWAYS written by pair in the same file. (req/resp pair)
	}

	// Configure HTTP client settings
	clientSettings := warc.HTTPClientSettings{
		RotatorSettings: rotatorSettings,
		Proxy:           "socks5://proxy.example.com:1080",
		TempDir:         "./temp",
		DNSServers:      []string{"8.8.8.8", "8.8.4.4"},
		DedupeOptions: warc.DedupeOptions{
			LocalDedupe:   true,
			CDXDedupe:     false,
			SizeThreshold: 2048, // Only payloads above that threshold will be deduped
		},
		DialTimeout:           10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DNSResolutionTimeout:  5 * time.Second,
		DNSRecordsTTL:         5 * time.Minute,
		DNSCacheSize:          10000,
		MaxReadBeforeTruncate: 1000000000,
		DecompressBody:        true,
		FollowRedirects:       true,
		VerifyCerts:           true,
		RandomLocalIP:         true,
	}

	// Create HTTP client
	client, err := warc.NewWARCWritingHTTPClient(clientSettings)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// The error channel NEED to be consumed, else it will block the
	// execution of the WARC module
	go func() {
		for err := range client.ErrChan {
			fmt.Errorf("WARC writer error: %s", err.Err.Error())
		}
	}()

	// This is optional but the module give a feedback on a channel passed as context value "feedback" to the
	// request, this helps knowing when the record has been written to disk. If this is not used, the WARC
	// writing is asynchronous
	req, err := http.NewRequest("GET", "https://archive.org", nil)
	if err != nil {
		panic(err)
	}

	feedbackChan := make(chan struct{}, 1)
	req = req.WithContext(context.WithValue(req.Context(), "feedback", feedbackChan))

	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	// Process response
	// Note: the body NEED to be consumed to be written to the WARC file.
	io.Copy(io.Discard, resp.Body)

	// Will block until records are actually written to the WARC file
	<-feedbackChan
}
```

## Build tags

- `standard_gzip`: Use the standard library gzip implementation instead of the faster one from [klauspost](https://github.com/klauspost/compress)
- `klauspost_gzip`: Use the faster gzip implementation from [klauspost](https://github.com/klauspost/compress) (default, you don't need to specify it)

## License

This module is released under CC0 license.
You can find a copy of the CC0 License in the [LICENSE](./LICENSE) file.
