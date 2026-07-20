package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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

// TunnelInfo is the JSON-serializable status of a tunnel.
type TunnelInfo struct {
	ID          uint32 `json:"id"`
	Name        string `json:"name"`
	RemoteAddr  string `json:"remote_addr"`
	AssignedIP  string `json:"assigned_ip"`
	ExposePorts []int  `json:"expose_ports,omitempty"`
}

// Tunnel represents an active tunnel to a remote client.
type Tunnel struct {
	ID          uint32
	Name        string
	RemoteAddr  string
	AssignedIP  net.IP
	ExposePorts []int
	Conn        *websocket.Conn
	cancel      context.CancelFunc

	// Stream routing for SOCKS/proxy mode
	mu         sync.Mutex
	streams    map[uint32]*Stream
	nextStream uint32
}

// Stream represents a proxied TCP connection through the tunnel.
type Stream struct {
	ID       uint32
	Conn     net.Conn  // local SOCKS connection
	ResultCh chan bool // signals dial result
}

// NewStream creates a stream and registers it in the tunnel.
func (t *Tunnel) NewStream(conn net.Conn) *Stream {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextStream++
	s := &Stream{
		ID:       t.nextStream,
		Conn:     conn,
		ResultCh: make(chan bool, 1),
	}
	t.streams[s.ID] = s
	return s
}

// GetStream returns a stream by ID.
func (t *Tunnel) GetStream(id uint32) (*Stream, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.streams[id]
	return s, ok
}

// RemoveStream removes and returns a stream.
func (t *Tunnel) RemoveStream(id uint32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streams, id)
}

// Manager handles multiple tunnels.
type Manager struct {
	cfg       *config.ServerConfig
	logger    *slog.Logger
	mu        sync.RWMutex
	tunnels   map[string]*Tunnel // name -> tunnel
	byIP      map[string]*Tunnel // IP string -> tunnel (for TUN routing)
	stickyIPs map[string]net.IP  // name -> last assigned IP (for affinity)
	nextID    uint32
	ipPool    *IPPool
	tunDev    io.ReadWriteCloser // TUN device (nil if mode != tun)
}

// NewManager creates a new tunnel manager.
func NewManager(cfg *config.ServerConfig, logger *slog.Logger) *Manager {
	pool, err := NewIPPool(cfg.IPPool)
	if err != nil {
		logger.Error("invalid IP pool, using default", "pool", cfg.IPPool, "error", err)
		pool, _ = NewIPPool("10.99.0.0/16")
	}

	return &Manager{
		cfg:       cfg,
		logger:    logger,
		tunnels:   make(map[string]*Tunnel),
		byIP:      make(map[string]*Tunnel),
		stickyIPs: make(map[string]net.IP),
		nextID:    1,
		ipPool:    pool,
	}
}

// HandleConnection returns an HTTP handler for incoming WebSocket tunnel connections.
func (m *Manager) HandleConnection(verifier *auth.Verifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true, // TLS is handled at the HTTP server level
		})
		if err != nil {
			m.logger.Error("websocket accept failed", "error", err)
			return
		}
		defer conn.CloseNow()

		conn.SetReadLimit(protocol.MaxFrameSize + protocol.FrameHeaderSize + 1024)

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		if err := m.handleClient(ctx, conn, verifier, r.RemoteAddr, cancel); err != nil {
			m.logger.Error("client session error", "remote", r.RemoteAddr, "error", err)
		}
	}
}

// HandleNoAuth returns an HTTP handler that skips authentication (for testing).
func (m *Manager) HandleNoAuth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			m.logger.Error("websocket accept failed", "error", err)
			return
		}
		defer conn.CloseNow()

		conn.SetReadLimit(protocol.MaxFrameSize + protocol.FrameHeaderSize + 1024)

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		if err := m.handleClientNoAuth(ctx, conn, r.RemoteAddr, cancel); err != nil {
			m.logger.Error("client session error", "remote", r.RemoteAddr, "error", err)
		}
	}
}

