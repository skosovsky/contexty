package contexty

import (
	"context"
	"mime"
	"slices"
	"strings"
)

// ArtifactKind classifies context artifacts without binding them to host domains.
type ArtifactKind string

const (
	ArtifactKindRetrievalDocument ArtifactKind = "retrieval_document"
	ArtifactKindMemoryBlock       ArtifactKind = "memory_block"
)

// ArtifactLifecycle controls artifact visibility across turns.
type ArtifactLifecycle string

const (
	ArtifactLifecycleTurnBound  ArtifactLifecycle = "bound_to_turn"
	ArtifactLifecyclePersistent ArtifactLifecycle = "persistent"
	ArtifactLifecycleEphemeral  ArtifactLifecycle = "ephemeral"
)

// ArtifactPersistencePolicy describes whether an artifact should be checkpointed.
type ArtifactPersistencePolicy string

const (
	ArtifactPersistenceDefault ArtifactPersistencePolicy = ""
	ArtifactPersistenceStore   ArtifactPersistencePolicy = "store"
	ArtifactPersistenceSkip    ArtifactPersistencePolicy = "skip"
)

// ArtifactBudgetPolicy bounds the materialized message using each output estimator.
// A nil policy is unlimited locally; TokenLimit zero is zero, not unlimited.
// Negative limits on active artifacts fail compilation.
type ArtifactBudgetPolicy struct {
	TokenLimit int `json:"token_limit,omitempty"`
}

// ContextArtifact is the common contract for retrieval and memory context.
type ContextArtifact struct {
	ID           string                    `json:"id"`
	Kind         ArtifactKind              `json:"kind"`
	ArtifactType string                    `json:"artifact_type,omitempty"`
	Payload      ToolPayload               `json:"payload"`
	Blob         *ArtifactBlob             `json:"blob,omitempty"`
	Extensions   []Extension               `json:"-"`
	Lifecycle    ArtifactLifecycle         `json:"lifecycle,omitempty"`
	BoundTurnID  string                    `json:"bound_turn_id,omitempty"`
	OwnerRef     *SourceRef                `json:"owner_ref,omitempty"`
	SourceRefs   []SourceRef               `json:"source_refs,omitempty"`
	MergePolicy  MergePolicy               `json:"merge_policy,omitempty"`
	Budget       *ArtifactBudgetPolicy     `json:"budget,omitempty"`
	Persistence  ArtifactPersistencePolicy `json:"persistence,omitempty"`
}

// RetrievalDocument is a source-backed retrieved context artifact.
type RetrievalDocument struct {
	ContextArtifact
}

// MemoryBlock is durable or ephemeral memory context.
type MemoryBlock struct {
	ContextArtifact
}

// NewRetrievalDocument builds a retrieval artifact.
func NewRetrievalDocument(id string, payload ToolPayload) RetrievalDocument {
	return RetrievalDocument{
		ID:           id,
		Kind:         ArtifactKindRetrievalDocument,
		ArtifactType: "",
		Payload:      payload.Clone(),
		Blob:         nil,
		Extensions:   nil,
		Lifecycle:    ArtifactLifecycleTurnBound,
		BoundTurnID:  "",
		OwnerRef:     nil,
		SourceRefs:   nil,
		MergePolicy:  "",
		Budget:       nil,
		Persistence:  "",
	}
}

// NewMemoryBlock builds a memory artifact.
func NewMemoryBlock(id string, payload ToolPayload) MemoryBlock {
	return MemoryBlock{
		ID:           id,
		Kind:         ArtifactKindMemoryBlock,
		ArtifactType: "",
		Payload:      payload.Clone(),
		Blob:         nil,
		Extensions:   nil,
		Lifecycle:    ArtifactLifecyclePersistent,
		BoundTurnID:  "",
		OwnerRef:     nil,
		SourceRefs:   nil,
		MergePolicy:  "",
		Budget:       nil,
		Persistence:  "",
	}
}

