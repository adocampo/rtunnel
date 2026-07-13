package integration

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/malevolent/rtunnel/pkg/config"
	"github.com/malevolent/rtunnel/pkg/socks"
	"github.com/malevolent/rtunnel/pkg/tunnel"
	"github.com/malevolent/rtunnel/pkg/transport"
)

// TestSOCKS5EndToEnd tests the full flow:
// 1. Start a dummy TCP server (simulates sshd on the "private" machine)
// 2. Start the rtunnel server (with auth disabled for testing)
// 3. Start the rtunnel client (connecting to the server)
// 4. Start a SOCKS5 proxy pointing at the tunnel
// 5. Connect through SOCKS5 to the dummy service
// 6. Verify bidirectional data flows correctly
func TestSOCKS5EndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Step 1: Start a dummy TCP echo server (simulates a service on the private machine)
	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echoLn.Close()
	echoAddr := echoLn.Addr().String()
	t.Logf("echo server on %s", echoAddr)

	go func() {
		for {
			conn, err := echoLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				// Echo: read and write back with prefix
				buf := make([]byte, 1024)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					reply := fmt.Sprintf("echo:%s", string(buf[:n]))
					c.Write([]byte(reply))
				}
			}(conn)
		}
	}()

	// Step 2: Start rtunnel server (no-auth mode for testing)
	serverCfg := &config.ServerConfig{
		Listen:         "127.0.0.1:0",
		AuthorizedKeys: "",
		IPPool:         "10.99.0.0/16",
		Mode:           "socks",
		Verbose:        true,
	}

	manager := tunnel.NewManager(serverCfg, logger)

	// Use a no-auth handler for testing
	mux := http.NewServeMux()
	mux.HandleFunc("/tunnel", manager.HandleNoAuth())

	serverLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer serverLn.Close()
	serverAddr := serverLn.Addr().String()
	t.Logf("rtunnel server on %s", serverAddr)

	httpServer := &http.Server{Handler: mux}
	go httpServer.Serve(serverLn)
	defer httpServer.Close()

	// Step 3: Start rtunnel client
	_, echoPort, _ := net.SplitHostPort(echoAddr)
	var port int
	fmt.Sscanf(echoPort, "%d", &port)

	clientCfg := &config.ClientConfig{
		Server:            "ws://" + serverAddr,
		Name:              "test-tunnel",
		ExposePorts:       []int{port},
		Reconnect:         false,
		ReconnectInterval: time.Second,
		Insecure:          true,
		Verbose:           true,
	}

	client := transport.NewClientNoAuth(clientCfg, logger)
	go func() {
		if err := client.Run(ctx); err != nil && ctx.Err() == nil {
			t.Logf("client error: %v", err)
		}
	}()

	// Wait for client to connect and register
	time.Sleep(500 * time.Millisecond)

	// Step 4: Start SOCKS5 proxy
	socksServer, err := socks.NewServer("127.0.0.1:0", manager, "test-tunnel", logger)
	if err != nil {
		t.Fatal(err)
	}
	defer socksServer.Close()

	go socksServer.Serve(ctx)
	socksAddr := socksServer.Addr().String()
	t.Logf("SOCKS5 proxy on %s", socksAddr)

	// Step 5: Connect through SOCKS5 to the echo service
	conn, err := dialSOCKS5(socksAddr, echoAddr)
	if err != nil {
		t.Fatalf("SOCKS5 dial failed: %v", err)
	}
	defer conn.Close()

	// Step 6: Test bidirectional data
	testMsg := "hello rtunnel!"
	_, err = conn.Write([]byte(testMsg))
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	buf := make([]byte, 1024)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	expected := "echo:" + testMsg
	got := string(buf[:n])
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}

	t.Logf("SUCCESS: sent %q, received %q through SOCKS5 tunnel", testMsg, got)

	// Test multiple messages
	for i := 0; i < 5; i++ {
		msg := fmt.Sprintf("message-%d", i)
		conn.Write([]byte(msg))
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, err = conn.Read(buf)
		if err != nil {
			t.Fatalf("read %d failed: %v", i, err)
		}
		expected = "echo:" + msg
		if string(buf[:n]) != expected {
			t.Fatalf("message %d: expected %q, got %q", i, expected, string(buf[:n]))
		}
	}

	t.Log("SUCCESS: all messages passed through SOCKS5 tunnel correctly")
}