func (m *Manager) handleClient(ctx context.Context, conn *websocket.Conn, verifier *auth.Verifier, remoteAddr string, cancel context.CancelFunc) error {
	// Step 1: Read Hello
	env, err := readJSON(ctx, conn)
	if err != nil {
		return fmt.Errorf("reading hello: %w", err)
	}
	if env.Type != protocol.MsgHello {
		return fmt.Errorf("expected hello, got %s", env.Type)
	}

	var hello protocol.Hello
	if err := protocol.Unmarshal(env, &hello); err != nil {
		return fmt.Errorf("decoding hello: %w", err)
	}

	m.logger.Info("client hello", "name", hello.Name, "remote", remoteAddr, "ports", hello.ExposePorts)

	// Step 2: Send Challenge
	nonce, err := auth.GenerateNonce()
	if err != nil {
		return fmt.Errorf("generating nonce: %w", err)
	}

	sessionID := fmt.Sprintf("%s-%d", hello.Name, m.nextID)
	challenge := &protocol.Challenge{
		Nonce:     nonce,
		SessionID: sessionID,
	}
	if err := writeJSON(ctx, conn, protocol.MsgChallenge, challenge); err != nil {
		return fmt.Errorf("sending challenge: %w", err)
	}

	// Step 3: Read ChallengeResponse
	env, err = readJSON(ctx, conn)
	if err != nil {
		return fmt.Errorf("reading challenge response: %w", err)
	}
	if env.Type != protocol.MsgChallengeResp {
		return fmt.Errorf("expected challenge_response, got %s", env.Type)
	}

	var resp protocol.ChallengeResponse
	if err := protocol.Unmarshal(env, &resp); err != nil {
		return fmt.Errorf("decoding challenge response: %w", err)
	}

	// Step 4: Verify signature
	pubKey, err := ssh.ParsePublicKey(resp.PublicKey)
	if err != nil {
		writeJSON(ctx, conn, protocol.MsgAuthFailed, &protocol.AuthFailed{Reason: "invalid public key"})
		return fmt.Errorf("parsing public key: %w", err)
	}

	if !verifier.IsAuthorized(pubKey) {
		writeJSON(ctx, conn, protocol.MsgAuthFailed, &protocol.AuthFailed{Reason: "key not authorized"})
		return fmt.Errorf("unauthorized key: %s", ssh.FingerprintSHA256(pubKey))
	}

	sig := new(ssh.Signature)
	if err := ssh.Unmarshal(resp.Signature, sig); err != nil {
		writeJSON(ctx, conn, protocol.MsgAuthFailed, &protocol.AuthFailed{Reason: "invalid signature format"})
		return fmt.Errorf("unmarshaling signature: %w", err)
	}

	if err := auth.VerifySignature(pubKey, nonce, sig); err != nil {
		writeJSON(ctx, conn, protocol.MsgAuthFailed, &protocol.AuthFailed{Reason: "signature verification failed"})
		return fmt.Errorf("signature verification: %w", err)
	}

	// Step 5: Send AuthOK
	if err := writeJSON(ctx, conn, protocol.MsgAuthOK, &protocol.AuthOK{SessionID: sessionID}); err != nil {
		return fmt.Errorf("sending auth ok: %w", err)
	}

	// Step 6: Register tunnel
	tun, err := m.registerTunnelWithPorts(hello.Name, remoteAddr, conn, cancel, hello.ExposePorts, hello.RequestIP)
	if err != nil {
		return fmt.Errorf("registering tunnel: %w", err)
	}
	defer m.unregisterTunnel(hello.Name)

	// Step 7: Send TunnelRequest with IP assignment
	mask, _ := m.ipPool.Network().Mask.Size()
	tunnelReq := &protocol.TunnelRequest{
		AssignedIP: fmt.Sprintf("%s/%d", tun.AssignedIP.String(), mask),
		ServerIP:   fmt.Sprintf("%s/%d", m.ipPool.GatewayIP().String(), mask),
		Mode:       m.cfg.Mode,
	}
	if err := writeJSON(ctx, conn, protocol.MsgTunnelRequest, tunnelReq); err != nil {
		return fmt.Errorf("sending tunnel request: %w", err)
	}

	m.logger.Info("tunnel established", "name", hello.Name, "ip", tun.AssignedIP, "id", tun.ID)

	// Step 8: Enter forwarding loop
	return m.forwardLoop(ctx, conn, tun)
}

