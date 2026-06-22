package contexty

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ArtifactCodecDescriptor describes a host-owned artifact schema without binding
// contexty to the host domain.
type ArtifactCodecDescriptor[T any] struct {
	TypeID      string
	Kind        ArtifactKind
	Render      func(T) string
	Lifecycle   ArtifactLifecycle
	BoundTurnID string
	OwnerRef    *SourceRef
	SourceRefs  []SourceRef
	MergePolicy MergePolicy
	Budget      *ArtifactBudgetPolicy
	Persistence ArtifactPersistencePolicy
}

// TypedArtifactCodec decodes one artifact schema.
type TypedArtifactCodec interface {
	TypeID() string
	DecodeArtifact(artifact ContextArtifact) (any, error)
}

type typedArtifactCodec[T any] struct {
	desc ArtifactCodecDescriptor[T]
}

// NewTypedArtifactCodec creates a registry entry for a typed artifact schema.
func NewTypedArtifactCodec[T any](desc ArtifactCodecDescriptor[T]) TypedArtifactCodec {
	return typedArtifactCodec[T]{desc: desc}
}

func (c typedArtifactCodec[T]) TypeID() string { return c.desc.TypeID }

func (c typedArtifactCodec[T]) DecodeArtifact(artifact ContextArtifact) (any, error) {
	return DecodeTypedArtifact[T](artifact, c.desc)
}

// ArtifactCodecRegistry stores host-provided artifact codecs.
type ArtifactCodecRegistry struct {
	codecs map[string]TypedArtifactCodec
}

// NewArtifactCodecRegistry returns an empty artifact codec registry.
func NewArtifactCodecRegistry() *ArtifactCodecRegistry {
	return &ArtifactCodecRegistry{codecs: make(map[string]TypedArtifactCodec)}
}

// Register adds a typed artifact codec. It panics on duplicate type IDs.
func (r *ArtifactCodecRegistry) Register(codec TypedArtifactCodec) {
	if r == nil || codec == nil {
		return
	}
	typeID := codec.TypeID()
	if typeID == "" {
		panic("contexty: artifact codec type_id is empty")
	}
	if r.codecs == nil {
		r.codecs = make(map[string]TypedArtifactCodec)
	}
	if _, exists := r.codecs[typeID]; exists {
		panic(fmt.Sprintf("contexty: duplicate artifact codec %q", typeID))
	}
	r.codecs[typeID] = codec
}

// Decode decodes an artifact through the registered codec matching ArtifactType.
func (r *ArtifactCodecRegistry) Decode(artifact ContextArtifact) (any, error) {
	if r == nil {
		return nil, errors.New("contexty: artifact decode: registry is nil")
	}
	codec, ok := r.codecs[artifact.ArtifactType]
	if !ok {
		return nil, fmt.Errorf("contexty: artifact decode: unregistered type_id %q", artifact.ArtifactType)
	}
	return codec.DecodeArtifact(artifact)
}

// NewTypedArtifact creates a ContextArtifact from a host-owned structured value.
func NewTypedArtifact[T any](id string, desc ArtifactCodecDescriptor[T], value T) (ContextArtifact, error) {
	if desc.TypeID == "" {
		return ContextArtifact{}, errors.New("contexty: typed artifact: type_id is empty")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ContextArtifact{}, fmt.Errorf("contexty: typed artifact %q: %w", desc.TypeID, err)
	}
	payload := ToolPayload{
		Text:     "",
		Data:     data,
		Binary:   nil,
		MIMEType: mimeApplicationJSON,
		Error:    nil,
		Progress: nil,
		Control:  nil,
	}
	if desc.Render != nil {
		payload.Text = desc.Render(value)
	}
	return ContextArtifact{
		ID:           id,
		Kind:         desc.Kind,
		ArtifactType: desc.TypeID,
		Payload:      payload,
		Blob:         nil,
		Extensions:   nil,
		Lifecycle:    artifactDescriptorLifecycle(desc.Lifecycle),
		BoundTurnID:  desc.BoundTurnID,
		OwnerRef:     cloneSourceRefPtr(desc.OwnerRef),
		SourceRefs:   cloneSourceRefs(desc.SourceRefs),
		MergePolicy:  desc.MergePolicy,
		Budget:       cloneArtifactBudget(desc.Budget),
		Persistence:  desc.Persistence,
	}, nil
}

func artifactDescriptorLifecycle(lifecycle ArtifactLifecycle) ArtifactLifecycle {
	if lifecycle == "" {
		return ArtifactLifecycleTurnBound
	}
	return lifecycle
}

func cloneArtifactBudget(in *ArtifactBudgetPolicy) *ArtifactBudgetPolicy {
	if in == nil {
		return nil
	}
	cp := *in
	return &cp
}

// DecodeTypedArtifact decodes a structured value from a typed artifact.
func DecodeTypedArtifact[T any](artifact ContextArtifact, desc ArtifactCodecDescriptor[T]) (T, error) {
	var zero T
	if desc.TypeID == "" {
		return zero, errors.New("contexty: typed artifact decode: type_id is empty")
	}
	if artifact.ArtifactType != desc.TypeID {
		return zero, fmt.Errorf(
			"contexty: typed artifact decode: expected %q, got %q",
			desc.TypeID,
			artifact.ArtifactType,
		)
	}
	if len(artifact.Payload.Data) == 0 {
		return zero, fmt.Errorf("contexty: typed artifact decode %q: payload data is empty", desc.TypeID)
	}
	var out T
	if err := json.Unmarshal(artifact.Payload.Data, &out); err != nil {
		return zero, fmt.Errorf("contexty: typed artifact decode %q: %w", desc.TypeID, err)
	}
	return out, nil
}
