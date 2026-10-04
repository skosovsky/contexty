package contexty

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"
)

// Extension is host-owned metadata with an explicit codec identity.
type Extension interface {
	ExtensionType() string
	CloneExtension() Extension
}

// ExtensionRegistry decodes host-owned typed extensions.
type ExtensionRegistry struct {
	mu           sync.RWMutex
	decoders     map[string]func([]byte) (Extension, error)
	opaqueCodecs map[string]Descriptor
}

// NewExtensionRegistry returns an empty extension registry.
func NewExtensionRegistry() *ExtensionRegistry {
	return &ExtensionRegistry{
		mu:           sync.RWMutex{},
		decoders:     make(map[string]func([]byte) (Extension, error)),
		opaqueCodecs: make(map[string]Descriptor),
	}
}

func (r *ExtensionRegistry) snapshot() *ExtensionRegistry {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return &ExtensionRegistry{
		mu:           sync.RWMutex{},
		decoders:     maps.Clone(r.decoders),
		opaqueCodecs: maps.Clone(r.opaqueCodecs),
	}
}

// Register adds a decoder for extension typeID. It panics on duplicates.
func (r *ExtensionRegistry) Register(typeID string, decode func([]byte) (Extension, error)) {
	if typeID == OpaqueStateExtensionType {
		panic("contexty: reserved opaque envelope type")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.decoders[typeID]; exists {
		panic(fmt.Sprintf("contexty: duplicate extension type %q", typeID))
	}
	r.decoders[typeID] = decode
}

// Decode restores one extension from wire JSON.
func (r *ExtensionRegistry) Decode(data []byte) (Extension, error) {
	if len(data) == 0 || string(data) == jsonNullLiteral {
		return nil, errExtensionNil
	}
	var wire extensionWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("contexty: extension decode: %w", err)
	}
	if wire.TypeID == "" {
		return nil, errors.New("contexty: extension decode: missing type_id")
	}
	if wire.TypeID == OpaqueStateExtensionType {
		return r.decodeOpaqueState(wire.Payload)
	}
	r.mu.RLock()
	decode, ok := r.decoders[wire.TypeID]
	r.mu.RUnlock()
	if !ok || decode == nil {
		return nil, fmt.Errorf("contexty: extension decode: unregistered type_id %q", wire.TypeID)
	}
	ext, err := decode(wire.Payload)
	if err != nil {
		return nil, fmt.Errorf("contexty: extension decode %q: %w", wire.TypeID, err)
	}
	if nilInterfaceValue(ext) || ext.ExtensionType() != wire.TypeID {
		return nil, fmt.Errorf("contexty: extension decode %q: invalid typed result", wire.TypeID)
	}
	return ext, nil
}

var errExtensionNil = errors.New("contexty: extension nil")

type extensionWire struct {
	TypeID  string          `json:"type_id"`
	Payload json.RawMessage `json:"payload"`
}

// EncodeExtension serializes a typed extension.
func EncodeExtension(ext Extension) ([]byte, error) {
	if nilInterfaceValue(ext) {
		return []byte(jsonNullLiteral), nil
	}
	if ext.ExtensionType() == OpaqueStateExtensionType {
		if _, ok := opaqueStateFromExtension(ext); !ok {
			return nil, ErrInvalidOpaqueState
		}
	}
	payload, err := json.Marshal(ext)
	if err != nil {
		return nil, fmt.Errorf("contexty: extension encode: %w", err)
	}
	return json.Marshal(extensionWire{TypeID: ext.ExtensionType(), Payload: payload})
}

func encodeExtensions(exts []Extension) ([]byte, error) {
	if len(exts) == 0 {
		return nil, nil
	}
	wires := make([]json.RawMessage, len(exts))
	for i, ext := range exts {
		b, err := EncodeExtension(ext)
		if err != nil {
			return nil, err
		}
		wires[i] = b
	}
	return json.Marshal(wires)
}

func decodeExtensions(data []byte, reg *ExtensionRegistry) ([]Extension, error) {
	if len(data) == 0 || string(data) == jsonNullLiteral {
		return nil, nil
	}
	if reg == nil {
		reg = NewExtensionRegistry()
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, fmt.Errorf("contexty: extensions decode: %w", err)
	}
	out := make([]Extension, len(raws))
	for i, raw := range raws {
		ext, err := reg.Decode(raw)
		if errors.Is(err, errExtensionNil) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("contexty: extensions decode index %d: %w", i, err)
		}
		out[i] = ext
	}
	return out, nil
}

func cloneExtensions(exts []Extension) []Extension {
	if len(exts) == 0 {
		return nil
	}
	out := make([]Extension, len(exts))
	for i, ext := range exts {
		if !nilInterfaceValue(ext) {
			out[i] = ext.CloneExtension()
		}
	}
	return out
}

// RegisterOpaquePayload pins a host payload decoder to its explicit encoding identity.
func (r *ExtensionRegistry) RegisterOpaquePayload(
	typeID string,
	identity Descriptor,
	decode func([]byte) (Extension, error),
) {
	if typeID == "" || typeID == OpaqueStateExtensionType || identity.Validate() != nil || decode == nil {
		panic("contexty: invalid opaque payload registration")
	}
	r.Register(typeID, decode)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.opaqueCodecs == nil {
		r.opaqueCodecs = make(map[string]Descriptor)
	}
	r.opaqueCodecs[typeID] = identity
}