func (m *Manager) handleClientNoAuth(ctx context.Context, conn *websocket.Conn, remoteAddr string, cancel context.CancelFunc) error {
	// Read Hello (same as authenticated flow, but skip auth)
	env, err := readJSON(ctx, conn)
	if err != nil {
		return fmt.Errorf("reading hello: %w", err)
	}
	if env.Type != protocol.MsgHello {
		return fmt.Errorf("expected hello, got %s", env.Type)
	}

	var hello protocol.Hello
	if err := protocol.Unmarshal(env, &hello); err != nil {
		return fmt.Errorf("decoding hello: %w", err)
	}

	m.logger.Info("client hello (no-auth)", "name", hello.Name, "remote", remoteAddr)

	// Send AuthOK immediately
	sessionID := fmt.Sprintf("%s-%d", hello.Name, m.nextID)
	if err := writeJSON(ctx, conn, protocol.MsgAuthOK, &protocol.AuthOK{SessionID: sessionID}); err != nil {
		return fmt.Errorf("sending auth ok: %w", err)
	}

	// Register tunnel
	tun, err := m.registerTunnelWithPorts(hello.Name, remoteAddr, conn, cancel, hello.ExposePorts, hello.RequestIP)
	if err != nil {
		return fmt.Errorf("registering tunnel: %w", err)
	}
	defer m.unregisterTunnel(hello.Name)

	// Send TunnelRequest with IP assignment
	mask, _ := m.ipPool.Network().Mask.Size()
	tunnelReq := &protocol.TunnelRequest{
		AssignedIP: fmt.Sprintf("%s/%d", tun.AssignedIP.String(), mask),
		ServerIP:   fmt.Sprintf("%s/%d", m.ipPool.GatewayIP().String(), mask),
		Mode:       m.cfg.Mode,
	}
	if err := writeJSON(ctx, conn, protocol.MsgTunnelRequest, tunnelReq); err != nil {
		return fmt.Errorf("sending tunnel request: %w", err)
	}

	m.logger.Info("tunnel established (no-auth)", "name", hello.Name, "ip", tun.AssignedIP, "id", tun.ID)

	return m.forwardLoop(ctx, conn, tun)
}

func (m *Manager) registerTunnel(name, remoteAddr string, conn *websocket.Conn, cancel context.CancelFunc) (*Tunnel, error) {
	return m.registerTunnelWithIP(name, remoteAddr, conn, cancel, "")
}

func (m *Manager) registerTunnelWithIP(name, remoteAddr string, conn *websocket.Conn, cancel context.CancelFunc, requestIP string) (*Tunnel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Close existing tunnel with same name
	if existing, ok := m.tunnels[name]; ok {
		existing.cancel()
		m.ipPool.Release(existing.AssignedIP)
	}

	var ip net.IP
	var err error

	// Priority: 1) client-requested IP, 2) sticky (last used by this name), 3) pool
	if requestIP != "" {
		requested := net.ParseIP(requestIP)
		if requested != nil {
			if allocErr := m.ipPool.AllocateSpecific(requested); allocErr == nil {
				ip = requested
				m.logger.Info("assigned requested IP", "name", name, "ip", ip)
			} else {
				m.logger.Warn("requested IP unavailable, falling back to pool", "requested", requestIP, "error", allocErr)
			}
		}
	}

	if ip == nil {
		if sticky, ok := m.stickyIPs[name]; ok {
			if allocErr := m.ipPool.AllocateSpecific(sticky); allocErr == nil {
				ip = sticky
				m.logger.Debug("reused sticky IP", "name", name, "ip", ip)
			}
		}
	}

	if ip == nil {
		ip, err = m.ipPool.Allocate()
		if err != nil {
			return nil, fmt.Errorf("allocating IP: %w", err)
		}
	}

	// Remember this assignment for future reconnects
	m.stickyIPs[name] = ip

	tun := &Tunnel{
		ID:         m.nextID,
		Name:       name,
		RemoteAddr: remoteAddr,
		AssignedIP: ip,
		Conn:       conn,
		cancel:     cancel,
		streams:    make(map[uint32]*Stream),
	}
	m.nextID++
	m.tunnels[name] = tun
	m.byIP[ip.String()] = tun

	return tun, nil
}

func (m *Manager) registerTunnelWithPorts(name, remoteAddr string, conn *websocket.Conn, cancel context.CancelFunc, ports []int, requestIP string) (*Tunnel, error) {
	tun, err := m.registerTunnelWithIP(name, remoteAddr, conn, cancel, requestIP)
	if err != nil {
		return nil, err
	}
	tun.ExposePorts = ports
	return tun, nil
}

