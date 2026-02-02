package warc

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/miekg/dns"
)

// dnsResult holds both IPv4 and IPv6 addresses from DNS resolution
type dnsResult struct {
	ipv4 net.IP
	ipv6 net.IP
}

func (d *customDialer) archiveDNS(ctx context.Context, address string) (ipv4, ipv6 net.IP, cached bool, err error) {
	// Get the address without the port if there is one
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, nil, false, err
	}

	// Check if the address is already an IP
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() != nil {
			if d.disableIPv4 {
				return nil, nil, false, fmt.Errorf("IPv4 is disabled but address %s is IPv4", host)
			}
			return ip, nil, false, nil
		}
		if d.disableIPv6 {
			return nil, nil, false, fmt.Errorf("IPv6 is disabled but address %s is IPv6", host)
		}
		return nil, ip, false, nil
	}

	// Check cache first
	if cached, ok := d.DNSRecords.Get(host); ok {
		ipv4, ipv6 := cached.ipv4, cached.ipv6
		// Filter cached results based on disabled settings - this is probably unneeded as in a singular gowarc invocation we are expecting the same settings, but...
		if d.disableIPv4 {
			ipv4 = nil
		}
		if d.disableIPv6 {
			ipv6 = nil
		}
		if ipv4 == nil && ipv6 == nil {
			return nil, nil, true, fmt.Errorf("no suitable IP address found for %s (cached)", host)
		}
		return ipv4, ipv6, true, nil
	}

	if len(d.DNSConfig.Servers) == 0 {
		return nil, nil, false, fmt.Errorf("no DNS servers configured")
	}

	var errA, errAAAA error
	ipv4, ipv6, errA, errAAAA = d.concurrentDNSLookup(ctx, host, len(d.DNSConfig.Servers))
	if errA != nil && errAAAA != nil {
		return nil, nil, false, fmt.Errorf("failed to resolve DNS: A error: %v, AAAA error: %v", errA, errAAAA)
	}

	// Filter based on disabled settings
	if d.disableIPv4 {
		ipv4 = nil
	}
	if d.disableIPv6 {
		ipv6 = nil
	}

	if ipv4 == nil && ipv6 == nil {
		return nil, nil, false, fmt.Errorf("no suitable IP address found for %s", host)
	}

	// Cache both results
	d.DNSRecords.Set(host, dnsResult{ipv4: ipv4, ipv6: ipv6})

	return ipv4, ipv6, false, nil
}

// concurrentDNSLookup tries DNS servers with configurable concurrency
// - dnsConcurrency <= 1: sequential (one server at a time)
// - dnsConcurrency > 1: that many servers concurrently
// - dnsConcurrency == -1: all servers at once (unlimited)
// Implements early cancellation: stops querying once results are found
func (d *customDialer) concurrentDNSLookup(ctx context.Context, address string, maxServers int) (ipv4, ipv6 net.IP, errA, errAAAA error) {
	type result struct {
		ip         net.IP
		err        error
		recordType uint16
	}

	// Determine effective concurrency
	concurrency := d.dnsConcurrency
	if concurrency == -1 {
		concurrency = maxServers // Unlimited = all servers
	} else if concurrency <= 0 {
		concurrency = 1 // Default to sequential
	}

	// Create cancellable context for early termination
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	resultChan := make(chan result, maxServers*2)
	serverChan := make(chan int, maxServers)
	var wg sync.WaitGroup

	// Fill server queue with round-robin starting index
	// Atomically increment and get the starting position
	startIdx := int(d.dnsRoundRobinIndex.Add(1)-1) % maxServers
	for i := range maxServers {
		serverIdx := (startIdx + i) % maxServers
		serverChan <- serverIdx
	}
	close(serverChan)

	// Helper to check if we have all needed results
	haveAllResults := func() bool {
		if !d.disableIPv4 && ipv4 == nil {
			return false
		}
		if !d.disableIPv6 && ipv6 == nil {
			return false
		}
		return true
	}

	// Launch worker goroutines (limited by concurrency)
	for i := 0; i < concurrency && i < maxServers; i++ {
		wg.Go(func() {
			for serverIdx := range serverChan {
				// Check if context was cancelled before starting queries
				select {
				case <-workerCtx.Done():
					return
				default:
				}

				// Query both A and AAAA for this server
				if !d.disableIPv4 {
					ip, err := d.lookupIP(workerCtx, address, dns.TypeA, serverIdx)
					select {
					case resultChan <- result{ip: ip, err: err, recordType: dns.TypeA}:
					case <-workerCtx.Done():
						return
					}
				}
				if !d.disableIPv6 {
					ip, err := d.lookupIP(workerCtx, address, dns.TypeAAAA, serverIdx)
					select {
					case resultChan <- result{ip: ip, err: err, recordType: dns.TypeAAAA}:
					case <-workerCtx.Done():
						return
					}
				}
			}
		})
	}

	// Close result channel when all workers complete
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results with early termination
	var ipv4Errors, ipv6Errors []error
	for res := range resultChan {
		if res.err == nil {
			if res.recordType == dns.TypeA && ipv4 == nil {
				ipv4 = res.ip
			} else if res.recordType == dns.TypeAAAA && ipv6 == nil {
				ipv6 = res.ip
			}

			// Early termination: if we have all results, cancel workers
			if haveAllResults() {
				cancel()
				// Drain remaining results to prevent worker blocking
				go func() {
					for range resultChan {
					}
				}()
				break
			}
		} else {
			if res.recordType == dns.TypeA {
				ipv4Errors = append(ipv4Errors, res.err)
			} else {
				ipv6Errors = append(ipv6Errors, res.err)
			}
		}
	}

	// Set errors only if all queries of that type failed
	if ipv4 == nil && len(ipv4Errors) > 0 {
		errA = ipv4Errors[0]
	}
	if ipv6 == nil && len(ipv6Errors) > 0 {
		errAAAA = ipv6Errors[0]
	}

	return ipv4, ipv6, errA, errAAAA
}

func (d *customDialer) lookupIP(ctx context.Context, address string, recordType uint16, DNSServer int) (net.IP, error) {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(address), recordType)

	r, _, err := d.DNSClient.ExchangeContext(ctx, m, net.JoinHostPort(d.DNSConfig.Servers[DNSServer], d.DNSConfig.Port))
	if err != nil {
		return nil, err
	}

	// Record the DNS response
	recordTypeStr := "TYPE=A"
	if recordType == dns.TypeAAAA {
		recordTypeStr = "TYPE=AAAA"
	}

	d.client.WriteRecord(fmt.Sprintf("dns:%s?%s", address, recordTypeStr), "resource", "text/dns", r.String(), nil)

	for _, answer := range r.Answer {
		switch recordType {
		case dns.TypeA:
			if a, ok := answer.(*dns.A); ok {
				return a.A, nil
			}
		case dns.TypeAAAA:
			if aaaa, ok := answer.(*dns.AAAA); ok {
				return aaaa.AAAA, nil
			}
		}
	}

	return nil, fmt.Errorf("no %s record found", recordTypeStr)
}
