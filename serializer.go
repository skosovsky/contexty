package contexty

import (
	"encoding/json"
)

// MessageSerializer converts messages to bytes for storage adapters.
type MessageSerializer interface {
	Marshal(msg Message) ([]byte, error)
	Unmarshal(data []byte, msg *Message) error
}

// JSONSerializer uses the polymorphic semantic codec.
type JSONSerializer struct {
	Provenance *ProvenanceRegistry
}

// DefaultJSONSerializer returns a serializer with the default provenance registry.
func DefaultJSONSerializer() JSONSerializer {
	return JSONSerializer{Provenance: DefaultProvenanceRegistry()}
}

// Marshal serializes msg into JSON.
func (s JSONSerializer) Marshal(msg Message) ([]byte, error) {
	reg := s.Provenance
	if reg == nil {
		reg = DefaultProvenanceRegistry()
	}
	return MarshalMessageJSON(msg, reg)
}

// Unmarshal deserializes a JSON-encoded message into msg.
func (s JSONSerializer) Unmarshal(data []byte, msg *Message) error {
	reg := s.Provenance
	if reg == nil {
		reg = DefaultProvenanceRegistry()
	}
	m, err := UnmarshalMessageJSON(data, reg)
	if err != nil {
		return err
	}
	*msg = m
	return nil
}

// ConversationCodec serializes full conversation snapshots for storage adapters.
type ConversationCodec struct {
	Provenance *ProvenanceRegistry
}

// conversationWire is the storage envelope for a thread.
type conversationWire struct {
	Version  int64                      `json:"version"`
	Segments map[string]json.RawMessage `json:"segments"`
}

// Encode serializes a snapshot to JSON bytes.
func (c ConversationCodec) Encode(snap ConversationSnapshot) ([]byte, error) {
	reg := c.Provenance
	if reg == nil {
		reg = DefaultProvenanceRegistry()
	}
	wire := conversationWire{
		Version:  snap.Version(),
		Segments: make(map[string]json.RawMessage, len(snap.segments)),
	}
	for name, msgs := range snap.segments {
		b, err := MarshalMessages(msgs, reg)
		if err != nil {
			return nil, err
		}
		wire.Segments[string(name)] = b
	}
	return json.Marshal(wire)
}

// Decode deserializes JSON bytes into a snapshot.
func (c ConversationCodec) Decode(data []byte) (ConversationSnapshot, error) {
	reg := c.Provenance
	if reg == nil {
		reg = DefaultProvenanceRegistry()
	}
	var wire conversationWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return ConversationSnapshot{}, err
	}
	segments := make(map[SegmentName][]Message, len(wire.Segments))
	for name, raw := range wire.Segments {
		msgs, err := UnmarshalMessages(raw, reg)
		if err != nil {
			return ConversationSnapshot{}, err
		}
		segments[SegmentName(name)] = msgs
	}
	return ConversationSnapshot{segments: segments, version: wire.Version}, nil
}

var _ MessageSerializer = JSONSerializer{Provenance: nil}
