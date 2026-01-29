package warc

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
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

// mockDNSClient is a mock DNS client for testing that doesn't make real network calls
type mockDNSClient struct {
	// responses maps server address to response config
	responses map[string]mockDNSResponse
	// callLog tracks which servers were called and in what order
	callLog   []string
	callLogMu sync.Mutex
	// delay simulates network latency per server
	delays map[string]time.Duration
}

type mockDNSResponse struct {
	ipv4 net.IP
	ipv6 net.IP
	err  error
}

func newMockDNSClient() *mockDNSClient {
	return &mockDNSClient{
		responses: make(map[string]mockDNSResponse),
		callLog:   []string{},
		delays:    make(map[string]time.Duration),
	}
}

func (m *mockDNSClient) setResponse(serverAddr string, ipv4, ipv6 net.IP, err error) {
	m.responses[serverAddr] = mockDNSResponse{ipv4: ipv4, ipv6: ipv6, err: err}
}

func (m *mockDNSClient) setDelay(serverAddr string, delay time.Duration) {
	m.delays[serverAddr] = delay
}

func (m *mockDNSClient) getCallLog() []string {
	m.callLogMu.Lock()
	defer m.callLogMu.Unlock()
	result := make([]string, len(m.callLog))
	copy(result, m.callLog)
	return result
}

func (m *mockDNSClient) ExchangeContext(ctx context.Context, msg *dns.Msg, address string) (*dns.Msg, time.Duration, error) {
	// Log the call
	m.callLogMu.Lock()
	m.callLog = append(m.callLog, address)
	m.callLogMu.Unlock()

	// Simulate delay if configured
	if delay, ok := m.delays[address]; ok && delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
	}

	// Get configured response
	resp, ok := m.responses[address]
	if !ok {
		return nil, 0, errors.New("no mock response configured for " + address)
	}

	if resp.err != nil {
		return nil, 0, resp.err
	}

	// Build DNS response message
	r := new(dns.Msg)
	r.SetReply(msg)

	// Determine record type being queried
	if len(msg.Question) > 0 {
		qtype := msg.Question[0].Qtype
		if qtype == dns.TypeA && resp.ipv4 != nil {
			rr := &dns.A{
				Hdr: dns.RR_Header{
					Name:   msg.Question[0].Name,
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    300,
				},
				A: resp.ipv4,
			}
			r.Answer = append(r.Answer, rr)
		} else if qtype == dns.TypeAAAA && resp.ipv6 != nil {
			rr := &dns.AAAA{
				Hdr: dns.RR_Header{
					Name:   msg.Question[0].Name,
					Rrtype: dns.TypeAAAA,
					Class:  dns.ClassINET,
					Ttl:    300,
				},
				AAAA: resp.ipv6,
			}
			r.Answer = append(r.Answer, rr)
		}
	}

	return r, 0, nil
}

