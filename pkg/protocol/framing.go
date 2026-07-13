package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Frame format for binary tunnel data (IP packets over WebSocket):
//
//	┌──────────┬──────────┬───────────────────┐
//	│ TunnelID │  Length  │     Payload       │
//	│  4 bytes │  4 bytes │  Length bytes      │
//	└──────────┴──────────┴───────────────────┘
//
// TunnelID identifies which tunnel the packet belongs to (for multi-tunnel).
// Length is the payload size in bytes.
// Payload is the raw IP packet (for TUN mode) or stream data (for proxy mode).

const (
	FrameHeaderSize = 8 // 4 (tunnel ID) + 4 (length)
	MaxFrameSize    = 65536
)

// Frame represents a binary data frame.
type Frame struct {
	TunnelID uint32
	Payload  []byte
}

// MarshalFrame serializes a frame into a byte slice.
func MarshalFrame(f *Frame) []byte {
	buf := make([]byte, FrameHeaderSize+len(f.Payload))
	binary.BigEndian.PutUint32(buf[0:4], f.TunnelID)
	binary.BigEndian.PutUint32(buf[4:8], uint32(len(f.Payload)))
	copy(buf[FrameHeaderSize:], f.Payload)
	return buf
}

// UnmarshalFrame deserializes a frame from a byte slice.
func UnmarshalFrame(data []byte) (*Frame, error) {
	if len(data) < FrameHeaderSize {
		return nil, fmt.Errorf("frame too short: %d bytes", len(data))
	}

	tunnelID := binary.BigEndian.Uint32(data[0:4])
	length := binary.BigEndian.Uint32(data[4:8])

	if length > MaxFrameSize {
		return nil, fmt.Errorf("frame payload too large: %d bytes", length)
	}

	if uint32(len(data)-FrameHeaderSize) < length {
		return nil, fmt.Errorf("frame truncated: expected %d payload bytes, got %d", length, len(data)-FrameHeaderSize)
	}

	return &Frame{
		TunnelID: tunnelID,
		Payload:  data[FrameHeaderSize : FrameHeaderSize+length],
	}, nil
}

// ReadFrame reads a single frame from a reader.
func ReadFrame(r io.Reader) (*Frame, error) {
	header := make([]byte, FrameHeaderSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	tunnelID := binary.BigEndian.Uint32(header[0:4])
	length := binary.BigEndian.Uint32(header[4:8])

	if length > MaxFrameSize {
		return nil, fmt.Errorf("frame payload too large: %d bytes", length)
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}

	return &Frame{
		TunnelID: tunnelID,
		Payload:  payload,
	}, nil
}

// WriteFrame writes a frame to a writer.
func WriteFrame(w io.Writer, f *Frame) error {
	data := MarshalFrame(f)
	_, err := w.Write(data)
	return err
}