// TestMultipleStreams tests concurrent connections through the tunnel.
func TestMultipleStreams(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Echo server
	echoLn, _ := net.Listen("tcp", "127.0.0.1:0")
	defer echoLn.Close()
	echoAddr := echoLn.Addr().String()

	go func() {
		for {
			conn, err := echoLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c) // simple echo
			}(conn)
		}
	}()

	// Server
	serverCfg := &config.ServerConfig{
		IPPool:  "10.99.0.0/16",
		Mode:    "socks",
		Verbose: true,
	}
	manager := tunnel.NewManager(serverCfg, logger)
	mux := http.NewServeMux()
	mux.HandleFunc("/tunnel", manager.HandleNoAuth())

	serverLn, _ := net.Listen("tcp", "127.0.0.1:0")
	defer serverLn.Close()
	httpServer := &http.Server{Handler: mux}
	go httpServer.Serve(serverLn)
	defer httpServer.Close()

	// Client
	_, echoPort, _ := net.SplitHostPort(echoAddr)
	var port int
	fmt.Sscanf(echoPort, "%d", &port)

	clientCfg := &config.ClientConfig{
		Server:            "ws://" + serverLn.Addr().String(),
		Name:              "multi-test",
		ExposePorts:       []int{port},
		Reconnect:         false,
		ReconnectInterval: time.Second,
		Insecure:          true,
		Verbose:           true,
	}
	client := transport.NewClientNoAuth(clientCfg, logger)
	go client.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	// SOCKS proxy
	socksServer, _ := socks.NewServer("127.0.0.1:0", manager, "multi-test", logger)
	defer socksServer.Close()
	go socksServer.Serve(ctx)
	socksAddr := socksServer.Addr().String()

	// Open 10 concurrent connections
	const numConns = 10
	done := make(chan error, numConns)

	for i := 0; i < numConns; i++ {
		go func(id int) {
			conn, err := dialSOCKS5(socksAddr, echoAddr)
			if err != nil {
				done <- fmt.Errorf("conn %d: dial: %w", id, err)
				return
			}
			defer conn.Close()

			msg := fmt.Sprintf("stream-%d-data", id)
			conn.Write([]byte(msg))
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			buf := make([]byte, 256)
			n, err := conn.Read(buf)
			if err != nil {
				done <- fmt.Errorf("conn %d: read: %w", id, err)
				return
			}
			if string(buf[:n]) != msg {
				done <- fmt.Errorf("conn %d: expected %q, got %q", id, msg, string(buf[:n]))
				return
			}
			done <- nil
		}(i)
	}

	for i := 0; i < numConns; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}

	t.Logf("SUCCESS: %d concurrent streams all passed", numConns)
}

// dialSOCKS5 connects to a SOCKS5 proxy and requests a connection to target.
func dialSOCKS5(proxyAddr, target string) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
	if err != nil {
		return nil, err
	}

	// SOCKS5 greeting: version=5, 1 method, no-auth
	conn.Write([]byte{0x05, 0x01, 0x00})

	// Read response
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		conn.Close()
		return nil, fmt.Errorf("greeting response: %w", err)
	}
	if buf[0] != 0x05 || buf[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("bad greeting response: %x", buf)
	}

	// Connect request
	host, portStr, _ := net.SplitHostPort(target)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	ip := net.ParseIP(host)
	var req []byte
	if ip4 := ip.To4(); ip4 != nil {
		req = []byte{0x05, 0x01, 0x00, 0x01}
		req = append(req, ip4...)
	} else {
		// Domain name
		req = []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
		req = append(req, []byte(host)...)
	}
	req = append(req, byte(port>>8), byte(port&0xff))
	conn.Write(req)

	// Read connect response
	resp := make([]byte, 10) // minimal IPv4 response
	if _, err := io.ReadFull(conn, resp); err != nil {
		conn.Close()
		return nil, fmt.Errorf("connect response: %w", err)
	}
	if resp[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("SOCKS5 connect failed: rep=%d", resp[1])
	}

	return conn, nil
}
