package warc

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/maypok86/otter"
	"github.com/miekg/dns"
)

const (
	invalidDNS = "198.51.100.0"
	publicDNS  = "8.8.8.8"

	nxdomain   = "warc.faketld:443"
	targetHost = "www.google.com"
	target     = "www.google.com:443"
	target1    = "www.archive.org:443"
)

func newTestCustomDialer() (d *customDialer) {
	d = new(customDialer)

	DNScache, err := otter.MustBuilder[string, net.IP](1000).
		WithTTL(1 * time.Hour).
		Build()
	if err != nil {
		panic(err)
	}

	d.DNSRecords = &DNScache

	d.DNSConfig = &dns.ClientConfig{
		Port: "53",
	}
	d.DNSClient = &dns.Client{
		Timeout: 2 * time.Second,
	}

	return d
}

func setup(t *testing.T) (*customDialer, *CustomHTTPClient, func()) {
	var (
		rotatorSettings = NewRotatorSettings()
		err             error
	)
	rotatorSettings.OutputDirectory, err = os.MkdirTemp("", "warc-tests-")
	if err != nil {
		t.Fatal(err)
	}

	rotatorSettings.Prefix = "TEST-DNS"

	httpClient, err := NewWARCWritingHTTPClient(HTTPClientSettings{
		RotatorSettings: rotatorSettings,
	})
	if err != nil {
		t.Fatalf("Unable to init WARC writing HTTP client: %s", err)
	}
	httpClient.closeDNSCache() // We discard the initial dialer cache immediately

	d := newTestCustomDialer()
	d.client = httpClient

	cleanup := func() {
		err = httpClient.Close()
		if err != nil {
			t.Fatalf("cleanup failed: %v", err)
		}
		d.DNSRecords.Close()
		os.RemoveAll(rotatorSettings.OutputDirectory)

		time.Sleep(1 * time.Second)
	}

	return d, httpClient, cleanup
}

func TestNoDNSServersConfigured(t *testing.T) {
	d, _, cleanup := setup(t)
	defer cleanup()

	wantErr := errors.New("no DNS servers configured")
	d.DNSConfig.Servers = []string{}
	_, _, err := d.archiveDNS(context.Background(), target)
	if err.Error() != wantErr.Error() {
		t.Errorf("Want error %s, got %s", wantErr, err)
	}
}

func TestNormalDNSResolution(t *testing.T) {
	d, _, cleanup := setup(t)
	defer cleanup()

	d.DNSConfig.Servers = []string{publicDNS}
	IP, _, err := d.archiveDNS(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}

	cachedIP, ok := d.DNSRecords.Get(targetHost)
	if !ok {
		t.Error("Cache not working")
	}
	if cachedIP.String() != IP.String() {
		t.Error("Cached IP not matching resolved IP")
	}
}

func TestIPv6Only(t *testing.T) {
	d, _, cleanup := setup(t)
	defer cleanup()

	d.disableIPv4 = true
	d.disableIPv6 = false

	d.DNSConfig.Servers = []string{publicDNS}
	IP, _, err := d.archiveDNS(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Resolved IP: %s", IP)
}

func TestNXDOMAIN(t *testing.T) {
	d, _, cleanup := setup(t)
	defer cleanup()

	IP, _, err := d.archiveDNS(context.Background(), nxdomain)
	if err == nil {
		t.Error("Want failure,", "got resolved IP", IP)
	}
}

func TestDNSFallback(t *testing.T) {
	d, _, cleanup := setup(t)
	defer cleanup()

	d.DNSRecords.Delete(targetHost)
	d.DNSConfig.Servers = []string{invalidDNS, publicDNS}
	IP, _, err := d.archiveDNS(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Resolved IP: %s", IP)
}

func TestDNSCaching(t *testing.T) {
	d, _, cleanup := setup(t)
	defer cleanup()

	d.DNSConfig.Servers = []string{publicDNS}
	ctx := context.Background()
	_, cached, err := d.archiveDNS(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if cached {
		t.Error("Expected uncached result")
	}

	_, cached, err = d.archiveDNS(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if !cached {
		t.Error("Expected cached result")
	}

	_, cached, err = d.archiveDNS(ctx, target1)
	if err != nil {
		t.Fatal(err)
	}
	if cached {
		t.Error("Expected uncached result")
	}

	_, cached, err = d.archiveDNS(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if !cached {
		t.Error("Expected cached result")
	}
}
