module github.com/internetarchive/gowarc

go 1.25.4

require (
	github.com/google/uuid v1.6.0
	github.com/klauspost/compress v1.18.2
	github.com/maypok86/otter v1.2.4
	github.com/miekg/dns v1.1.68
	github.com/refraction-networking/utls v1.8.1
	github.com/remeh/sizedwaitgroup v1.0.0
	github.com/spf13/cobra v1.10.2
	github.com/things-go/go-socks5 v0.1.0
	github.com/ulikunitz/xz v0.5.15
	github.com/valyala/bytebufferpool v1.0.0
	github.com/zeebo/blake3 v0.2.4
	go.uber.org/goleak v1.3.0
	golang.org/x/net v0.47.0
	golang.org/x/sync v0.19.0
	golang.org/x/sys v0.39.0
)

// By default, and historically, this project uses klauspost's gzip implementation,
// which is faster than the standard library gzip, but comes at the cost of less predictable
// memory usage. It's widely used and stable but if you want to use the standard library gzip,
// you can build with the standard_gzip tag:
// go build -tags standard_gzip

require (
	github.com/andybalholm/brotli v1.1.1 // indirect
	github.com/dolthub/maphash v0.1.0 // indirect
	github.com/gammazero/deque v1.0.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/klauspost/cpuid/v2 v2.0.12 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	golang.org/x/crypto v0.44.0 // indirect
	golang.org/x/mod v0.24.0 // indirect
	golang.org/x/tools v0.33.0 // indirect
)

// Unsure exactly where these versions came from, but no longer exist. If we plan to publish under these versions, we need to remove them from this retract list.
retract (
	v1.1.2
	v1.1.0
	v1.0.0
)