// HandleStatus returns an HTTP handler that reports connected tunnels as JSON.
func (m *Manager) HandleStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		infos := make([]TunnelInfo, 0, len(m.tunnels))
		for _, t := range m.tunnels {
			infos = append(infos, TunnelInfo{
				ID:          t.ID,
				Name:        t.Name,
				RemoteAddr:  t.RemoteAddr,
				AssignedIP:  t.AssignedIP.String(),
				ExposePorts: t.ExposePorts,
			})
		}
		m.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"tunnels": infos})
	}
}

func (m *Manager) unregisterTunnel(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if tun, ok := m.tunnels[name]; ok {
		m.ipPool.Release(tun.AssignedIP)
		delete(m.tunnels, name)
		delete(m.byIP, tun.AssignedIP.String())
		m.logger.Info("tunnel removed", "name", name)
	}
}

func (m *Manager) forwardLoop(ctx context.Context, conn *websocket.Conn, tun *Tunnel) error {
	// Start server-side keepalive ping loop
	loopCtx, loopCancel := context.WithCancel(ctx)
	defer loopCancel()

	errCh := make(chan error, 2)
	go func() { errCh <- m.pingLoop(loopCtx, conn, tun.Name) }()
	go func() { errCh <- m.readLoop(loopCtx, conn, tun) }()

	err := <-errCh
	loopCancel()
	return err
}

// pingLoop sends periodic pings to the client to keep the connection alive.
func (m *Manager) pingLoop(ctx context.Context, conn *websocket.Conn, name string) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := writeJSON(ctx, conn, protocol.MsgPing, &protocol.Ping{Timestamp: time.Now().UnixMilli()}); err != nil {
				return fmt.Errorf("ping to %s failed: %w", name, err)
			}
			m.logger.Debug("keepalive ping sent", "tunnel", name)
		}
	}
}

func (m *Manager) readLoop(ctx context.Context, conn *websocket.Conn, tun *Tunnel) error {
	for {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("reading from client: %w", err)
		}

		switch msgType {
		case websocket.MessageText:
			env, err := protocol.DecodeEnvelope(data)
			if err != nil {
				m.logger.Warn("invalid control message from client", "error", err)
				continue
			}
			if err := m.handleClientControl(ctx, conn, tun, env); err != nil {
				return err
			}

		case websocket.MessageBinary:
			frame, err := protocol.UnmarshalFrame(data)
			if err != nil {
				m.logger.Warn("invalid frame from client", "error", err)
				continue
			}
			m.handleClientFrame(ctx, tun, frame)
		}
	}
}

func (m *Manager) handleClientControl(ctx context.Context, conn *websocket.Conn, tun *Tunnel, env *protocol.Envelope) error {
	switch env.Type {
	case protocol.MsgPing:
		var ping protocol.Ping
		protocol.Unmarshal(env, &ping)
		return writeJSON(ctx, conn, protocol.MsgPong, &protocol.Pong{Timestamp: ping.Timestamp})

	case protocol.MsgPong:
		return nil

	case protocol.MsgDialResult:
		var result protocol.DialResult
		protocol.Unmarshal(env, &result)
		m.logger.Debug("dial result", "stream", result.StreamID, "ok", result.OK)
		if s, ok := tun.GetStream(result.StreamID); ok {
			s.ResultCh <- result.OK
		}
		return nil

	case protocol.MsgData:
		var data protocol.Data
		protocol.Unmarshal(env, &data)
		if s, ok := tun.GetStream(data.StreamID); ok {
			if _, err := s.Conn.Write(data.Payload); err != nil {
				s.Conn.Close()
				tun.RemoveStream(data.StreamID)
			}
		}
		return nil

	case protocol.MsgDataClose:
		var dc protocol.DataClose
		protocol.Unmarshal(env, &dc)
		if s, ok := tun.GetStream(dc.StreamID); ok {
			s.Conn.Close()
			tun.RemoveStream(dc.StreamID)
		}
		return nil

	default:
		m.logger.Debug("unhandled client control message", "type", env.Type)
		return nil
	}
}

