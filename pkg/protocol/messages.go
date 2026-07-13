package protocol

import "encoding/json"

// MessageType identifies the type of control message.
type MessageType string

const (
	// Handshake messages
	MsgHello         MessageType = "hello"
	MsgChallenge     MessageType = "challenge"
	MsgChallengeResp MessageType = "challenge_response"
	MsgAuthOK        MessageType = "auth_ok"
	MsgAuthFailed    MessageType = "auth_failed"

	// Tunnel management
	MsgTunnelRequest MessageType = "tunnel_request"
	MsgTunnelReady   MessageType = "tunnel_ready"
	MsgTunnelClose   MessageType = "tunnel_close"

	// Data forwarding (used in SOCKS/proxy mode)
	MsgDial       MessageType = "dial"
	MsgDialResult MessageType = "dial_result"
	MsgData       MessageType = "data"
	MsgDataClose  MessageType = "data_close"

	// Keepalive
	MsgPing MessageType = "ping"
	MsgPong MessageType = "pong"

	// Errors
	MsgError MessageType = "error"
)

// Envelope wraps all control messages with a type discriminator.
type Envelope struct {
	Type    MessageType     `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Hello is sent by the client upon connection.
type Hello struct {
	Name          string   `json:"name"`
	Version       string   `json:"version"`
	ExposePorts   []int    `json:"expose_ports,omitempty"`
	ExposeSubnets []string `json:"expose_subnets,omitempty"`
}

// Challenge is sent by the server after Hello.
type Challenge struct {
	Nonce     []byte `json:"nonce"`      // random bytes to sign
	SessionID string `json:"session_id"` // unique session identifier
}

// ChallengeResponse is sent by the client with the signed nonce.
type ChallengeResponse struct {
	PublicKey []byte `json:"public_key"` // SSH public key (marshaled)
	Signature []byte `json:"signature"`  // signature of the nonce
}

// AuthOK confirms successful authentication.
type AuthOK struct {
	SessionID string `json:"session_id"`
}

// AuthFailed indicates authentication failure.
type AuthFailed struct {
	Reason string `json:"reason"`
}

// TunnelRequest is sent by the server to configure the tunnel.
type TunnelRequest struct {
	AssignedIP string `json:"assigned_ip"` // IP assigned to this tunnel (e.g., "10.99.0.2/30")
	ServerIP   string `json:"server_ip"`   // server-side tunnel IP (e.g., "10.99.0.1/30")
	Mode       string `json:"mode"`        // "tun" or "socks"
}

// TunnelReady confirms the tunnel is operational.
type TunnelReady struct {
	AssignedIP string `json:"assigned_ip"`
}

// TunnelClose signals tunnel teardown.
type TunnelClose struct {
	Reason string `json:"reason,omitempty"`
}

// Dial requests the client to open a TCP connection.
type Dial struct {
	StreamID uint32 `json:"stream_id"`
	Address  string `json:"address"` // host:port
}

// DialResult is the client's response to a Dial request.
type DialResult struct {
	StreamID uint32 `json:"stream_id"`
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
}

// Data carries stream data (for SOCKS/proxy mode).
type Data struct {
	StreamID uint32 `json:"stream_id"`
	Payload  []byte `json:"payload"`
}

// DataClose signals a stream has ended.
type DataClose struct {
	StreamID uint32 `json:"stream_id"`
}

// Ping/Pong for keepalive.
type Ping struct {
	Timestamp int64 `json:"ts"`
}

type Pong struct {
	Timestamp int64 `json:"ts"`
}

// Error carries error information.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