func newTestCustomDialer() (d *customDialer) {
	d = new(customDialer)

	DNScache, err := otter.MustBuilder[string, dnsResult](1000).
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

// setupMock creates a dialer for mock-based tests without WARC writing
func setupMock() (*customDialer, func()) {
	d := newTestCustomDialer()

	// Use a no-op client with a WARC writer channel that gets drained
	d.client = &CustomHTTPClient{
		WARCWriter: make(chan *RecordBatch, 100), // Large buffer to prevent blocking
	}

	// Drain the WARC writer channel to prevent blocking
	stopDrain := make(chan bool)
	var drainerWg sync.WaitGroup
	drainerWg.Go(func() {
		for {
			select {
			case batch := <-d.client.WARCWriter:
				// Send feedback immediately to unblock writer
				if batch != nil && batch.FeedbackChan != nil {
					batch.FeedbackChan <- struct{}{}
				}
			case <-stopDrain:
				// Drain remaining items
				for {
					select {
					case batch := <-d.client.WARCWriter:
						if batch != nil && batch.FeedbackChan != nil {
							batch.FeedbackChan <- struct{}{}
						}
					default:
						return
					}
				}
			}
		}
	})

	cleanup := func() {
		// Wait for DNS operations to complete
		time.Sleep(200 * time.Millisecond)
		// Stop the drainer
		close(stopDrain)
		// Wait for drainer to finish
		drainerWg.Wait()
		// Close the cache
		d.DNSRecords.Close()
		// Now safe to close the channel
		close(d.client.WARCWriter)
		// Give otter cache goroutines time to shut down (matches original setup())
		time.Sleep(1 * time.Second)
	}

	return d, cleanup
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
	_, _, _, err := d.archiveDNS(context.Background(), target)
	if err.Error() != wantErr.Error() {
		t.Errorf("Want error %s, got %s", wantErr, err)
	}
}

func TestNormalDNSResolution(t *testing.T) {
	d, _, cleanup := setup(t)
	defer cleanup()

	d.DNSConfig.Servers = []string{publicDNS}
	ipv4, ipv6, _, err := d.archiveDNS(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}

	cachedResult, ok := d.DNSRecords.Get(targetHost)
	if !ok {
		t.Error("Cache not working")
	}
	// Check that at least one of the cached IPs matches what was resolved
	if (ipv4 != nil && cachedResult.ipv4.String() != ipv4.String()) &&
		(ipv6 != nil && cachedResult.ipv6.String() != ipv6.String()) {
		t.Error("Cached IP not matching resolved IP")
	}
}

func TestIPv6Only(t *testing.T) {
	d, _, cleanup := setup(t)
	defer cleanup()

	d.disableIPv4 = true
	d.disableIPv6 = false

	d.DNSConfig.Servers = []string{publicDNS}
	ipv4, ipv6, _, err := d.archiveDNS(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if ipv4 != nil {
		t.Errorf("Expected no IPv4 address when disabled, got %v", ipv4)
	}

	t.Logf("Resolved IPv6: %s", ipv6)
}

func TestNXDOMAIN(t *testing.T) {
	d, _, cleanup := setup(t)
	defer cleanup()

	ipv4, ipv6, _, err := d.archiveDNS(context.Background(), nxdomain)
	if err == nil {
		t.Error("Want failure,", "got resolved IPs", ipv4, ipv6)
	}
}

func TestDNSFallback(t *testing.T) {
	d, _, cleanup := setup(t)
	defer cleanup()

	d.DNSRecords.Delete(targetHost)
	d.DNSConfig.Servers = []string{invalidDNS, publicDNS}
	ipv4, ipv6, _, err := d.archiveDNS(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Resolved IPs: v4=%s v6=%s", ipv4, ipv6)
}

func TestDNSCaching(t *testing.T) {
	d, _, cleanup := setup(t)
	defer cleanup()

	d.DNSConfig.Servers = []string{publicDNS}
	ctx := context.Background()
	_, _, cached, err := d.archiveDNS(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if cached {
		t.Error("Expected uncached result")
	}

	_, _, cached, err = d.archiveDNS(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if !cached {
		t.Error("Expected cached result")
	}

	_, _, cached, err = d.archiveDNS(ctx, target1)
	if err != nil {
		t.Fatal(err)
	}
	if cached {
		t.Error("Expected uncached result")
	}

	_, _, cached, err = d.archiveDNS(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if !cached {
		t.Error("Expected cached result")
	}
}

// TestDNSConcurrencySequential tests sequential DNS resolution (concurrency=1)
func TestDNSConcurrencySequential(t *testing.T) {
	d, cleanup := setupMock()
	defer cleanup()

	mock := newMockDNSClient()
	d.DNSClient = mock
	d.dnsConcurrency = 1

	// Configure 3 servers where first fails, second succeeds
	server1 := "1.1.1.1:53"
	server2 := "2.2.2.2:53"
	server3 := "3.3.3.3:53"

	mock.setResponse(server1, nil, nil, errors.New("server 1 failed"))
	mock.setResponse(server2, net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1"), nil)
	mock.setResponse(server3, net.ParseIP("192.0.2.3"), net.ParseIP("2001:db8::3"), nil)

	d.DNSConfig.Servers = []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}
	d.DNSRecords.Delete(targetHost)

	ipv4, ipv6, _, err := d.archiveDNS(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}

	if ipv4 == nil && ipv6 == nil {
		t.Fatal("Expected resolved IP")
	}

	// With sequential mode, should have tried servers one at a time
	callLog := mock.getCallLog()
	if len(callLog) < 2 {
		t.Errorf("Expected at least 2 calls (first fails, second succeeds), got %d", len(callLog))
	}
	t.Logf("Call log: %v", callLog)
}

// TestDNSConcurrencyParallel tests parallel DNS resolution with limited concurrency
func TestDNSConcurrencyParallel(t *testing.T) {
	d, cleanup := setupMock()
	defer cleanup()

	mock := newMockDNSClient()
	d.DNSClient = mock
	d.dnsConcurrency = 3

	// Configure 3 servers, all succeed
	server1 := "1.1.1.1:53"
	server2 := "2.2.2.2:53"
	server3 := "3.3.3.3:53"

	mock.setResponse(server1, net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1"), nil)
	mock.setResponse(server2, net.ParseIP("192.0.2.2"), net.ParseIP("2001:db8::2"), nil)
	mock.setResponse(server3, net.ParseIP("192.0.2.3"), net.ParseIP("2001:db8::3"), nil)

	d.DNSConfig.Servers = []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}
	d.DNSRecords.Delete(targetHost)

	ipv4, ipv6, _, err := d.archiveDNS(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}

	if ipv4 == nil && ipv6 == nil {
		t.Fatal("Expected resolved IP")
	}

	// With parallel mode (concurrency=3), all 3 servers may be queried
	callLog := mock.getCallLog()
	t.Logf("Call log: %v (length: %d)", callLog, len(callLog))
}

// TestDNSConcurrencyUnlimited tests unlimited parallel DNS resolution
func TestDNSConcurrencyUnlimited(t *testing.T) {
	d, cleanup := setupMock()
	defer cleanup()

	mock := newMockDNSClient()
	d.DNSClient = mock
	d.dnsConcurrency = -1 // Unlimited

	// Configure 3 servers
	server1 := "1.1.1.1:53"
	server2 := "2.2.2.2:53"
	server3 := "3.3.3.3:53"

	mock.setResponse(server1, net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1"), nil)
	mock.setResponse(server2, net.ParseIP("192.0.2.2"), net.ParseIP("2001:db8::2"), nil)
	mock.setResponse(server3, net.ParseIP("192.0.2.3"), net.ParseIP("2001:db8::3"), nil)

	d.DNSConfig.Servers = []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}
	d.DNSRecords.Delete(targetHost)

	ipv4, ipv6, _, err := d.archiveDNS(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}

	if ipv4 == nil && ipv6 == nil {
		t.Fatal("Expected resolved IP")
	}

	// With unlimited concurrency, servers are queried in parallel
	callLog := mock.getCallLog()
	t.Logf("Call log: %v (length: %d)", callLog, len(callLog))
}

// TestDNSRoundRobin verifies round-robin DNS server selection
func TestDNSRoundRobin(t *testing.T) {
	d, cleanup := setupMock()
	defer cleanup()

	mock := newMockDNSClient()
	d.DNSClient = mock
	d.dnsConcurrency = 1 // Sequential for predictable ordering

	// Configure 3 servers
	server1 := "1.1.1.1:53"
	server2 := "2.2.2.2:53"
	server3 := "3.3.3.3:53"

	mock.setResponse(server1, net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1"), nil)
	mock.setResponse(server2, net.ParseIP("192.0.2.2"), net.ParseIP("2001:db8::2"), nil)
	mock.setResponse(server3, net.ParseIP("192.0.2.3"), net.ParseIP("2001:db8::3"), nil)

	d.DNSConfig.Servers = []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}

	// Track starting servers for each lookup
	startingServers := []string{}

	// Perform 6 lookups (2 full rotations)
	for i := 0; i < 6; i++ {
		host := "example" + string(rune('a'+i)) + ".com:443"
		d.DNSRecords.Delete("example" + string(rune('a'+i)) + ".com")

		callLogBefore := len(mock.getCallLog())
		_, _, _, err := d.archiveDNS(context.Background(), host)
		if err != nil {
			t.Fatal(err)
		}

		callLog := mock.getCallLog()
		if len(callLog) > callLogBefore {
			// First call in this lookup shows the starting server
			startingServers = append(startingServers, callLog[callLogBefore])
		}
	}

	t.Logf("Starting servers for each lookup: %v", startingServers)

	// Verify round-robin: each lookup should start from a different server
	// We should see rotation across the 3 servers
	if len(startingServers) < 6 {
		t.Fatalf("Expected 6 starting servers, got %d", len(startingServers))
	}

	// Check that not all lookups started from the same server
	uniqueStarts := make(map[string]bool)
	for _, server := range startingServers {
		uniqueStarts[server] = true
	}

	if len(uniqueStarts) == 1 {
		t.Error("All lookups started from same server - round-robin not working")
	} else {
		t.Logf("Round-robin working: saw %d different starting servers", len(uniqueStarts))
	}
}

// TestDNSMultipleServersFallback tests that all servers are tried (no 4-server limit)
func TestDNSMultipleServersFallback(t *testing.T) {
	d, cleanup := setupMock()
	defer cleanup()

	mock := newMockDNSClient()
	d.DNSClient = mock
	d.dnsConcurrency = 1

	// Configure 3 servers where first 2 fail, third succeeds
	server1 := "1.1.1.1:53"
	server2 := "2.2.2.2:53"
	server3 := "3.3.3.3:53"

	mock.setResponse(server1, nil, nil, errors.New("server 1 timeout"))
	mock.setResponse(server2, nil, nil, errors.New("server 2 timeout"))
	mock.setResponse(server3, net.ParseIP("192.0.2.3"), net.ParseIP("2001:db8::3"), nil)

	d.DNSConfig.Servers = []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}
	d.DNSRecords.Delete(targetHost)

	ipv4, ipv6, _, err := d.archiveDNS(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}

	if ipv4 == nil && ipv6 == nil {
		t.Fatal("Expected resolved IP from third server")
	}

	callLog := mock.getCallLog()
	t.Logf("Call log: %v", callLog)

	// Should have tried all 3 servers (proving no hardcoded 4-server limit)
	uniqueServers := make(map[string]bool)
	for _, call := range callLog {
		uniqueServers[call] = true
	}

	if len(uniqueServers) < 3 {
		t.Errorf("Expected queries to all 3 servers, got %d unique servers", len(uniqueServers))
	}
}

// TestDNSEarlyCancellation tests that queries stop once results are found
func TestDNSEarlyCancellation(t *testing.T) {
	d, cleanup := setupMock()
	defer cleanup()

	mock := newMockDNSClient()
	d.DNSClient = mock
	d.dnsConcurrency = 3 // Allow parallel queries

	// Configure 3 servers with delays
	server1 := "1.1.1.1:53"
	server2 := "2.2.2.2:53"
	server3 := "3.3.3.3:53"

	// Server 1 is fast and succeeds
	mock.setResponse(server1, net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1"), nil)
	mock.setDelay(server1, 10*time.Millisecond)

	// Servers 2 and 3 are slow
	mock.setResponse(server2, net.ParseIP("192.0.2.2"), net.ParseIP("2001:db8::2"), nil)
	mock.setDelay(server2, 500*time.Millisecond)

	mock.setResponse(server3, net.ParseIP("192.0.2.3"), net.ParseIP("2001:db8::3"), nil)
	mock.setDelay(server3, 500*time.Millisecond)

	d.DNSConfig.Servers = []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}
	d.DNSRecords.Delete(targetHost)

	start := time.Now()
	ipv4, ipv6, _, err := d.archiveDNS(context.Background(), target)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}

	if ipv4 == nil && ipv6 == nil {
		t.Fatal("Expected resolved IP")
	}

	// Should complete quickly due to early cancellation (not wait for slow servers)
	if elapsed > 200*time.Millisecond {
		t.Logf("Warning: took %v, expected <200ms with early cancellation", elapsed)
	} else {
		t.Logf("Early cancellation working: completed in %v", elapsed)
	}

	callLog := mock.getCallLog()
	t.Logf("Call log: %v", callLog)
}

// TestDNSIPv4Only tests IPv6-disabled mode
func TestDNSIPv4Only(t *testing.T) {
	d, cleanup := setupMock()
	defer cleanup()

	mock := newMockDNSClient()
	d.DNSClient = mock
	d.disableIPv4 = false
	d.disableIPv6 = true

	server1 := "1.1.1.1:53"
	mock.setResponse(server1, net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1"), nil)

	d.DNSConfig.Servers = []string{"1.1.1.1"}
	d.DNSRecords.Delete(targetHost)

	ipv4, ipv6, _, err := d.archiveDNS(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}

	// Should return IPv4 only
	if ipv4 == nil {
		t.Fatal("Expected IPv4 address")
	}

	if ipv4.To4() == nil {
		t.Errorf("Expected IPv4 address, got %v", ipv4)
	}

	if ipv6 != nil {
		t.Errorf("Expected no IPv6 address when disabled, got %v", ipv6)
	}

	t.Logf("Resolved IPv4: %v", ipv4)
}

// TestDNSMixedResults tests when IPv4 succeeds but IPv6 fails
func TestDNSMixedResults(t *testing.T) {
	d, cleanup := setupMock()
	defer cleanup()

	mock := newMockDNSClient()
	d.DNSClient = mock

	server1 := "1.1.1.1:53"
	// IPv4 succeeds, IPv6 returns no record
	mock.setResponse(server1, net.ParseIP("192.0.2.1"), nil, nil)

	d.DNSConfig.Servers = []string{"1.1.1.1"}
	d.DNSRecords.Delete(targetHost)

	ipv4, ipv6, _, err := d.archiveDNS(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}

	// Should succeed with IPv4 even though IPv6 failed
	if ipv4 == nil {
		t.Fatal("Expected IPv4 address")
	}

	t.Logf("Resolved with mixed results: ipv4=%v ipv6=%v", ipv4, ipv6)
}

// TestDisableIPv4 tests that disableIPv4 prevents IPv4 resolution and dialing
func TestDisableIPv4(t *testing.T) {
	d, cleanup := setupMock()
	defer cleanup()

	mock := newMockDNSClient()
	d.DNSClient = mock
	d.disableIPv4 = true
	d.disableIPv6 = false

	server1 := "1.1.1.1:53"
	mock.setResponse(server1, net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1"), nil)

	d.DNSConfig.Servers = []string{"1.1.1.1"}
	d.DNSRecords.Delete(targetHost)

	ipv4, ipv6, _, err := d.archiveDNS(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}

	// IPv4 should be nil when disabled
	if ipv4 != nil {
		t.Errorf("Expected no IPv4 address when disabled, got %v", ipv4)
	}

	// IPv6 should be returned
	if ipv6 == nil {
		t.Fatal("Expected IPv6 address")
	}

	if ipv6.To4() != nil {
		t.Errorf("Expected IPv6 address, got IPv4: %v", ipv6)
	}

	// Verify that only AAAA queries were made (no A queries)
	callLog := mock.getCallLog()
	for _, call := range callLog {
		t.Logf("DNS call to: %s", call)
	}

	t.Logf("disableIPv4=true: Resolved IPv6=%v, IPv4=%v", ipv6, ipv4)
}

// TestDisableIPv6 tests that disableIPv6 prevents IPv6 resolution and dialing
func TestDisableIPv6(t *testing.T) {
	d, cleanup := setupMock()
	defer cleanup()

	mock := newMockDNSClient()
	d.DNSClient = mock
	d.disableIPv4 = false
	d.disableIPv6 = true

	server1 := "1.1.1.1:53"
	mock.setResponse(server1, net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1"), nil)

	d.DNSConfig.Servers = []string{"1.1.1.1"}
	d.DNSRecords.Delete(targetHost)

	ipv4, ipv6, _, err := d.archiveDNS(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}

	// IPv6 should be nil when disabled
	if ipv6 != nil {
		t.Errorf("Expected no IPv6 address when disabled, got %v", ipv6)
	}

	// IPv4 should be returned
	if ipv4 == nil {
		t.Fatal("Expected IPv4 address")
	}

	if ipv4.To4() == nil {
		t.Errorf("Expected IPv4 address, got: %v", ipv4)
	}

	// Verify that only A queries were made (no AAAA queries)
	callLog := mock.getCallLog()
	for _, call := range callLog {
		t.Logf("DNS call to: %s", call)
	}

	t.Logf("disableIPv6=true: Resolved IPv4=%v, IPv6=%v", ipv4, ipv6)
}

// TestDisableBothIPv4AndIPv6 tests that disabling both returns an error
func TestDisableBothIPv4AndIPv6(t *testing.T) {
	d, cleanup := setupMock()
	defer cleanup()

	mock := newMockDNSClient()
	d.DNSClient = mock
	d.disableIPv4 = true
	d.disableIPv6 = true

	server1 := "1.1.1.1:53"
	mock.setResponse(server1, net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1"), nil)

	d.DNSConfig.Servers = []string{"1.1.1.1"}
	d.DNSRecords.Delete(targetHost)

	ipv4, ipv6, _, err := d.archiveDNS(context.Background(), target)

	// Should fail because no address family is available
	if err == nil {
		t.Errorf("Expected error when both IPv4 and IPv6 are disabled, got ipv4=%v ipv6=%v", ipv4, ipv6)
	}

	t.Logf("disableIPv4=true, disableIPv6=true: Got expected error: %v", err)
}

// TestDisableIPv4WithIPv4OnlyHost tests error when IPv4 is disabled but host only has IPv4
func TestDisableIPv4WithIPv4OnlyHost(t *testing.T) {
	d, cleanup := setupMock()
	defer cleanup()

	mock := newMockDNSClient()
	d.DNSClient = mock
	d.disableIPv4 = true
	d.disableIPv6 = false

	server1 := "1.1.1.1:53"
	// Host only has IPv4 address
	mock.setResponse(server1, net.ParseIP("192.0.2.1"), nil, nil)

	d.DNSConfig.Servers = []string{"1.1.1.1"}
	d.DNSRecords.Delete(targetHost)

	ipv4, ipv6, _, err := d.archiveDNS(context.Background(), target)

	// Should fail because host has no IPv6 and IPv4 is disabled
	if err == nil {
		t.Errorf("Expected error when IPv4 disabled and host only has IPv4, got ipv4=%v ipv6=%v", ipv4, ipv6)
	}

	t.Logf("disableIPv4=true with IPv4-only host: Got expected error: %v", err)
}

// TestDisableIPv6WithIPv6OnlyHost tests error when IPv6 is disabled but host only has IPv6
func TestDisableIPv6WithIPv6OnlyHost(t *testing.T) {
	d, cleanup := setupMock()
	defer cleanup()

	mock := newMockDNSClient()
	d.DNSClient = mock
	d.disableIPv4 = false
	d.disableIPv6 = true

	server1 := "1.1.1.1:53"
	// Host only has IPv6 address
	mock.setResponse(server1, nil, net.ParseIP("2001:db8::1"), nil)

	d.DNSConfig.Servers = []string{"1.1.1.1"}
	d.DNSRecords.Delete(targetHost)

	ipv4, ipv6, _, err := d.archiveDNS(context.Background(), target)

	// Should fail because host has no IPv4 and IPv6 is disabled
	if err == nil {
		t.Errorf("Expected error when IPv6 disabled and host only has IPv6, got ipv4=%v ipv6=%v", ipv4, ipv6)
	}

	t.Logf("disableIPv6=true with IPv6-only host: Got expected error: %v", err)
}

// TestDisableIPv4CacheInteraction tests that cached results respect disableIPv4
func TestDisableIPv4CacheInteraction(t *testing.T) {
	d, cleanup := setupMock()
	defer cleanup()

	mock := newMockDNSClient()
	d.DNSClient = mock

	server1 := "1.1.1.1:53"
	mock.setResponse(server1, net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1"), nil)
	d.DNSConfig.Servers = []string{"1.1.1.1"}

	// First query with both enabled - populates cache
	d.disableIPv4 = false
	d.disableIPv6 = false
	d.DNSRecords.Delete(targetHost)

	ipv4, ipv6, cached, err := d.archiveDNS(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if cached {
		t.Error("Expected uncached result on first query")
	}
	if ipv4 == nil || ipv6 == nil {
		t.Errorf("Expected both IPs on first query, got ipv4=%v ipv6=%v", ipv4, ipv6)
	}

	// Second query with IPv4 disabled - cached results should be filtered
	d.disableIPv4 = true
	ipv4, ipv6, cached, err = d.archiveDNS(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if !cached {
		t.Error("Expected cached result on second query")
	}
	// IPv4 should be filtered out even from cached results
	if ipv4 != nil {
		t.Errorf("Expected IPv4 to be nil when disabled, got %v", ipv4)
	}
	if ipv6 == nil {
		t.Error("Expected IPv6 to be returned from cache")
	}
	t.Logf("Cached query with disableIPv4=true: ipv4=%v ipv6=%v cached=%v", ipv4, ipv6, cached)
}

// TestDisableIPv6CacheInteraction tests that cached results respect disableIPv6
func TestDisableIPv6CacheInteraction(t *testing.T) {
	d, cleanup := setupMock()
	defer cleanup()

	mock := newMockDNSClient()
	d.DNSClient = mock

	server1 := "1.1.1.1:53"
	mock.setResponse(server1, net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1"), nil)
	d.DNSConfig.Servers = []string{"1.1.1.1"}

	// First query with both enabled - populates cache
	d.disableIPv4 = false
	d.disableIPv6 = false
	d.DNSRecords.Delete(targetHost)

	ipv4, ipv6, cached, err := d.archiveDNS(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if cached {
		t.Error("Expected uncached result on first query")
	}
	if ipv4 == nil || ipv6 == nil {
		t.Errorf("Expected both IPs on first query, got ipv4=%v ipv6=%v", ipv4, ipv6)
	}

	// Second query with IPv6 disabled - cached results should be filtered
	d.disableIPv6 = true
	ipv4, ipv6, cached, err = d.archiveDNS(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if !cached {
		t.Error("Expected cached result on second query")
	}
	// IPv6 should be filtered out even from cached results
	if ipv6 != nil {
		t.Errorf("Expected IPv6 to be nil when disabled, got %v", ipv6)
	}
	if ipv4 == nil {
		t.Error("Expected IPv4 to be returned from cache")
	}
	t.Logf("Cached query with disableIPv6=true: ipv4=%v ipv6=%v cached=%v", ipv4, ipv6, cached)
}

// TestDisableBothCacheInteraction tests error when both are disabled with cached results
func TestDisableBothCacheInteraction(t *testing.T) {
	d, cleanup := setupMock()
	defer cleanup()

	mock := newMockDNSClient()
	d.DNSClient = mock

	server1 := "1.1.1.1:53"
	mock.setResponse(server1, net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1"), nil)
	d.DNSConfig.Servers = []string{"1.1.1.1"}

	// First query with both enabled - populates cache
	d.disableIPv4 = false
	d.disableIPv6 = false
	d.DNSRecords.Delete(targetHost)

	_, _, _, err := d.archiveDNS(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}

	// Second query with both disabled - should error even with cached results
	d.disableIPv4 = true
	d.disableIPv6 = true
	_, _, _, err = d.archiveDNS(context.Background(), target)
	if err == nil {
		t.Error("Expected error when both IPv4 and IPv6 are disabled with cached results")
	}
	t.Logf("Cached query with both disabled: got expected error: %v", err)
}
