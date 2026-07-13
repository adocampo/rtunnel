package tun

import (
	"encoding/binary"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

const utunControlName = "com.apple.net.utun_control"

const (
	// Darwin control-socket protocol and utun sockopt.
	sysProtoControl = 2
	utunOptIfName   = 2
)

// openTUN creates a utun device on macOS and wraps it to exchange raw IP packets.
func openTUN(name string) (*Device, error) {
	fd, err := unix.Socket(unix.AF_SYSTEM, unix.SOCK_DGRAM, sysProtoControl)
	if err != nil {
		return nil, fmt.Errorf("opening AF_SYSTEM socket: %w", err)
	}

	var ctlInfo unix.CtlInfo
	copy(ctlInfo.Name[:], []byte(utunControlName))
	if err := unix.IoctlCtlInfo(fd, &ctlInfo); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("CTLIOCGINFO ioctl: %w", err)
	}

	addr := &unix.SockaddrCtl{ID: ctlInfo.Id, Unit: 0}
	if err := unix.Connect(fd, addr); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("connecting utun control socket: %w", err)
	}

	ifName, err := unix.GetsockoptString(fd, sysProtoControl, utunOptIfName)
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("getting utun interface name: %w", err)
	}

	wrapped := &utunFile{f: os.NewFile(uintptr(fd), ifName)}
	return &Device{
		name: ifName,
		file: wrapped,
		mtu:  1500,
	}, nil
}

// utunFile adapts macOS utun framing (4-byte family prefix) to raw IP packets.
type utunFile struct {
	f *os.File
}

func (u *utunFile) Read(p []byte) (int, error) {
	buf := make([]byte, len(p)+4)
	n, err := u.f.Read(buf)
	if n <= 4 {
		if n < 0 {
			n = 0
		}
		return 0, err
	}
	copy(p, buf[4:n])
	return n - 4, err
}

func (u *utunFile) Write(p []byte) (int, error) {
	frame := make([]byte, len(p)+4)
	family := uint32(unix.AF_INET)
	if len(p) > 0 && (p[0]>>4) == 6 {
		family = unix.AF_INET6
	}
	binary.BigEndian.PutUint32(frame[:4], family)
	copy(frame[4:], p)
	n, err := u.f.Write(frame)
	if n >= 4 {
		n -= 4
	} else {
		n = 0
	}
	return n, err
}

func (u *utunFile) Close() error {
	return u.f.Close()
}