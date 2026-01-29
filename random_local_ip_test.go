package warc

import (
	"net"
	"strings"
	"testing"
)

// TestGenerateRandomIPv6 tests the generation of a random IPv6 address within a given subnet.
func TestGenerateRandomIPv6(t *testing.T) {
	baseIPNet := net.IPNet{
		IP:   net.ParseIP("2001:db8::"),
		Mask: net.CIDRMask(64, 128),
	}
	ip, err := generateRandomIPv6(baseIPNet)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if !baseIPNet.Contains(ip) {
		t.Errorf("Generated IP %v is not within base IP network %v", ip, baseIPNet)
	}
}

// TestGenerateRandomIPv6InvalidBaseIP tests the function with an invalid base IP.
func TestGenerateRandomIPv6InvalidBaseIP(t *testing.T) {
	baseIPNet := net.IPNet{
		IP:   net.ParseIP("invalidIP"),
		Mask: net.CIDRMask(64, 128),
	}
	_, err := generateRandomIPv6(baseIPNet)
	if err == nil {
		t.Error("Expected error for invalid base IP, got nil")
	}
}

// TestGenerateRandomIPv6InvalidMask tests the function with an invalid mask length.
func TestGenerateRandomIPv6InvalidMask(t *testing.T) {
	baseIPNet := net.IPNet{
		IP:   net.ParseIP("2001:db8::"),
		Mask: net.CIDRMask(129, 128),
	}
	_, err := generateRandomIPv6(baseIPNet)
	if err == nil {
		t.Error("Expected error for invalid mask length, got nil")
	}

	baseIPNet = net.IPNet{
		IP: net.ParseIP("2001:db8::"),
		Mask: net.IPMask([]byte{
			0xff, 0xfe, 0xff, 0xff, // Non-contiguous mask (invalid) (2nd byte)
			0xff, 0xff, 0xff, 0xff,
			0xff, 0xff, 0xff, 0xff,
			0xff, 0xff, 0xff, 0xff,
		}),
	}
	_, err = generateRandomIPv6(baseIPNet)
	if err == nil {
		t.Error("Expected error for invalid mask length, got nil")
	}
}

func TestGenerateRandomIPv6EmptyMask(t *testing.T) {
	baseIPNet := net.IPNet{
		IP:   net.ParseIP("2001:db8::"),
		Mask: net.IPMask([]byte{}), // Empty mask
	}
	_, err := generateRandomIPv6(baseIPNet)
	if err == nil {
		t.Error("Expected error for empty mask, got nil")
	}
}

// TestGenerateRandomIPv6FullMask tests the function with a /128 mask (no host bits).
func TestGenerateRandomIPv6FullMask(t *testing.T) {
	baseIPNet := net.IPNet{
		IP:   net.ParseIP("2001:db8::1"),
		Mask: net.CIDRMask(128, 128),
	}
	ip, err := generateRandomIPv6(baseIPNet)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if !ip.Equal(baseIPNet.IP) {
		t.Errorf("Expected IP %v, got %v", baseIPNet.IP, ip)
	}
}

// TestGenerateRandomIPv6ZeroMask tests the function with a /0 mask (all host bits).
func TestGenerateRandomIPv6ZeroMask(t *testing.T) {
	baseIPNet := net.IPNet{
		IP:   net.ParseIP("::"),
		Mask: net.CIDRMask(0, 128),
	}
	ip, err := generateRandomIPv6(baseIPNet)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if !baseIPNet.Contains(ip) {
		t.Errorf("Generated IP %v is not within base IP network %v", ip, baseIPNet)
	}
}

// TestGetNextIP tests the round-robin IP selection.
func TestGetNextIP(t *testing.T) {
	ip1 := net.ParseIP("192.168.1.1")
	ip2 := net.ParseIP("192.168.1.2")
	ipNet1 := net.IPNet{IP: ip1, Mask: net.CIDRMask(24, 32)}
	ipNet2 := net.IPNet{IP: ip2, Mask: net.CIDRMask(24, 32)}
	ipList := []net.IPNet{ipNet1, ipNet2}
	availableIPs := &availableIPs{}
	availableIPs.IPs.Store(&ipList)

	ip := GetNextIP(availableIPs)
	if !ip.Equal(ip1) {
		t.Errorf("Expected %v, got %v", ip1, ip)
	}
	ip = GetNextIP(availableIPs)
	if !ip.Equal(ip2) {
		t.Errorf("Expected %v, got %v", ip2, ip)
	}
	ip = GetNextIP(availableIPs)
	if !ip.Equal(ip1) {
		t.Errorf("Expected %v, got %v", ip1, ip)
	}
}

// TestGetNextIPEmptyIPs tests the function when no IPs are available.
func TestGetNextIPEmptyIPs(t *testing.T) {
	availableIPs := &availableIPs{}
	availableIPs.IPs.Store(&[]net.IPNet{})
	ip := GetNextIP(availableIPs)
	if ip != nil {
		t.Errorf("Expected nil, got %v", ip)
	}
}

