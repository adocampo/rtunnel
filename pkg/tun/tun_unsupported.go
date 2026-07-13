//go:build !linux && !darwin

package tun

import "fmt"

// openTUN is unavailable on non-Linux platforms.
func openTUN(name string) (*Device, error) {
	return nil, fmt.Errorf("TUN mode only supported on Linux")
}