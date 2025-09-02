package warc

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/miekg/dns"
)

const maxFallbackDNSServers = 3

func (d *customDialer) archiveDNS(ctx context.Context, address string) (resolvedIP net.IP, cached bool, err error) {
	// Get the address without the port if there is one
	address, _, err = net.SplitHostPort(address)
	if err != nil {
		return resolvedIP, false, err
	}

	// Check if the address is already an IP
	resolvedIP = net.ParseIP(address)
	if resolvedIP != nil {
		return resolvedIP, false, nil
	}

	// Check cache first
	if cachedIP, ok := d.DNSRecords.Get(address); ok {
		return cachedIP, true, nil
	}

	var wg sync.WaitGroup
	var ipv4, ipv6 net.IP
	var errA, errAAAA error

	if len(d.DNSConfig.Servers) == 0 {
		return nil, false, fmt.Errorf("no DNS servers configured")
	}

	fallbackServers := min(maxFallbackDNSServers, len(d.DNSConfig.Servers)-1)

	for DNSServer := 0; DNSServer <= fallbackServers; DNSServer++ {
		if !d.disableIPv4 {
			wg.Go(func() {
				ipv4, errA = d.lookupIP(ctx, address, dns.TypeA, DNSServer)
			})
		}

		if !d.disableIPv6 {
			wg.Go(func() {
				ipv6, errAAAA = d.lookupIP(ctx, address, dns.TypeAAAA, DNSServer)
			})
		}

		wg.Wait()

		if errA == nil || errAAAA == nil {
			break
		}
	}
	if errA != nil && errAAAA != nil {
		return nil, false, fmt.Errorf("failed to resolve DNS: A error: %v, AAAA error: %v", errA, errAAAA)
	}

	// Prioritize IPv6 if both are available and enabled
	if ipv6 != nil && !d.disableIPv6 {
		resolvedIP = ipv6
	} else if ipv4 != nil && !d.disableIPv4 {
		resolvedIP = ipv4
	}

	if resolvedIP != nil {
		// Cache the result
		d.DNSRecords.Set(address, resolvedIP)
		return resolvedIP, false, nil
	}

	return nil, false, fmt.Errorf("no suitable IP address found for %s", address)
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