// TestGetNextIPAnyIP tests the function with AnyIP set to true for IPv6.
func TestGetNextIPAnyIP(t *testing.T) {
	baseIP := net.ParseIP("2001:db8::")
	baseIPNet := net.IPNet{IP: baseIP, Mask: net.CIDRMask(64, 128)}
	ipList := []net.IPNet{baseIPNet}
	availableIPs := &availableIPs{AnyIP: true}
	availableIPs.IPs.Store(&ipList)

	ip := GetNextIP(availableIPs)
	if ip == nil {
		t.Error("Expected non-nil IP, got nil")
	}
	if !baseIPNet.Contains(ip) {
		t.Errorf("Generated IP %v is not within base IP network %v", ip, baseIPNet)
	}
}

// TestGetNextIPAnyIPv6MultipleIPv6 tests the function with multiple IPv6 addresses and AnyIP set to true.
func TestGetNextIPAnyIPMultipleIPv6(t *testing.T) {
	baseIP1 := net.ParseIP("2001:db8::")
	baseIPNet1 := net.IPNet{IP: baseIP1, Mask: net.CIDRMask(64, 128)}
	baseIP2 := net.ParseIP("2001:db9::")
	baseIPNet2 := net.IPNet{IP: baseIP2, Mask: net.CIDRMask(64, 128)}
	ipList := []net.IPNet{baseIPNet1, baseIPNet2}
	availableIPs := &availableIPs{AnyIP: true}
	availableIPs.IPs.Store(&ipList)

	for i := 0; i < 5; i++ {
		ip := GetNextIP(availableIPs)
		if ip == nil {
			t.Error("Expected non-nil IP, got nil")
		}
		if !baseIPNet1.Contains(ip) && !baseIPNet2.Contains(ip) {
			t.Errorf("Generated IP %v is not within base IP network %v or %v", ip, baseIPNet1, baseIPNet2)
		}
	}
}

// TestTestGetNextIPAnyIPv6Randomness tests the randomness of generated IPv6 addresses.
func TestTestGetNextIPAnyIPv6Randomness(t *testing.T) {
	baseIP := net.ParseIP("2001:db8::")
	baseIPNet := net.IPNet{IP: baseIP, Mask: net.CIDRMask(64, 128)}
	ipList := []net.IPNet{baseIPNet}
	availableIPs := &availableIPs{AnyIP: true}
	availableIPs.IPs.Store(&ipList)

	ip1 := GetNextIP(availableIPs)
	ip2 := GetNextIP(availableIPs)
	if ip1.Equal(ip2) {
		t.Errorf("Expected different IPs, got %v", ip1)
	}
}

// TestGetNextIPHighIndex tests the function with a high index value.
func TestGetNextIPHighIndex(t *testing.T) {
	ip1 := net.ParseIP("192.168.1.1")
	ipNet1 := net.IPNet{IP: ip1, Mask: net.CIDRMask(24, 32)}
	ipList := []net.IPNet{ipNet1}
	availableIPs := &availableIPs{}
	availableIPs.IPs.Store(&ipList)
	availableIPs.Index.Store(9999999)

	ip := GetNextIP(availableIPs)
	if !ip.Equal(ip1) {
		t.Errorf("Expected %v, got %v", ip1, ip)
	}
}

// TestGetLocalAddrIPv4TCP tests local address selection for IPv4 TCP connections.
func TestGetLocalAddrIPv4TCP(t *testing.T) {
	IPv4 = &availableIPs{}
	ip1 := net.ParseIP("192.168.1.1")
	ipNet1 := net.IPNet{IP: ip1, Mask: net.CIDRMask(24, 32)}
	ipList := []net.IPNet{ipNet1}
	IPv4.IPs.Store(&ipList)

	addr := getLocalAddr("tcp", net.ParseIP("192.168.1.2"))
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		t.Errorf("Expected *net.TCPAddr, got %T", addr)
	}
	if !tcpAddr.IP.Equal(ip1) {
		t.Errorf("Expected IP %v, got %v", ip1, tcpAddr.IP)
	}
}

// TestGetLocalAddrIPv6TCP tests local address selection for IPv6 TCP connections.
func TestGetLocalAddrIPv6TCP(t *testing.T) {
	IPv6 = &availableIPs{}
	ip1 := net.ParseIP("2001:db8::1")
	ipNet1 := net.IPNet{IP: ip1, Mask: net.CIDRMask(64, 128)}
	ipList := []net.IPNet{ipNet1}
	IPv6.IPs.Store(&ipList)

	addr := getLocalAddr("tcp6", net.ParseIP("[2001:db8::2]"))
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		t.Errorf("Expected *net.TCPAddr, got %T", addr)
	}
	if !tcpAddr.IP.Equal(ip1) {
		t.Errorf("Expected IP %v, got %v", ip1, tcpAddr.IP)
	}
}

