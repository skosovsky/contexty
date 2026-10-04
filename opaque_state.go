package contexty

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

const OpaqueStateExtensionType = "contexty.opaque_state"

var (
	ErrInvalidOpaqueState       = errors.New("contexty: invalid opaque state")
	ErrMissingOpaqueStateCodec  = errors.New("contexty: missing opaque state codec")
	ErrMissingOpaqueStatePolicy = errors.New("contexty: missing opaque state policy")
	ErrOpaqueStateInvalidated   = errors.New("contexty: opaque state invalidated")
)

// OpaquePlacement places state after a content part; -1 means before the first part.
// Multiple states at one position retain their Extensions order.
type OpaquePlacement struct {
	AfterPart int `json:"after_part"`
}

// OpaqueBinding declares host dependencies; core never infers provider rules.
// Prefix, when present, is the exact ordered context beginning through Boundary,
// which must precede the owning message. Content after Boundary is outside that scope.
type OpaqueBinding struct {
	Profile  Descriptor   `json:"profile"`
	Required []ContentRef `json:"required,omitempty"`
	Prefix   []ContentRef `json:"prefix,omitempty"`
	Boundary string       `json:"boundary,omitempty"`
}

// OpaqueState is the sole envelope for host-owned external model state.
// Payload is BYOT; its decoder must be registered with RegisterOpaquePayload.
type OpaqueState struct {
	ID        string          `json:"id"`
	Codec     Descriptor      `json:"codec"`
	Payload   Extension       `json:"-"`
	Placement OpaquePlacement `json:"placement"`
	Binding   OpaqueBinding   `json:"binding"`
}

func (OpaqueState) ExtensionType() string { return OpaqueStateExtensionType }
func (s OpaqueState) CloneExtension() Extension {
	s.Binding.Required = slices.Clone(s.Binding.Required)
	s.Binding.Prefix = slices.Clone(s.Binding.Prefix)
	if !nilInterfaceValue(s.Payload) {
		s.Payload = s.Payload.CloneExtension()
	}
	return s
}

func (s OpaqueState) MarshalJSON() ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	payload, err := EncodeExtension(s.Payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(
		opaqueStateWire{ID: s.ID, Codec: s.Codec, Payload: payload, Placement: s.Placement, Binding: s.Binding},
	)
}

type opaqueStateWire struct {
	ID        string          `json:"id"`
	Codec     Descriptor      `json:"codec"`
	Payload   json.RawMessage `json:"payload"`
	Placement OpaquePlacement `json:"placement"`
	Binding   OpaqueBinding   `json:"binding"`
}

func opaqueStateFromExtension(extension Extension) (OpaqueState, bool) {
	switch value := extension.(type) {
	case OpaqueState:
		return value, true
	case *OpaqueState:
		if value != nil {
			return *value, true
		}
	}
	return OpaqueState{}, false
}

func (s OpaqueState) validate() error {
	if s.ID == "" || nilInterfaceValue(s.Payload) || s.Payload.ExtensionType() == "" ||
		s.Payload.ExtensionType() == OpaqueStateExtensionType ||
		s.Placement.AfterPart < -1 {
		return ErrInvalidOpaqueState
	}
	if s.Codec.Validate() != nil || s.Binding.Profile.Validate() != nil {
		return ErrInvalidOpaqueState
	}
	if (len(s.Binding.Prefix) == 0) != (s.Binding.Boundary == "") {
		return ErrInvalidOpaqueState
	}
	for _, refs := range [][]ContentRef{s.Binding.Required, s.Binding.Prefix} {
		seen := make(map[ContentRef]bool)
		for _, ref := range refs {
			if ref.Validate() != nil || ref.Occurrence != "" || seen[ref] {
				return ErrInvalidOpaqueState
			}
			seen[ref] = true
		}
	}
	if len(s.Binding.Prefix) > 0 && s.Binding.Prefix[len(s.Binding.Prefix)-1].ID != s.Binding.Boundary {
		return ErrInvalidOpaqueState
	}
	return nil
}

