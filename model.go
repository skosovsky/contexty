package contexty

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const jsonNullLiteral = "null"

// Role identifies the provider-facing role of a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is a semantic AST node — no transport prefixes in text fields.
type Message struct {
	ID          string          `json:"id,omitempty"`
	Role        Role            `json:"role"`
	Actor       *Actor          `json:"actor,omitempty"`
	Parts       []ContentPart   `json:"parts"`
	Annotations Annotations     `json:"annotations"`
	SourceRefs  []SourceRef     `json:"source_refs,omitempty"`
	Extensions  []Extension     `json:"-"`
	Origin      *MessageOrigin  `json:"origin,omitempty"`
	LLMCache    *CachePolicyRef `json:"llm_cache,omitempty"`
	Provenance  Provenance      `json:"-"`
}

// Clone returns a deep copy of the message.
func (m Message) Clone() Message {
	cloned := Message{
		ID:          m.ID,
		Role:        m.Role,
		Actor:       m.Actor.Clone(),
		Annotations: m.Annotations.Clone(),
		SourceRefs:  cloneSourceRefs(m.SourceRefs),
		Extensions:  cloneExtensions(m.Extensions),
		Origin:      m.Origin.Clone(),
		LLMCache:    m.LLMCache.Clone(),
	}
	if len(m.Parts) > 0 {
		cloned.Parts = make([]ContentPart, len(m.Parts))
		for i, p := range m.Parts {
			cloned.Parts[i] = p.clonePart()
		}
	}
	if m.Provenance != nil {
		cloned.Provenance = m.Provenance.cloneProvenance()
	}
	return cloned
}

// TextContent concatenates TextPart bodies for convenience.
func (m Message) TextContent() string {
	var out strings.Builder
	for _, p := range m.Parts {
		if t, ok := p.(TextPart); ok {
			out.WriteString(t.Text)
		}
	}
	return out.String()
}

// HasToolCalls reports whether the message contains ToolCallPart nodes.
func (m Message) HasToolCalls() bool {
	for _, p := range m.Parts {
		if _, ok := p.(ToolCallPart); ok {
			return true
		}
	}
	return false
}

// ToolCallParts returns all tool call parts in order.
func (m Message) ToolCallParts() []ToolCallPart {
	var out []ToolCallPart
	for _, p := range m.Parts {
		if tc, ok := p.(ToolCallPart); ok {
			out = append(out, tc)
		}
	}
	return out
}

// ToolResultParts returns all tool result parts in order.
func (m Message) ToolResultParts() []ToolResultPart {
	var out []ToolResultPart
	for _, p := range m.Parts {
		if tr, ok := p.(ToolResultPart); ok {
			out = append(out, tr)
		}
	}
	return out
}

// messageWire is the JSON transport envelope for Message.
type messageWire struct {
	ID          string          `json:"id,omitempty"`
	Role        Role            `json:"role"`
	Actor       *Actor          `json:"actor,omitempty"`
	Parts       json.RawMessage `json:"parts"`
	Annotations Annotations     `json:"annotations"`
	SourceRefs  []SourceRef     `json:"source_refs,omitempty"`
	Extensions  json.RawMessage `json:"extensions,omitempty"`
	Origin      *MessageOrigin  `json:"origin,omitempty"`
	LLMCache    *CachePolicyRef `json:"llm_cache,omitempty"`
	Provenance  json.RawMessage `json:"provenance,omitempty"`
}

// MessageCodec carries registries required by message-level JSON helpers.
// Host-owned typed extensions require an ExtensionRegistry with matching decoders.
type MessageCodec struct {
	Provenance *ProvenanceRegistry
	Extensions *ExtensionRegistry
}

// DefaultMessageCodec returns the default message codec configuration.
func DefaultMessageCodec() MessageCodec {
	return MessageCodec{Provenance: DefaultProvenanceRegistry(), Extensions: NewExtensionRegistry()}
}

// MarshalMessageJSON serializes a message using the polymorphic codec.
func MarshalMessageJSON(m Message, codec MessageCodec) ([]byte, error) {
	return marshalMessageJSONWithRegistries(m, codec.Provenance, codec.Extensions)
}

