package tun

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	tunDevice = "/dev/net/tun"
	ifnameSize = 16
)

// openTUN creates a TUN device using the Linux TUN/TAP driver.
func openTUN(name string) (*Device, error) {
	fd, err := unix.Open(tunDevice, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w (are you running as root?)", tunDevice, err)
	}

	// struct ifreq with flags IFF_TUN | IFF_NO_PI
	var ifr [unix.IFNAMSIZ + 64]byte
	if name != "" {
		copy(ifr[:ifnameSize], []byte(name))
	}
	// flags at offset IFNAMSIZ (16): IFF_TUN | IFF_NO_PI
	flags := uint16(unix.IFF_TUN | unix.IFF_NO_PI)
	*(*uint16)(unsafe.Pointer(&ifr[ifnameSize])) = flags

	if _, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		uintptr(fd),
		unix.TUNSETIFF,
		uintptr(unsafe.Pointer(&ifr[0])),
	); errno != 0 {
		unix.Close(fd)
		return nil, fmt.Errorf("TUNSETIFF ioctl: %w", errno)
	}

	// Read back the assigned name
	assignedName := string(ifr[:ifnameSize])
	// Trim null bytes
	for i, b := range assignedName {
		if b == 0 {
			assignedName = assignedName[:i]
			break
		}
	}

	return &Device{
		name: assignedName,
		file: os.NewFile(uintptr(fd), tunDevice),
		mtu:  1500,
	}, nil
}