func TestGetLocalAddrIPv6TCPAnyIP(t *testing.T) {
	IPv6 = &availableIPs{AnyIP: true}
	ip1 := net.ParseIP("2001:db8::1")
	ipNet1 := net.IPNet{IP: ip1, Mask: net.CIDRMask(64, 128)}
	ipList := []net.IPNet{ipNet1}
	IPv6.IPs.Store(&ipList)

	addr := getLocalAddr("tcp6", net.ParseIP("[2001:db12::20]"))
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		t.Errorf("Expected *net.TCPAddr, got %T", addr)
	}
	if !ipNet1.Contains(tcpAddr.IP) {
		t.Errorf("Expected IP within %v, got %v", ipNet1, tcpAddr.IP)
	}
}

// TestGetLocalAddrIPv4UDP tests local address selection for IPv4 UDP connections.
func TestGetLocalAddrIPv4UDP(t *testing.T) {
	IPv4 = &availableIPs{}
	ip1 := net.ParseIP("192.168.1.1")
	ipNet1 := net.IPNet{IP: ip1, Mask: net.CIDRMask(24, 32)}
	ipList := []net.IPNet{ipNet1}
	IPv4.IPs.Store(&ipList)

	addr := getLocalAddr("udp", net.ParseIP("192.168.1.2"))
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		t.Errorf("Expected *net.UDPAddr, got %T", addr)
	}
	if !udpAddr.IP.Equal(ip1) {
		t.Errorf("Expected IP %v, got %v", ip1, udpAddr.IP)
	}
}

// TestGetLocalAddrIPv6UDP tests local address selection for IPv6 UDP connections.
func TestGetLocalAddrIPv6UDP(t *testing.T) {
	IPv6 = &availableIPs{}
	ip1 := net.ParseIP("2001:db8::1")
	ipNet1 := net.IPNet{IP: ip1, Mask: net.CIDRMask(64, 128)}
	ipList := []net.IPNet{ipNet1}
	IPv6.IPs.Store(&ipList)

	addr := getLocalAddr("udp", net.ParseIP("[2001:db8::2]"))
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		t.Errorf("Expected *net.UDPAddr, got %T", addr)
	}
	if !udpAddr.IP.Equal(ip1) {
		t.Errorf("Expected IP %v, got %v", ip1, udpAddr.IP)
	}
}

// TestGetLocalAddrUnknownNetwork tests the function with an unknown network type.
func TestGetLocalAddrUnknownNetwork(t *testing.T) {
	addr := getLocalAddr("unknown", net.ParseIP("192.168.1.2"))
	if addr != nil {
		t.Errorf("Expected nil, got %v", addr)
	}
}

// TestAnyIPIPv6IPv4DisabledRealLife tests the function with IPv6 enabled and IPv4 disabled.
func TestAnyIPIPv6IPv4DisabledRealLife(t *testing.T) {
	IPv6 = &availableIPs{AnyIP: true}
	IPv4 = &availableIPs{}
	ip1 := net.ParseIP("2001:db8::1")
	ipNet1 := net.IPNet{IP: ip1, Mask: net.CIDRMask(64, 128)}
	ipList := []net.IPNet{ipNet1}
	IPv6.IPs.Store(&ipList)

	tcpAddr := getLocalAddr("tcp6", net.ParseIP("2606:4700:3030::ac43:a86a"))
	if tcpAddr == nil {
		t.Error("Expected non-nil TCP address, got nil")
	}
	t.Logf("IPv6 TCP address: %v", tcpAddr)
}

// TestGetAvailableIPs is difficult due to its infinite loop and dependency on system interfaces.
func TestGetAvailableIPsAnyIP(t *testing.T) {
	if IPv6 == nil {
		IPv6 = &availableIPs{
			AnyIP: true,
		}
	}

	if IPv4 == nil {
		IPv4 = &availableIPs{}
	}

	// Get all network interfaces
	interfaces, err := net.Interfaces()
	if err != nil {

	}

	// Iterate over the interfaces
	newIPv4 := make([]net.IPNet, 0)
	newIPv6 := make([]net.IPNet, 0)
	for _, iface := range interfaces {
		if strings.Contains(iface.Name, "docker") || iface.Flags&net.FlagPointToPoint != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		} else {
			t.Logf("Interface: %v", iface.Name)
			t.Logf("Flags: %v", iface.Flags)
		}

		// Get the addresses associated with the interface
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		// Iterate over the addresses
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok {
				ip := ipNet.IP

				if ip.IsLoopback() {
					continue
				}

				t.Logf("IPNet: %v", ipNet)

				// Process Global Unicast IPv6 addresses
				if ip.IsGlobalUnicast() && ip.To16() != nil && ip.To4() == nil && ip.IsGlobalUnicast() {
					newIPv6 = append(newIPv6, *ipNet)
				}

				// Process Global Unicast IPv4 addresses
				if ip.IsGlobalUnicast() && ip.To16() == nil && ip.To4() != nil {
					// Add IPv4 addresses to the list
					newIPv4 = append(newIPv4, *ipNet)
				}
			}
		}
	}

	// Add the new addresses to the list
	IPv6.IPs.Store(&newIPv6)
	IPv4.IPs.Store(&newIPv4)

	tcpv6Addr := getLocalAddr("tcp", net.ParseIP("[2001:db8::2]:80"))
	t.Logf("IPv6 TCP address: %v", tcpv6Addr)
}
