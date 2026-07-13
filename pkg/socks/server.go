package socks

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"

	"github.com/malevolent/rtunnel/pkg/tunnel"
)

// Server implements a SOCKS5 proxy that forwards connections through a tunnel.
type Server struct {
	logger     *slog.Logger
	listener   net.Listener
	manager    *tunnel.Manager
	tunnelName string // which tunnel to route through
}

// NewServer creates a SOCKS5 server bound to the given address.
// All SOCKS connections will be forwarded through the named tunnel.
func NewServer(addr string, manager *tunnel.Manager, tunnelName string, logger *slog.Logger) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", addr, err)
	}

	return &Server{
		logger:     logger,
		listener:   ln,
		manager:    manager,
		tunnelName: tunnelName,
	}, nil
}

// Addr returns the listener's address.
func (s *Server) Addr() net.Addr {
	return s.listener.Addr()
}

// Serve accepts and handles SOCKS5 connections until context is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	s.logger.Info("SOCKS5 server listening", "addr", s.listener.Addr(), "tunnel", s.tunnelName)

	go func() {
		<-ctx.Done()
		s.listener.Close()
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	// SOCKS5 handshake
	target, err := s.socks5Handshake(conn)
	if err != nil {
		s.logger.Debug("SOCKS5 handshake failed", "error", err)
		return
	}

	s.logger.Debug("SOCKS5 connect", "target", target)

	// Resolve tunnel name ("*" means first available)
	tunnelName := s.tunnelName
	if tunnelName == "*" {
		tunnels := s.manager.ListTunnels()
		if len(tunnels) == 0 {
			s.logger.Debug("no tunnels available")
			s.socks5Reply(conn, 0x05)
			return
		}
		tunnelName = tunnels[0].Name
	}

	// Dial through the tunnel
	stream, err := s.manager.DialThrough(ctx, tunnelName, target, conn)
	if err != nil {
		s.logger.Debug("dial through tunnel failed", "target", target, "error", err)
		s.socks5Reply(conn, 0x05) // connection refused
		return
	}

	// Success reply
	s.socks5Reply(conn, 0x00)

	// Forward local SOCKS conn → tunnel (reads from conn, sends Data to client)
	s.manager.ForwardStreamToClient(ctx, tunnelName, stream)
}

// Close shuts down the SOCKS server.
func (s *Server) Close() error {
	return s.listener.Close()
}

// SetTunnelName changes which tunnel the SOCKS proxy routes through.
func (s *Server) SetTunnelName(name string) {
	s.tunnelName = name
}

// socks5Handshake performs the SOCKS5 protocol negotiation.
func (s *Server) socks5Handshake(conn net.Conn) (string, error) {
	buf := make([]byte, 256)

	// Read version + method count
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return "", err
	}
	if buf[0] != 0x05 {
		return "", fmt.Errorf("unsupported SOCKS version: %d", buf[0])
	}

	nmethods := int(buf[1])
	if _, err := io.ReadFull(conn, buf[:nmethods]); err != nil {
		return "", err
	}

	// No auth required
	conn.Write([]byte{0x05, 0x00})

	// Read connect request
	if _, err := io.ReadFull(conn, buf[:4]); err != nil {
		return "", err
	}
	if buf[1] != 0x01 { // CONNECT
		return "", fmt.Errorf("unsupported command: %d", buf[1])
	}

	var host string
	switch buf[3] {
	case 0x01: // IPv4
		if _, err := io.ReadFull(conn, buf[:4]); err != nil {
			return "", err
		}
		host = net.IP(buf[:4]).String()
	case 0x03: // Domain
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return "", err
		}
		domainLen := int(buf[0])
		if _, err := io.ReadFull(conn, buf[:domainLen]); err != nil {
			return "", err
		}
		host = string(buf[:domainLen])
	case 0x04: // IPv6
		if _, err := io.ReadFull(conn, buf[:16]); err != nil {
			return "", err
		}
		host = net.IP(buf[:16]).String()
	default:
		return "", fmt.Errorf("unsupported address type: %d", buf[3])
	}

	// Read port
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return "", err
	}
	port := int(buf[0])<<8 | int(buf[1])

	return net.JoinHostPort(host, fmt.Sprintf("%d", port)), nil
}

// socks5Reply sends a SOCKS5 reply.
func (s *Server) socks5Reply(conn net.Conn, rep byte) {
	conn.Write([]byte{0x05, rep, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
}