func marshalMessageJSONWithRegistries(
	m Message,
	_ *ProvenanceRegistry,
	extRegistry *ExtensionRegistry,
) ([]byte, error) {
	if err := validateOpaqueMessageCodec(m, extRegistry); err != nil {
		return nil, err
	}
	partsJSON, err := MarshalParts(m.Parts)
	if err != nil {
		return nil, err
	}
	provJSON, err := EncodeProvenance(m.Provenance)
	if err != nil {
		return nil, err
	}
	extJSON, err := encodeExtensions(m.Extensions)
	if err != nil {
		return nil, err
	}
	wire := messageWire{
		ID:          m.ID,
		Role:        m.Role,
		Actor:       m.Actor.Clone(),
		Parts:       partsJSON,
		Annotations: m.Annotations,
		SourceRefs:  cloneSourceRefs(m.SourceRefs),
		Extensions:  extJSON,
		Origin:      m.Origin.Clone(),
		LLMCache:    m.LLMCache.Clone(),
		Provenance:  provJSON,
	}
	return json.Marshal(wire)
}

// UnmarshalMessageJSON deserializes a message using the polymorphic codec.
func UnmarshalMessageJSON(data []byte, codec MessageCodec) (Message, error) {
	return unmarshalMessageJSONWithRegistries(data, codec.Provenance, codec.Extensions)
}

func unmarshalMessageJSONWithRegistries(
	data []byte,
	reg *ProvenanceRegistry,
	extReg *ExtensionRegistry,
) (Message, error) {
	var wire messageWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return Message{}, fmt.Errorf("contexty: unmarshal message: %w", err)
	}
	parts, err := UnmarshalParts(wire.Parts)
	if err != nil {
		return Message{}, err
	}
	var prov Provenance
	if len(wire.Provenance) > 0 && string(wire.Provenance) != jsonNullLiteral {
		if reg == nil {
			return Message{}, errors.New("contexty: unmarshal message: provenance present but registry is nil")
		}
		prov, err = reg.Decode(wire.Provenance)
		if errors.Is(err, errProvenanceNil) {
			prov = nil
		} else if err != nil {
			return Message{}, err
		}
	}
	extensions, err := decodeExtensions(wire.Extensions, extReg)
	if err != nil {
		return Message{}, err
	}
	return Message{
		ID:          wire.ID,
		Role:        wire.Role,
		Actor:       wire.Actor.Clone(),
		Parts:       parts,
		Annotations: wire.Annotations,
		SourceRefs:  cloneSourceRefs(wire.SourceRefs),
		Extensions:  extensions,
		Origin:      wire.Origin.Clone(),
		LLMCache:    wire.LLMCache.Clone(),
		Provenance:  prov,
	}, nil
}

// MarshalMessages serializes a slice of messages.
func MarshalMessages(msgs []Message, codec MessageCodec) ([]byte, error) {
	return marshalMessagesWithRegistries(msgs, codec.Provenance, codec.Extensions)
}

func marshalMessagesWithRegistries(
	msgs []Message,
	reg *ProvenanceRegistry,
	extReg *ExtensionRegistry,
) ([]byte, error) {
	wires := make([]json.RawMessage, len(msgs))
	for i, m := range msgs {
		b, err := marshalMessageJSONWithRegistries(m, reg, extReg)
		if err != nil {
			return nil, err
		}
		wires[i] = b
	}
	return json.Marshal(wires)
}

// UnmarshalMessages deserializes a slice of messages.
func UnmarshalMessages(data []byte, codec MessageCodec) ([]Message, error) {
	return unmarshalMessagesWithRegistries(data, codec.Provenance, codec.Extensions)
}

func unmarshalMessagesWithRegistries(
	data []byte,
	reg *ProvenanceRegistry,
	extReg *ExtensionRegistry,
) ([]Message, error) {
	if len(data) == 0 || string(data) == jsonNullLiteral {
		return nil, nil
	}
	var wires []json.RawMessage
	if err := json.Unmarshal(data, &wires); err != nil {
		return nil, fmt.Errorf("contexty: unmarshal messages: %w", err)
	}
	out := make([]Message, len(wires))
	for i, w := range wires {
		m, err := unmarshalMessageJSONWithRegistries(w, reg, extReg)
		if err != nil {
			return nil, fmt.Errorf("contexty: unmarshal messages index %d: %w", i, err)
		}
		out[i] = m
	}
	return out, nil
}
