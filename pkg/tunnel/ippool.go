package tunnel

import (
	"fmt"
	"net"
	"sync"
)

// IPPool manages a pool of IPs for tunnel allocation.
// It allocates IPs from a CIDR range, skipping the network and broadcast addresses.
type IPPool struct {
	mu        sync.Mutex
	network   *net.IPNet
	allocated map[string]bool
	nextIP    net.IP
}

// NewIPPool creates a new IP pool from a CIDR notation (e.g., "10.99.0.0/16").
func NewIPPool(cidr string) (*IPPool, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("parsing CIDR %q: %w", cidr, err)
	}

	// Start allocation from .2 (skip .0 network and .1 for server gateway)
	startIP := make(net.IP, len(network.IP))
	copy(startIP, network.IP)
	startIP = incrementIP(startIP)
	startIP = incrementIP(startIP)

	return &IPPool{
		network:   network,
		allocated: make(map[string]bool),
		nextIP:    startIP,
	}, nil
}

// Allocate returns the next available IP from the pool.
func (p *IPPool) Allocate() (net.IP, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Try from nextIP onwards
	ip := make(net.IP, len(p.nextIP))
	copy(ip, p.nextIP)

	for i := 0; i < 65534; i++ { // safety limit
		if !p.network.Contains(ip) {
			return nil, fmt.Errorf("IP pool exhausted")
		}
		if !p.allocated[ip.String()] {
			p.allocated[ip.String()] = true
			p.nextIP = incrementIP(copyIP(ip))
			return ip, nil
		}
		ip = incrementIP(ip)
	}

	return nil, fmt.Errorf("IP pool exhausted")
}

// Release returns an IP to the pool.
func (p *IPPool) Release(ip net.IP) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.allocated, ip.String())
}

// GatewayIP returns the gateway IP (.1) for the pool's network.
func (p *IPPool) GatewayIP() net.IP {
	gw := make(net.IP, len(p.network.IP))
	copy(gw, p.network.IP)
	return incrementIP(gw)
}

// Network returns the pool's network.
func (p *IPPool) Network() *net.IPNet {
	return p.network
}

func incrementIP(ip net.IP) net.IP {
	ip = ip.To4()
	if ip == nil {
		return nil
	}
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
	return ip
}

func copyIP(ip net.IP) net.IP {
	dup := make(net.IP, len(ip))
	copy(dup, ip)
	return dup
}