func (m *Manager) handleClientFrame(ctx context.Context, tun *Tunnel, frame *protocol.Frame) {
	if m.tunDev == nil {
		m.logger.Debug("received frame but no TUN device", "tunnel", tun.Name, "size", len(frame.Payload))
		return
	}
	// Write IP packet to TUN device
	if _, err := m.tunDev.Write(frame.Payload); err != nil {
		m.logger.Warn("TUN write error", "error", err)
	}
}

// SetTUN sets the TUN device for the manager.
func (m *Manager) SetTUN(dev io.ReadWriteCloser) {
	m.tunDev = dev
}

// ReadTUNLoop reads IP packets from TUN and dispatches them to the correct tunnel.
func (m *Manager) ReadTUNLoop(ctx context.Context) {
	buf := make([]byte, 65536)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := m.tunDev.Read(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			m.logger.Warn("TUN read error", "error", err)
			return
		}

		if n < 20 {
			continue // too short for IP header
		}

		pkt := buf[:n]
		// Extract destination IP from IPv4 header (bytes 16-19)
		version := pkt[0] >> 4
		var dstIP net.IP
		if version == 4 && n >= 20 {
			dstIP = net.IP(pkt[16:20])
		} else if version == 6 && n >= 40 {
			dstIP = net.IP(pkt[24:40])
		} else {
			continue
		}

		// Find tunnel for this destination IP
		m.mu.RLock()
		tun, ok := m.byIP[dstIP.String()]
		m.mu.RUnlock()
		if !ok {
			m.logger.Debug("no tunnel for destination IP", "dst", dstIP)
			continue
		}

		// Send as binary frame
		frame := &protocol.Frame{
			TunnelID: tun.ID,
			Payload:  pkt,
		}
		data := protocol.MarshalFrame(frame)
		if err := tun.Conn.Write(ctx, websocket.MessageBinary, data); err != nil {
			m.logger.Debug("failed to send frame to client", "tunnel", tun.Name, "error", err)
		}
	}
}

// GetTunnel returns a tunnel by name.
func (m *Manager) GetTunnel(name string) (*Tunnel, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tun, ok := m.tunnels[name]
	return tun, ok
}

// ListTunnels returns all active tunnels.
func (m *Manager) ListTunnels() []*Tunnel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tunnels := make([]*Tunnel, 0, len(m.tunnels))
	for _, t := range m.tunnels {
		tunnels = append(tunnels, t)
	}
	return tunnels
}

// DialThrough opens a proxied TCP connection through a tunnel.
// It sends a Dial request to the client and returns after the client responds.
// The caller is responsible for reading/writing the returned Stream and closing it.
func (m *Manager) DialThrough(ctx context.Context, tunnelName string, address string, localConn net.Conn) (*Stream, error) {
	m.mu.RLock()
	tun, ok := m.tunnels[tunnelName]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("tunnel %q not found", tunnelName)
	}

	stream := tun.NewStream(localConn)

	// Send Dial to client
	if err := writeJSON(ctx, tun.Conn, protocol.MsgDial, &protocol.Dial{
		StreamID: stream.ID,
		Address:  address,
	}); err != nil {
		tun.RemoveStream(stream.ID)
		return nil, fmt.Errorf("sending dial: %w", err)
	}

	// Wait for result
	select {
	case ok := <-stream.ResultCh:
		if !ok {
			tun.RemoveStream(stream.ID)
			return nil, fmt.Errorf("remote dial failed")
		}
		return stream, nil
	case <-ctx.Done():
		tun.RemoveStream(stream.ID)
		return nil, ctx.Err()
	}
}

// ForwardStreamToClient reads from localConn and forwards to the client via WebSocket.
func (m *Manager) ForwardStreamToClient(ctx context.Context, tunnelName string, stream *Stream) {
	m.mu.RLock()
	tun, ok := m.tunnels[tunnelName]
	m.mu.RUnlock()
	if !ok {
		return
	}

	buf := make([]byte, 32*1024)
	for {
		n, err := stream.Conn.Read(buf)
		if n > 0 {
			writeJSON(ctx, tun.Conn, protocol.MsgData, &protocol.Data{
				StreamID: stream.ID,
				Payload:  buf[:n],
			})
		}
		if err != nil {
			writeJSON(ctx, tun.Conn, protocol.MsgDataClose, &protocol.DataClose{
				StreamID: stream.ID,
			})
			tun.RemoveStream(stream.ID)
			return
		}
	}
}

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
