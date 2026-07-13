package tun

import (
	"fmt"
	"io"
	"net"
	"os/exec"
	"runtime"
)

// Device represents a TUN network device.
type Device struct {
	name   string
	file   io.ReadWriteCloser
	mtu    int
}

// Config holds TUN device configuration.
type Config struct {
	// Name is the desired interface name (e.g., "rtun0"). Empty = auto-assign.
	Name string
	// IP is the IP address to assign (e.g., "10.99.0.1/16").
	IP string
	// MTU for the interface (default: 1500).
	MTU int
}

// New creates and configures a TUN device.
func New(cfg Config) (*Device, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("TUN mode only supported on Linux")
	}
	if cfg.MTU == 0 {
		cfg.MTU = 1500
	}

	dev, err := openTUN(cfg.Name)
	if err != nil {
		return nil, fmt.Errorf("creating TUN device: %w", err)
	}

	// Configure IP and bring up
	if cfg.IP != "" {
		if err := dev.configureIP(cfg.IP); err != nil {
			dev.Close()
			return nil, fmt.Errorf("configuring TUN IP: %w", err)
		}
	}

	if err := dev.setMTU(cfg.MTU); err != nil {
		dev.Close()
		return nil, fmt.Errorf("setting MTU: %w", err)
	}

	if err := dev.setUp(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("bringing up TUN: %w", err)
	}

	return dev, nil
}

// Name returns the interface name.
func (d *Device) Name() string {
	return d.name
}

// Read reads a single IP packet from the TUN device.
func (d *Device) Read(buf []byte) (int, error) {
	return d.file.Read(buf)
}

// Write writes a single IP packet to the TUN device.
func (d *Device) Write(buf []byte) (int, error) {
	return d.file.Write(buf)
}

// Close destroys the TUN device.
func (d *Device) Close() error {
	return d.file.Close()
}

// configureIP sets the IP address on the interface.
func (d *Device) configureIP(cidr string) error {
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("parsing CIDR %q: %w", cidr, err)
	}
	// ip addr add <ip>/<mask> dev <name>
	addr := fmt.Sprintf("%s/%d", ip.String(), maskBits(ipNet.Mask))
	return runCmd("ip", "addr", "add", addr, "dev", d.name)
}

// setMTU sets the MTU on the interface.
func (d *Device) setMTU(mtu int) error {
	return runCmd("ip", "link", "set", d.name, "mtu", fmt.Sprintf("%d", mtu))
}

// setUp brings the interface up.
func (d *Device) setUp() error {
	return runCmd("ip", "link", "set", d.name, "up")
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %v: %s: %w", name, args, string(out), err)
	}
	return nil
}

func maskBits(mask net.IPMask) int {
	bits, _ := mask.Size()
	return bits
}
