package client

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"

	"github.com/malevolent/rtunnel/pkg/protocol"
	"nhooyr.io/websocket"
)

// Handler manages proxying connections to local services on the client side.
type Handler struct {
	logger  *slog.Logger
	mu      sync.RWMutex
	streams map[uint32]*stream
}

type stream struct {
	conn   net.Conn
	cancel context.CancelFunc
}

// NewHandler creates a new client-side connection handler.
func NewHandler(logger *slog.Logger) *Handler {
	return &Handler{
		logger:  logger,
		streams: make(map[uint32]*stream),
	}
}

// HandleDial opens a local TCP connection for the given dial request and
// starts bidirectional forwarding over the WebSocket.
func (h *Handler) HandleDial(ctx context.Context, wsConn *websocket.Conn, dial *protocol.Dial) {
	h.logger.Info("dialing local service", "stream", dial.StreamID, "address", dial.Address)

	// Connect to the local service
	conn, err := net.DialTimeout("tcp", dial.Address, 10*1e9) // 10s timeout
	if err != nil {
		h.logger.Warn("dial failed", "address", dial.Address, "error", err)
		h.sendDialResult(ctx, wsConn, dial.StreamID, false, err.Error())
		return
	}

	h.sendDialResult(ctx, wsConn, dial.StreamID, true, "")

	streamCtx, cancel := context.WithCancel(ctx)
	s := &stream{conn: conn, cancel: cancel}

	h.mu.Lock()
	h.streams[dial.StreamID] = s
	h.mu.Unlock()

	// Start reading from local connection and forwarding to WebSocket
	go h.localToWS(streamCtx, wsConn, conn, dial.StreamID)

	// The WS → local direction is handled by HandleData calls
	<-streamCtx.Done()

	conn.Close()
	h.mu.Lock()
	delete(h.streams, dial.StreamID)
	h.mu.Unlock()
}

// HandleData forwards data from the WebSocket to the local connection.
func (h *Handler) HandleData(data *protocol.Data) {
	h.mu.RLock()
	s, ok := h.streams[data.StreamID]
	h.mu.RUnlock()

	if !ok {
		h.logger.Debug("data for unknown stream", "stream", data.StreamID)
		return
	}

	if _, err := s.conn.Write(data.Payload); err != nil {
		h.logger.Debug("write to local failed", "stream", data.StreamID, "error", err)
		s.cancel()
	}
}

// HandleDataClose closes the local connection for a stream.
func (h *Handler) HandleDataClose(streamID uint32) {
	h.mu.RLock()
	s, ok := h.streams[streamID]
	h.mu.RUnlock()

	if ok {
		s.cancel()
	}
}

func (h *Handler) localToWS(ctx context.Context, wsConn *websocket.Conn, localConn net.Conn, streamID uint32) {
	buf := make([]byte, 32*1024)
	for {
		n, err := localConn.Read(buf)
		if n > 0 {
			data := &protocol.Data{
				StreamID: streamID,
				Payload:  buf[:n],
			}
			env, _ := protocol.Marshal(protocol.MsgData, data)
			envBytes, _ := protocol.EncodeEnvelope(env)
			if writeErr := wsConn.Write(ctx, websocket.MessageText, envBytes); writeErr != nil {
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				h.logger.Debug("local read error", "stream", streamID, "error", err)
			}
			// Signal stream close
			close := &protocol.DataClose{StreamID: streamID}
			env, _ := protocol.Marshal(protocol.MsgDataClose, close)
			envBytes, _ := protocol.EncodeEnvelope(env)
			wsConn.Write(ctx, websocket.MessageText, envBytes)
			return
		}
	}
}

func (h *Handler) sendDialResult(ctx context.Context, wsConn *websocket.Conn, streamID uint32, ok bool, errMsg string) {
	result := &protocol.DialResult{
		StreamID: streamID,
		OK:       ok,
		Error:    errMsg,
	}
	env, err := protocol.Marshal(protocol.MsgDialResult, result)
	if err != nil {
		return
	}
	data, err := protocol.EncodeEnvelope(env)
	if err != nil {
		return
	}
	wsConn.Write(ctx, websocket.MessageText, data)
}

// Close closes all active streams.
func (h *Handler) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.streams {
		s.cancel()
	}
}

// ActiveStreams returns the count of active streams.
func (h *Handler) ActiveStreams() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.streams)
}

// PortMapper maps exposed ports to their local dial addresses.
type PortMapper struct {
	ports map[int]string // port -> dial address (e.g., 22 -> "127.0.0.1:22")
}

// NewPortMapper creates a mapper for the given exposed ports.
func NewPortMapper(ports []int) *PortMapper {
	m := &PortMapper{ports: make(map[int]string, len(ports))}
	for _, p := range ports {
		m.ports[p] = fmt.Sprintf("127.0.0.1:%d", p)
	}
	return m
}

// Resolve returns the local dial address for a target address.
// If the target port matches an exposed port, it maps to localhost.
func (pm *PortMapper) Resolve(address string) (string, bool) {
	_, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return "", false
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	if local, ok := pm.ports[port]; ok {
		return local, true
	}
	return "", false
}
