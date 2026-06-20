package contexty

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

// ArtifactBudgetPolicy describes how an artifact participates in budget preflight.
type ArtifactBudgetPolicy struct {
	Group      string `json:"group,omitempty"`
	TokenLimit int    `json:"token_limit,omitempty"`
}

// ContextArtifact is the common contract for retrieval and memory context.
type ContextArtifact struct {
	ID          string                    `json:"id"`
	Kind        ArtifactKind              `json:"kind"`
	Payload     ToolPayload               `json:"payload"`
	Lifecycle   ArtifactLifecycle         `json:"lifecycle,omitempty"`
	BoundTurnID string                    `json:"bound_turn_id,omitempty"`
	OwnerRef    *SourceRef                `json:"owner_ref,omitempty"`
	SourceRefs  []SourceRef               `json:"source_refs,omitempty"`
	MergePolicy MergePolicy               `json:"merge_policy,omitempty"`
	Budget      *ArtifactBudgetPolicy     `json:"budget,omitempty"`
	Persistence ArtifactPersistencePolicy `json:"persistence,omitempty"`
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
	return RetrievalDocument{ContextArtifact: ContextArtifact{
		ID:          id,
		Kind:        ArtifactKindRetrievalDocument,
		Payload:     payload.Clone(),
		Lifecycle:   ArtifactLifecycleTurnBound,
		BoundTurnID: "",
		OwnerRef:    nil,
		SourceRefs:  nil,
		MergePolicy: "",
		Budget:      nil,
		Persistence: "",
	}}
}

// NewMemoryBlock builds a memory artifact.
func NewMemoryBlock(id string, payload ToolPayload) MemoryBlock {
	return MemoryBlock{ContextArtifact: ContextArtifact{
		ID:          id,
		Kind:        ArtifactKindMemoryBlock,
		Payload:     payload.Clone(),
		Lifecycle:   ArtifactLifecyclePersistent,
		BoundTurnID: "",
		OwnerRef:    nil,
		SourceRefs:  nil,
		MergePolicy: "",
		Budget:      nil,
		Persistence: "",
	}}
}

// Clone returns a deep copy.
func (a ContextArtifact) Clone() ContextArtifact {
	cp := a
	cp.Payload = a.Payload.Clone()
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

func activeArtifactsForTurn(turnID string, artifacts []ContextArtifact) []ContextArtifact {
	out := make([]ContextArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if !artifactVisibleInTurn(turnID, artifact) {
			continue
		}
		if !artifactFitsBudget(artifact) {
			continue
		}
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

func artifactFitsBudget(artifact ContextArtifact) bool {
	if artifact.Budget == nil || artifact.Budget.TokenLimit <= 0 {
		return true
	}
	return artifactPayloadTokens(artifact) <= artifact.Budget.TokenLimit
}

func artifactPayloadTokens(artifact ContextArtifact) int {
	text := artifact.Payload.PlainText()
	if text != "" {
		return len([]rune(text))
	}
	if len(artifact.Payload.Binary) > 0 {
		return len(artifact.Payload.Binary)
	}
	return 0
}

func upsertArtifact(artifacts []ContextArtifact, incoming ContextArtifact) []ContextArtifact {
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
	existingRefs := sourceRefSet(existing.SourceRefs)
	incomingRefs := sourceRefSet(incoming.SourceRefs)
	for ref := range incomingRefs {
		if existingRefs[ref] {
			return existing.Clone()
		}
	}
	return incoming.Clone()
}

func sourceRefSet(refs []SourceRef) map[string]bool {
	out := make(map[string]bool)
	for _, ref := range refs {
		key := ref.Namespace + "\x00" + ref.Kind + "\x00" + ref.ID + "\x00" + ref.CheckpointID + "\x00" + ref.URI
		if key != "\x00\x00\x00\x00" {
			out[key] = true
		}
	}
	return out
}

func persistentArtifacts(artifacts []ContextArtifact) []ContextArtifact {
	if len(artifacts) == 0 {
		return nil
	}
	out := make([]ContextArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if !artifactShouldPersist(artifact) {
			continue
		}
		out = append(out, artifact.Clone())
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

func artifactMessage(artifact ContextArtifact) Message {
	return Message{
		ID:         "artifact:" + artifact.ID,
		Role:       RoleSystem,
		Parts:      []ContentPart{TextPart{Text: artifact.Payload.PlainText()}},
		SourceRefs: cloneSourceRefs(artifact.SourceRefs),
	}
}

func artifactMessages(artifacts []ContextArtifact) []Message {
	if len(artifacts) == 0 {
		return nil
	}
	out := make([]Message, 0, len(artifacts))
	for _, artifact := range artifacts {
		out = append(out, artifactMessage(artifact))
	}
	return out
}