// Clone returns a deep copy.
func (a ContextArtifact) Clone() ContextArtifact {
	cp := a
	cp.Payload = a.Payload.Clone()
	cp.Blob = a.Blob.clone()
	cp.Extensions = cloneExtensions(a.Extensions)
	cp.OwnerRef = cloneSourceRefPtr(a.OwnerRef)
	cp.SourceRefs = cloneSourceRefs(a.SourceRefs)
	if a.Budget != nil {
		budgetCopy := *a.Budget
		cp.Budget = &budgetCopy
	}
	return cp
}

// WithTurn returns a turn-bound copy of the artifact.
func (a ContextArtifact) WithTurn(turnID string) ContextArtifact {
	cp := a.Clone()
	cp.Lifecycle = ArtifactLifecycleTurnBound
	cp.BoundTurnID = turnID
	return cp
}

// WithPersistence returns a copy with an explicit checkpoint policy.
func (a ContextArtifact) WithPersistence(policy ArtifactPersistencePolicy) ContextArtifact {
	cp := a.Clone()
	cp.Persistence = policy
	return cp
}

// WithBudget returns a copy with an artifact-local budget policy.
func (a ContextArtifact) WithBudget(policy ArtifactBudgetPolicy) ContextArtifact {
	cp := a.Clone()
	cp.Budget = &policy
	return cp
}

// WithOwner returns a copy with explicit host-owned ownership metadata.
func (a ContextArtifact) WithOwner(owner SourceRef) ContextArtifact {
	cp := a.Clone()
	cp.OwnerRef = cloneSourceRefPtr(&owner)
	return cp
}

func cloneArtifacts(artifacts []ContextArtifact) []ContextArtifact {
	if len(artifacts) == 0 {
		return nil
	}
	out := make([]ContextArtifact, len(artifacts))
	for i, artifact := range artifacts {
		out[i] = artifact.Clone()
	}
	return out
}

func mergeArtifacts(artifacts []ContextArtifact) []ContextArtifact {
	out := make([]ContextArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		out = upsertArtifact(out, artifact)
	}
	return out
}

func artifactVisibleInTurn(turnID string, artifact ContextArtifact) bool {
	switch artifact.Lifecycle {
	case ArtifactLifecycleTurnBound:
		return turnID != "" && artifact.BoundTurnID == turnID
	case ArtifactLifecyclePersistent, ArtifactLifecycleEphemeral, "":
		return true
	default:
		return false
	}
}

func upsertArtifact(artifacts []ContextArtifact, incoming ContextArtifact) []ContextArtifact {
	if incoming.MergePolicy == PolicyReplaceByOrigin {
		if replaceKey := artifactOriginKey(incoming); replaceKey != "" {
			filtered := artifacts[:0]
			for _, existing := range artifacts {
				if existing.ID == incoming.ID || artifactOriginKey(existing) != replaceKey {
					filtered = append(filtered, existing)
				}
			}
			artifacts = filtered
		}
	}
	if incoming.ID == "" {
		return append(artifacts, incoming.Clone())
	}
	for idx, existing := range artifacts {
		if existing.ID != incoming.ID {
			continue
		}
		if incoming.Lifecycle == ArtifactLifecycleEphemeral {
			artifacts[idx] = incoming.Clone()
			return artifacts
		}
		artifacts[idx] = mergeArtifact(existing, incoming)
		return artifacts
	}
	return append(artifacts, incoming.Clone())
}

func mergeArtifact(existing, incoming ContextArtifact) ContextArtifact {
	switch incoming.MergePolicy {
	case PolicyAppend:
		return appendArtifactPayload(existing, incoming)
	case PolicyDeduplicateByLayer:
		return deduplicateArtifactBySourceLayer(existing, incoming)
	case PolicyReplaceByOrigin, "":
		return incoming.Clone()
	default:
		return incoming.Clone()
	}
}

func appendArtifactPayload(existing, incoming ContextArtifact) ContextArtifact {
	merged := incoming.Clone()
	merged.Extensions = append(cloneExtensions(existing.Extensions), merged.Extensions...)
	merged.SourceRefs = cloneSourceRefs(existing.SourceRefs)
	for _, source := range incoming.SourceRefs {
		if !slices.Contains(merged.SourceRefs, source) {
			merged.SourceRefs = append(merged.SourceRefs, source)
		}
	}
	left := existing.Payload.PlainText()
	right := incoming.Payload.PlainText()
	if left == "" {
		return merged
	}
	if right == "" {
		merged.Payload = existing.Payload.Clone()
		return merged
	}
	merged.Payload = TextPayload(left + "\n" + right)
	return merged
}

