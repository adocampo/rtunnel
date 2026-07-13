package protocol

import "encoding/json"

// Marshal creates an Envelope with the given message type and payload.
func Marshal(msgType MessageType, payload interface{}) (*Envelope, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &Envelope{
		Type:    msgType,
		Payload: json.RawMessage(data),
	}, nil
}

// Unmarshal decodes an Envelope's payload into the target struct.
func Unmarshal(env *Envelope, target interface{}) error {
	return json.Unmarshal(env.Payload, target)
}

// EncodeEnvelope serializes an Envelope to JSON bytes.
func EncodeEnvelope(env *Envelope) ([]byte, error) {
	return json.Marshal(env)
}

// DecodeEnvelope deserializes JSON bytes into an Envelope.
func DecodeEnvelope(data []byte) (*Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	return &env, nil
}
