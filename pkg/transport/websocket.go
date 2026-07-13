package transport

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/malevolent/rtunnel/pkg/auth"
	"github.com/malevolent/rtunnel/pkg/config"
	"github.com/malevolent/rtunnel/pkg/protocol"
	"golang.org/x/crypto/ssh"
	"nhooyr.io/websocket"
)

// Client manages the outbound WebSocket connection from the private machine.
type Client struct {
	cfg        *config.ClientConfig
	logger     *slog.Logger
	mu         sync.Mutex
	streams    map[uint32]net.Conn
	noAuth     bool
	tunDev     io.ReadWriteCloser // TUN device (nil until created)
	tunName    string             // interface name after creation
	tunEnabled bool               // if true, create TUN when server sends IP
	tunCreator func(ip string) (io.ReadWriteCloser, string, error) // returns device, ifname, error
	onTunReady func(tunName, assignedIP string) // called after TUN is created
}

// NewClient creates a new transport client.
func NewClient(cfg *config.ClientConfig, logger *slog.Logger) *Client {
	return &Client{
		cfg:     cfg,
		logger:  logger,
		streams: make(map[uint32]net.Conn),
	}
}

// NewClientNoAuth creates a client that skips SSH authentication (for testing).
func NewClientNoAuth(cfg *config.ClientConfig, logger *slog.Logger) *Client {
	return &Client{
		cfg:     cfg,
		logger:  logger,
		streams: make(map[uint32]net.Conn),
		noAuth:  true,
	}
}

// EnableTUN enables TUN mode. The creator function is called when the server assigns an IP.
// It must return the ReadWriteCloser, the interface name, and any error.
func (c *Client) EnableTUN(creator func(ip string) (io.ReadWriteCloser, string, error)) {
	c.tunEnabled = true
	c.tunCreator = creator
}

// OnTunReady registers a callback invoked after the TUN device is created.
// tunName is the interface name (e.g., "utun4"), assignedIP is the CIDR (e.g., "10.99.0.2/16").
func (c *Client) OnTunReady(fn func(tunName, assignedIP string)) {
	c.onTunReady = fn
}

// SetTUN sets a pre-created TUN device.
func (c *Client) SetTUN(dev io.ReadWriteCloser) {
	c.tunDev = dev
	c.tunEnabled = true
}

// Run connects to the server and maintains the tunnel with reconnection logic.
func (c *Client) Run(ctx context.Context) error {
	attempt := 0
	for {
		err := c.connect(ctx)
		if ctx.Err() != nil {
			return nil // clean shutdown
		}
		if !c.cfg.Reconnect {
			return err
		}

		attempt++
		delay := c.backoff(attempt)
		c.logger.Warn("connection lost, reconnecting", "error", err, "attempt", attempt, "delay", delay)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
	}
}

func (c *Client) connect(ctx context.Context) error {
	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{},
	}

	if c.cfg.Insecure {
		opts.HTTPClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
	}

	url := c.cfg.Server + "/tunnel"
	c.logger.Debug("dialing server", "url", url)

	conn, _, err := websocket.Dial(ctx, url, opts)
	if err != nil {
		return fmt.Errorf("dialing %s: %w", url, err)
	}
	defer conn.CloseNow()

	conn.SetReadLimit(protocol.MaxFrameSize + protocol.FrameHeaderSize + 1024)

	// Perform handshake
	var hsErr error
	if c.noAuth {
		hsErr = c.handshakeNoAuth(ctx, conn)
	} else {
		hsErr = c.handshake(ctx, conn)
	}
	if hsErr != nil {
		return fmt.Errorf("handshake: %w", hsErr)
	}

	c.logger.Info("tunnel established", "name", c.cfg.Name)

	// Enter packet forwarding loop
	return c.forwardLoop(ctx, conn)
}