func deduplicateArtifactBySourceLayer(existing, incoming ContextArtifact) ContextArtifact {
	if artifactsShareSourceLayer(existing, incoming) {
		return existing.Clone()
	}
	return incoming.Clone()
}

func artifactsShareSourceLayer(existing, incoming ContextArtifact) bool {
	existingRefs := sourceRefSet(existing.SourceRefs)
	incomingRefs := sourceRefSet(incoming.SourceRefs)
	for ref := range incomingRefs {
		if existingRefs[ref] {
			return true
		}
	}
	return false
}

func sourceRefSet(refs []SourceRef) map[string]bool {
	out := make(map[string]bool)
	for _, ref := range refs {
		key := sourceRefKey(ref)
		if key != "\x00\x00\x00\x00" {
			out[key] = true
		}
	}
	return out
}

func artifactOriginKey(artifact ContextArtifact) string {
	ownerKey := ""
	if artifact.OwnerRef != nil {
		ownerKey = sourceRefKey(*artifact.OwnerRef)
	}
	sourceKeys := make([]string, 0, len(artifact.SourceRefs))
	for _, ref := range artifact.SourceRefs {
		key := sourceRefKey(ref)
		if key != "\x00\x00\x00\x00" {
			sourceKeys = append(sourceKeys, key)
		}
	}
	if ownerKey == "" && len(sourceKeys) == 0 {
		return ""
	}
	slices.Sort(sourceKeys)
	return string(artifact.Kind) +
		"\x00" + artifact.ArtifactType +
		"\x00" + ownerKey +
		"\x00" + strings.Join(sourceKeys, "\x1f")
}

func sourceRefKey(ref SourceRef) string {
	return ref.Namespace + "\x00" + ref.Kind + "\x00" + ref.ID + "\x00" + ref.CheckpointID + "\x00" + ref.URI
}

func persistentArtifacts(artifacts []ContextArtifact) []ContextArtifact {
	var persistent []ContextArtifact
	for _, artifact := range artifacts {
		if artifactShouldPersist(artifact) {
			persistent = append(persistent, artifact.Clone())
		}
	}
	return persistent
}

func artifactShouldPersist(artifact ContextArtifact) bool {
	switch artifact.Persistence {
	case ArtifactPersistenceStore:
		return true
	case ArtifactPersistenceSkip:
		return false
	case ArtifactPersistenceDefault:
		return artifact.Lifecycle == "" || artifact.Lifecycle == ArtifactLifecyclePersistent
	default:
		return false
	}
}

func artifactMessages(ctx context.Context, artifacts []ContextArtifact) ([]Message, error) {
	if len(artifacts) == 0 {
		return nil, nil
	}
	out := make([]Message, 0, len(artifacts))
	for _, artifact := range artifacts {
		message, err := artifactMessage(ctx, artifact)
		if err != nil {
			return nil, err
		}
		out = append(out, message)
	}
	return out, nil
}

func artifactParts(payload ToolPayload) ([]ContentPart, error) {
	mediaType, _, parseErr := mime.ParseMediaType(payload.MIMEType)
	major, _, _ := strings.Cut(mediaType, "/")
	isMedia := len(payload.Binary) > 0 || (payload.MIMEType != "" &&
		(parseErr != nil || (mediaType != mimeApplicationJSON && major != "text")))
	if !isMedia {
		return []ContentPart{TextPart{Text: payload.PlainText()}}, nil
	}
	data := payload.Binary
	if len(data) == 0 {
		data = payload.Data
		if len(data) == 0 {
			data = []byte(payload.Text)
		}
	}
	part := MediaPart{MIMEType: payload.MIMEType, Data: slices.Clone(data)}
	if err := part.Validate(); err != nil {
		return nil, err
	}
	parts := []ContentPart{part}
	if len(payload.Binary) > 0 && payload.Text != "" {
		parts = append([]ContentPart{TextPart{Text: payload.Text}}, parts...)
	}
	return parts, nil
}
