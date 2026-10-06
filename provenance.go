package contexty

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"
)

// Provenance is host-owned typed origin metadata for a message.
// ProvenanceType is a stable wire discriminator. CloneProvenance must retain it
// and own every mutable field; implementations must support concurrent reuse
// when their messages are compiled concurrently.
type Provenance interface {
	ProvenanceType() string
	CloneProvenance() Provenance
}

// ProvenanceRegistry decodes provenance payloads by type_id without map[string]any.
type ProvenanceRegistry struct {
	mu        sync.RWMutex
	decoders  map[string]func([]byte) (Provenance, error)
	intrinsic map[string]Descriptor
}

// NewProvenanceRegistry returns an empty registry.
func NewProvenanceRegistry() *ProvenanceRegistry {
	return &ProvenanceRegistry{
		mu:        sync.RWMutex{},
		decoders:  make(map[string]func([]byte) (Provenance, error)),
		intrinsic: nil,
	}
}

func (r *ProvenanceRegistry) snapshot() *ProvenanceRegistry {
	if r == nil {
		return DefaultProvenanceRegistry()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return &ProvenanceRegistry{mu: sync.RWMutex{}, decoders: maps.Clone(r.decoders), intrinsic: maps.Clone(r.intrinsic)}
}

// Register adds a decoder for typeID. Panics on duplicate registration.
func (r *ProvenanceRegistry) Register(typeID string, decode func([]byte) (Provenance, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.decoders[typeID]; exists {
		panic(fmt.Sprintf("contexty: duplicate provenance type %q", typeID))
	}
	r.decoders[typeID] = decode
}

// Decode restores a concrete Provenance from wire JSON.
func (r *ProvenanceRegistry) Decode(data []byte) (Provenance, error) {
	if len(data) == 0 || string(data) == jsonNullLiteral {
		return nil, errProvenanceNil
	}
	var wire provenanceWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("contexty: provenance decode: %w", err)
	}
	if wire.TypeID == "" {
		return nil, errors.New("contexty: provenance decode: missing type_id")
	}
	r.mu.RLock()
	decode, ok := r.decoders[wire.TypeID]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("contexty: provenance decode: unregistered type_id %q", wire.TypeID)
	}
	p, err := decode(wire.Payload)
	if err != nil {
		return nil, fmt.Errorf("contexty: provenance decode %q: %w", wire.TypeID, err)
	}
	return p, nil
}

// errProvenanceNil indicates absent provenance in wire JSON.
var errProvenanceNil = errors.New("contexty: provenance nil")

// EncodeProvenance serializes provenance with type discriminator.
func EncodeProvenance(p Provenance) ([]byte, error) {
	if p == nil {
		return []byte(jsonNullLiteral), nil
	}
	payload, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("contexty: provenance encode: %w", err)
	}
	return json.Marshal(provenanceWire{TypeID: p.ProvenanceType(), Payload: payload})
}

type provenanceWire struct {
	TypeID  string          `json:"type_id"`
	Payload json.RawMessage `json:"payload"`
}

// UserProvenance marks a message originating from an end user channel.
type UserProvenance struct {
	Channel string `json:"channel"`
	UserID  string `json:"user_id,omitempty"`
}

func (UserProvenance) ProvenanceType() string { return "user" }

func (p UserProvenance) CloneProvenance() Provenance { return p }

// SystemProvenance marks system-generated messages.
type SystemProvenance struct {
	Component string `json:"component"`
}

func (SystemProvenance) ProvenanceType() string { return "system" }

func (p SystemProvenance) CloneProvenance() Provenance { return p }

// DefaultProvenanceRegistry registers built-in provenance types.
func DefaultProvenanceRegistry() *ProvenanceRegistry {
	r := NewProvenanceRegistry()
	r.Register("user", func(b []byte) (Provenance, error) {
		var p UserProvenance
		if err := json.Unmarshal(b, &p); err != nil {
			return nil, err
		}
		return p, nil
	})
	r.Register("system", func(b []byte) (Provenance, error) {
		var p SystemProvenance
		if err := json.Unmarshal(b, &p); err != nil {
			return nil, err
		}
		return p, nil
	})
	r.intrinsic = map[string]Descriptor{
		"user":   {ID: "contexty/codec/user-provenance", Revision: "contract"},
		"system": {ID: "contexty/codec/system-provenance", Revision: "contract"},
	}
	return r
}