func (c *Client) handshake(ctx context.Context, conn *websocket.Conn) error {
	// Step 1: Send Hello
	hello := &protocol.Hello{
		Name:          c.cfg.Name,
		Version:       "0.1.0",
		ExposePorts:   c.cfg.ExposePorts,
		ExposeSubnets: c.cfg.ExposeSubnets,
	}

	if err := writeJSON(ctx, conn, protocol.MsgHello, hello); err != nil {
		return fmt.Errorf("sending hello: %w", err)
	}

	// Step 2: Read Challenge
	env, err := readJSON(ctx, conn)
	if err != nil {
		return fmt.Errorf("reading challenge: %w", err)
	}
	if env.Type != protocol.MsgChallenge {
		return fmt.Errorf("expected challenge, got %s", env.Type)
	}

	var challenge protocol.Challenge
	if err := protocol.Unmarshal(env, &challenge); err != nil {
		return fmt.Errorf("decoding challenge: %w", err)
	}

	// Step 3: Sign challenge
	var signer *auth.Signer
	if c.cfg.SSHKey != "" {
		signer, err = auth.NewKeySigner(c.cfg.SSHKey)
	} else {
		signer, err = auth.NewAgentSigner()
	}
	if err != nil {
		return fmt.Errorf("getting signer: %w", err)
	}

	pubKey, sig, err := signer.Sign(challenge.Nonce)
	if err != nil {
		return fmt.Errorf("signing challenge: %w", err)
	}

	// Step 4: Send ChallengeResponse
	sigBytes := ssh.Marshal(sig)
	resp := &protocol.ChallengeResponse{
		PublicKey: pubKey.Marshal(),
		Signature: sigBytes,
	}
	if err := writeJSON(ctx, conn, protocol.MsgChallengeResp, resp); err != nil {
		return fmt.Errorf("sending challenge response: %w", err)
	}

	// Step 5: Read auth result
	env, err = readJSON(ctx, conn)
	if err != nil {
		return fmt.Errorf("reading auth result: %w", err)
	}

	switch env.Type {
	case protocol.MsgAuthOK:
		return nil
	case protocol.MsgAuthFailed:
		var fail protocol.AuthFailed
		protocol.Unmarshal(env, &fail)
		return fmt.Errorf("authentication failed: %s", fail.Reason)
	default:
		return fmt.Errorf("unexpected message type: %s", env.Type)
	}
}

func (c *Client) handshakeNoAuth(ctx context.Context, conn *websocket.Conn) error {
	// Send Hello
	hello := &protocol.Hello{
		Name:          c.cfg.Name,
		Version:       "0.1.0",
		ExposePorts:   c.cfg.ExposePorts,
		ExposeSubnets: c.cfg.ExposeSubnets,
	}
	if err := writeJSON(ctx, conn, protocol.MsgHello, hello); err != nil {
		return fmt.Errorf("sending hello: %w", err)
	}

	// Wait for AuthOK (server skips challenge in no-auth mode)
	env, err := readJSON(ctx, conn)
	if err != nil {
		return fmt.Errorf("reading auth result: %w", err)
	}
	if env.Type != protocol.MsgAuthOK {
		return fmt.Errorf("expected auth_ok, got %s", env.Type)
	}
	return nil
}

func (c *Client) forwardLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("reading from server: %w", err)
		}

		switch msgType {
		case websocket.MessageText:
			// Control message
			env, err := protocol.DecodeEnvelope(data)
			if err != nil {
				c.logger.Warn("invalid control message", "error", err)
				continue
			}
			if err := c.handleControl(ctx, conn, env); err != nil {
				return err
			}

		case websocket.MessageBinary:
			// Binary frame (IP packet in TUN mode, or stream data)
			frame, err := protocol.UnmarshalFrame(data)
			if err != nil {
				c.logger.Warn("invalid frame", "error", err)
				continue
			}
			c.handleFrame(ctx, conn, frame)
		}
	}
}

func (c *Client) handleControl(ctx context.Context, conn *websocket.Conn, env *protocol.Envelope) error {
	switch env.Type {
	case protocol.MsgPing:
		var ping protocol.Ping
		protocol.Unmarshal(env, &ping)
		return writeJSON(ctx, conn, protocol.MsgPong, &protocol.Pong{Timestamp: ping.Timestamp})

	case protocol.MsgTunnelRequest:
		var req protocol.TunnelRequest
		protocol.Unmarshal(env, &req)
		c.logger.Info("tunnel configured", "assigned_ip", req.AssignedIP, "server_ip", req.ServerIP, "mode", req.Mode)
		// Warn if client expects TUN but server is not in TUN mode
		if c.tunEnabled && req.Mode != "tun" {
			c.logger.Warn("client has --tun enabled but server is in different mode — TUN will NOT be created. Start the server with --mode tun", "server_mode", req.Mode)
		}
		// If TUN mode is enabled and server wants TUN, create the device
		if c.tunEnabled && req.Mode == "tun" && c.tunDev == nil {
			if c.tunCreator != nil {
				dev, ifName, err := c.tunCreator(req.AssignedIP)
				if err != nil {
					c.logger.Error("failed to create TUN device", "error", err)
				} else {
					c.tunDev = dev
					c.tunName = ifName
					go c.readTUNLoop(ctx, conn)
					c.logger.Info("TUN device created", "ip", req.AssignedIP)
					if c.onTunReady != nil {
						c.onTunReady(ifName, req.AssignedIP)
					}
				}
			}
		} else if c.tunDev != nil && req.Mode == "tun" {
			go c.readTUNLoop(ctx, conn)
		}
		return nil

	case protocol.MsgDial:
		var dial protocol.Dial
		protocol.Unmarshal(env, &dial)
		go c.handleDial(ctx, conn, &dial)
		return nil

	case protocol.MsgData:
		var data protocol.Data
		protocol.Unmarshal(env, &data)
		c.mu.Lock()
		stream, ok := c.streams[data.StreamID]
		c.mu.Unlock()
		if ok {
			stream.Write(data.Payload)
		}
		return nil

	case protocol.MsgDataClose:
		var dc protocol.DataClose
		protocol.Unmarshal(env, &dc)
		c.mu.Lock()
		stream, ok := c.streams[dc.StreamID]
		if ok {
			delete(c.streams, dc.StreamID)
		}
		c.mu.Unlock()
		if ok {
			stream.Close()
		}
		return nil

	case protocol.MsgTunnelClose:
		return fmt.Errorf("server closed tunnel")

	default:
		c.logger.Debug("unhandled control message", "type", env.Type)
		return nil
	}
}