func (r *ExtensionRegistry) decodeOpaqueState(data []byte) (Extension, error) {
	var wire opaqueStateWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, err
	}
	payload, err := r.Decode(wire.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMissingOpaqueStateCodec, err)
	}
	if err = validateOpaqueDecodedPayload(wire.Payload, payload); err != nil {
		return nil, err
	}
	state := OpaqueState{
		ID:        wire.ID,
		Codec:     wire.Codec,
		Payload:   payload,
		Placement: wire.Placement,
		Binding:   wire.Binding,
	}
	if err = state.validate(); err != nil {
		return nil, err
	}
	if err = validateOpaquePayloadCodec(state, r); err != nil {
		return nil, err
	}
	return state, nil
}

func validateOpaquePayloadCodec(state OpaqueState, registry *ExtensionRegistry) error {
	if registry == nil {
		return ErrMissingOpaqueStateCodec
	}
	registry.mu.RLock()
	identity, found := registry.opaqueCodecs[state.Payload.ExtensionType()]
	registry.mu.RUnlock()
	if !found || identity != state.Codec {
		return ErrMissingOpaqueStateCodec
	}
	return nil
}

// ValidateOpaqueState checks exact declared context dependencies and codec identity.
// It never calls an adapter policy, rewrites payload bytes, or reveals dependencies.
func ValidateOpaqueState(messages []Message, codec JSONSerializer, profile Descriptor) error {
	if !hasOpaqueState(messages) {
		return nil
	}
	if err := validateUniqueMessageIDs(messages); err != nil {
		return ErrInvalidOpaqueState
	}
	inventory, err := opaqueStateInventory(messages)
	if err != nil {
		return err
	}
	for _, origin := range inventory {
		if err := validateOneOpaqueState(messages, origin.message.ID, origin.state, codec, profile); err != nil {
			return err
		}
	}
	return nil
}

func hasOpaqueState(messages []Message) bool {
	for _, message := range messages {
		for _, extension := range message.Extensions {
			if !nilInterfaceValue(extension) && extension.ExtensionType() == OpaqueStateExtensionType {
				return true
			}
		}
	}
	return false
}

func validateOneOpaqueState(
	messages []Message,
	ownerID string,
	state OpaqueState,
	codec JSONSerializer,
	profile Descriptor,
) error {
	if err := state.validate(); err != nil {
		return err
	}
	if err := validateOpaquePayloadCodec(state, codec.Extensions); err != nil {
		return err
	}
	if state.Binding.Profile != profile {
		return ErrOpaqueStateInvalidated
	}
	refs, owner, err := opaqueContextRefs(messages, ownerID, state.Placement, codec)
	if err != nil {
		return err
	}
	return validateOpaqueBinding(state.Binding, owner, refs)
}

func opaqueContextRefs(
	messages []Message,
	ownerID string,
	placement OpaquePlacement,
	codec JSONSerializer,
) ([]ContentRef, int, error) {
	refs := make([]ContentRef, len(messages))
	owner := -1
	for i, message := range messages {
		ref, err := MessageContentRef(message, codec)
		if err != nil {
			return nil, 0, err
		}
		refs[i] = ref
		if message.ID == ownerID {
			owner = i
			if placement.AfterPart >= len(message.Parts) {
				return nil, 0, ErrInvalidOpaqueState
			}
		}
	}
	if owner < 0 {
		return nil, 0, ErrOpaqueStateInvalidated
	}
	return refs, owner, nil
}

func validateOpaqueBinding(binding OpaqueBinding, owner int, refs []ContentRef) error {
	byID := make(map[string]ContentRef, len(refs))
	for _, ref := range refs {
		byID[ref.ID] = ref
	}
	for _, required := range binding.Required {
		if required.ID == refs[owner].ID || byID[required.ID] != required {
			return ErrOpaqueStateInvalidated
		}
	}
	if len(binding.Prefix) == 0 {
		return nil
	}
	for i := range owner {
		if refs[i].ID == binding.Boundary {
			if slices.Equal(binding.Prefix, refs[:i+1]) {
				return nil
			}
			return ErrOpaqueStateInvalidated
		}
	}
	return ErrOpaqueStateInvalidated
}

func validateOpaqueDecodedPayload(original []byte, payload Extension) error {
	restored, err := EncodeExtension(payload)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrMissingOpaqueStateCodec, err)
	}
	originalDigest, err := canonicalJSONDigest(original)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrMissingOpaqueStateCodec, err)
	}
	restoredDigest, err := canonicalJSONDigest(restored)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrMissingOpaqueStateCodec, err)
	}
	if originalDigest != restoredDigest {
		return ErrMissingOpaqueStateCodec
	}
	return nil
}
