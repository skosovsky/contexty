package contexty

// TransformAction classifies what the compile pipeline did to a message.
type TransformAction string

const (
	ActionEvicted   TransformAction = "evicted"
	ActionTruncated TransformAction = "truncated"
	ActionFormatted TransformAction = "formatted"
	ActionPassed    TransformAction = "passed"
)

const (
	ReasonProtectedPending    = "protected_pending"
	ReasonSegmentFormatter    = "segment_formatter"
	ReasonReplacedByFormatter = "replaced_by_formatter"
	ReasonTransformHook       = "transform_hook"
	ReasonReplacedByHook      = "replaced_by_hook"
	ReasonReplacedByDeferred  = "replaced_by_deferred"
	ReasonEphemeralPatch      = "ephemeral_patch"
	ReasonTokenBudgetExceeded = "token_budget_exceeded" //nolint:gosec // reason label, not a credential
)

// TransformRecord describes a single message transformation.
type TransformRecord struct {
	Action TransformAction
	Reason string
}

// CompileResult is the immutable compile output plus O(1) traceability by Message.ID.
type CompileResult struct {
	Payload         AbstractPayload
	Transformations map[string]TransformRecord
	Source          CompileRequest     // immutable freeze after Normalize, before pipeline mutations
	Introduced      map[string]Message // deep-cloned baseline for payload-born IDs (post-deferred, pre-hooks/patches)
	Artifacts       []ContextArtifact
}

// CompileRequest is the single exhaustive compile input (including stateless CompileSnapshot).
type CompileRequest struct {
	TurnID    string
	System    []Message
	History   []Message
	Memory    []Message
	Tools     []Message
	Pending   []Message
	Artifacts []ContextArtifact
	Options   []CompileOption
}

// Normalize ensures every message has a non-empty ID.
func (r CompileRequest) Normalize() CompileRequest {
	return CompileRequest{
		TurnID:    r.TurnID,
		System:    EnsureMessageIDs(r.System),
		History:   EnsureMessageIDs(r.History),
		Memory:    EnsureMessageIDs(r.Memory),
		Tools:     EnsureMessageIDs(r.Tools),
		Pending:   EnsureMessageIDs(r.Pending),
		Artifacts: cloneArtifacts(r.Artifacts),
		Options:   r.Options,
	}
}

// Freeze returns a deep copy of all messages for immutable CompileResult.Source.
func (r CompileRequest) Freeze() CompileRequest {
	return CompileRequest{ //nolint:exhaustruct // Options omitted from immutable source snapshot
		TurnID:    r.TurnID,
		System:    cloneMessageSlice(r.System),
		History:   cloneMessageSlice(r.History),
		Memory:    cloneMessageSlice(r.Memory),
		Tools:     cloneMessageSlice(r.Tools),
		Pending:   cloneMessageSlice(r.Pending),
		Artifacts: cloneArtifacts(r.Artifacts),
	}
}

// Validate checks compile input invariants after Normalize.
func (r CompileRequest) Validate() error {
	if err := validateUniqueMessageIDs(r.AllMessages()); err != nil {
		return err
	}
	return validateUniqueArtifactIDs(r.Artifacts)
}

func validateUniqueArtifactIDs(artifacts []ContextArtifact) error {
	seen := make(map[string]struct{}, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.ID == "" {
			continue
		}
		if _, dup := seen[artifact.ID]; dup {
			return ErrDuplicateMessageID
		}
		seen[artifact.ID] = struct{}{}
	}
	return nil
}

// validateUniqueMessageIDs returns ErrDuplicateMessageID when any non-empty ID repeats.
func validateUniqueMessageIDs(msgs []Message) error {
	seen := make(map[string]struct{}, len(msgs))
	for _, m := range msgs {
		if m.ID == "" {
			continue
		}
		if _, dup := seen[m.ID]; dup {
			return ErrDuplicateMessageID
		}
		seen[m.ID] = struct{}{}
	}
	return nil
}

// validateSnapshotUniqueIDs ensures all messages in snapshot segments have unique IDs.
func validateSnapshotUniqueIDs(snap ConversationSnapshot) error {
	var msgs []Message
	for _, seg := range snapshotSegmentOrder() {
		msgs = append(msgs, snap.Segment(seg)...)
	}
	return validateUniqueMessageIDs(msgs)
}

// snapshotSegmentOrder returns stable segment enumeration for validation and recorder helpers.
func snapshotSegmentOrder() []SegmentName {
	return []SegmentName{
		SegmentSystem,
		SegmentHistory,
		SegmentMemory,
		SegmentTools,
	}
}

// AllMessages returns every input message in stable segment order for recorder init.
func (r CompileRequest) AllMessages() []Message {
	var out []Message
	out = append(out, r.System...)
	out = append(out, r.History...)
	out = append(out, r.Memory...)
	out = append(out, r.Tools...)
	out = append(out, r.Pending...)
	return out
}

// RequestFromSnapshot builds a CompileRequest from snapshot segments (no Pending).
func RequestFromSnapshot(snap ConversationSnapshot) CompileRequest {
	return CompileRequest{ //nolint:exhaustruct // Pending is compile-time only
		System:    snap.Segment(SegmentSystem),
		History:   snap.Segment(SegmentHistory),
		Memory:    snap.Segment(SegmentMemory),
		Tools:     snap.Segment(SegmentTools),
		Artifacts: snap.Artifacts(),
	}
}

// ToSnapshot builds a conversation snapshot from request segments (excludes Pending).
func (r CompileRequest) ToSnapshot() ConversationSnapshot {
	snap := EmptySnapshot()
	if len(r.System) > 0 {
		snap = snap.WithSegment(SegmentSystem, cloneMessageSlice(r.System))
	}
	if len(r.History) > 0 {
		snap = snap.WithSegment(SegmentHistory, cloneMessageSlice(r.History))
	}
	if len(r.Memory) > 0 {
		snap = snap.WithSegment(SegmentMemory, cloneMessageSlice(r.Memory))
	}
	if len(r.Tools) > 0 {
		snap = snap.WithSegment(SegmentTools, cloneMessageSlice(r.Tools))
	}
	if len(r.Artifacts) > 0 {
		snap = snap.WithArtifacts(r.Artifacts)
	}
	return snap
}