func (c *Client) handleDial(ctx context.Context, conn *websocket.Conn, dial *protocol.Dial) {
	c.logger.Info("dial request", "stream", dial.StreamID, "address", dial.Address)

	// Resolve address: if the target port matches an exposed port, dial localhost
	target := dial.Address
	if _, portStr, err := net.SplitHostPort(dial.Address); err == nil {
		var port int
		fmt.Sscanf(portStr, "%d", &port)
		for _, p := range c.cfg.ExposePorts {
			if p == port {
				target = fmt.Sprintf("127.0.0.1:%d", port)
				break
			}
		}
	}

	// Dial the target
	localConn, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		c.logger.Warn("dial failed", "address", target, "error", err)
		writeJSON(ctx, conn, protocol.MsgDialResult, &protocol.DialResult{
			StreamID: dial.StreamID,
			OK:       false,
			Error:    err.Error(),
		})
		return
	}

	// Send success
	writeJSON(ctx, conn, protocol.MsgDialResult, &protocol.DialResult{
		StreamID: dial.StreamID,
		OK:       true,
	})

	c.mu.Lock()
	c.streams[dial.StreamID] = localConn
	c.mu.Unlock()

	// Forward local → WebSocket
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := localConn.Read(buf)
			if n > 0 {
				writeJSON(ctx, conn, protocol.MsgData, &protocol.Data{
					StreamID: dial.StreamID,
					Payload:  buf[:n],
				})
			}
			if err != nil {
				writeJSON(ctx, conn, protocol.MsgDataClose, &protocol.DataClose{
					StreamID: dial.StreamID,
				})
				c.mu.Lock()
				delete(c.streams, dial.StreamID)
				c.mu.Unlock()
				localConn.Close()
				return
			}
		}
	}()
}

func (c *Client) handleFrame(ctx context.Context, conn *websocket.Conn, frame *protocol.Frame) {
	if c.tunDev == nil {
		c.logger.Debug("received frame but no TUN device", "tunnel", frame.TunnelID, "size", len(frame.Payload))
		return
	}
	// Write IP packet to local TUN device
	if _, err := c.tunDev.Write(frame.Payload); err != nil {
		c.logger.Warn("TUN write error", "error", err)
	}
}

// readTUNLoop reads IP packets from the local TUN device and sends them to the server.
func (c *Client) readTUNLoop(ctx context.Context, conn *websocket.Conn) {
	buf := make([]byte, 65536)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := c.tunDev.Read(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.logger.Warn("TUN read error", "error", err)
			return
		}

		if n < 20 {
			continue
		}

		// Send as binary frame
		frame := &protocol.Frame{
			TunnelID: 0, // single tunnel from client side
			Payload:  buf[:n],
		}
		data := protocol.MarshalFrame(frame)
		if err := conn.Write(ctx, websocket.MessageBinary, data); err != nil {
			if ctx.Err() != nil {
				return
			}
			c.logger.Debug("failed to send frame to server", "error", err)
			return
		}
	}
}

func (c *Client) backoff(attempt int) time.Duration {
	base := c.cfg.ReconnectInterval
	multiplier := math.Pow(1.5, float64(attempt-1))
	delay := time.Duration(float64(base) * multiplier)
	if delay > 60*time.Second {
		delay = 60 * time.Second
	}
	return delay
}

// Helper functions for JSON WebSocket messages

func writeJSON(ctx context.Context, conn *websocket.Conn, msgType protocol.MessageType, payload interface{}) error {
	env, err := protocol.Marshal(msgType, payload)
	if err != nil {
		return err
	}
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

func readJSON(ctx context.Context, conn *websocket.Conn) (*protocol.Envelope, error) {
	_, data, err := conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	return protocol.DecodeEnvelope(data)
}
