module github.com/internetarchive/gowarc

go 1.24.2

require (
	github.com/armon/go-socks5 v0.0.0-20160902184237-e75332964ef5
	github.com/google/uuid v1.6.0
	github.com/klauspost/compress v1.18.0
	github.com/maypok86/otter v1.2.4
	github.com/miekg/dns v1.1.65
	github.com/paulbellamy/ratecounter v0.2.0
	github.com/refraction-networking/utls v1.6.7
	github.com/remeh/sizedwaitgroup v1.0.0
	github.com/spf13/cobra v1.9.1
	github.com/ulikunitz/xz v0.5.12
	go.uber.org/goleak v1.3.0
	golang.org/x/net v0.39.0
	golang.org/x/sync v0.13.0
)

require (
	github.com/andybalholm/brotli v1.1.1 // indirect
	github.com/cloudflare/circl v1.6.1 // indirect
	github.com/dolthub/maphash v0.1.0 // indirect
	github.com/gammazero/deque v1.0.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.6 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/mod v0.24.0 // indirect
	golang.org/x/sys v0.32.0 // indirect
	golang.org/x/tools v0.32.0 // indirect
)

// Unsure exactly where these versions came from, but no longer exist. If we plan to publish under these versions, we need to remove them from this retract list.
retract (
	v1.1.2
	v1.1.0
	v1.0.0
)
