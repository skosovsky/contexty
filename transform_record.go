package contexty

import (
	"fmt"
	"strings"
)

// TransformAction classifies what the compile pipeline did to a message.
type TransformAction string

const (
	ActionEvicted   TransformAction = "evicted"
	ActionTruncated TransformAction = "truncated"
	ActionFormatted TransformAction = "formatted"
	ActionPassed    TransformAction = "passed"
)

const (
	ReasonProtectedPending      = "protected_pending"
	ReasonCurrentTurnProjection = "current_turn_projection"
	ReasonRoleProjection        = "role_projection"
	ReasonSegmentFormatter      = "segment_formatter"
	ReasonReplacedByFormatter   = "replaced_by_formatter"
	ReasonTransformHook         = "transform_hook"
	ReasonReplacedByHook        = "replaced_by_hook"
	ReasonReplacedByDeferred    = "replaced_by_deferred"
	ReasonTextReplacement       = "text_replacement"
	ReasonHistoricalArguments   = "historical_arguments"
	ReasonTokenBudgetExceeded   = "token_budget_exceeded" //nolint:gosec // reason label, not a credential
)

// TransformRecord describes a single message transformation.
type TransformRecord struct {
	Action TransformAction
	Reason string
}

// TransformChain retains every observed transition of a message in stage order.
// It replaces the last-write-wins record contract. Final derives the effective
// persistence outcome while retaining later observations after removal.
type TransformChain []TransformRecord

// Final returns the effective outcome; an empty chain has no declared action.
func (c TransformChain) Final() TransformRecord {
	var final TransformRecord
	for _, step := range c {
		switch step.Action {
		case ActionEvicted, ActionTruncated:
			final = step
		case ActionPassed, ActionFormatted:
			if final.Action == ActionEvicted || final.Action == ActionTruncated {
				continue
			}
			if final.Action == ActionFormatted && !isInPlaceFormatReason(final.Reason) {
				continue
			}
			final = step
		}
	}
	return final
}

// CompileResult is the immutable compile output plus O(1) traceability by Message.ID.
type CompileResult struct {
	Payload            AbstractPayload
	Transformations    map[string]TransformChain
	Source             CompileRequest     // immutable freeze after Normalize, before pipeline mutations
	Introduced         map[string]Message // deep-cloned baseline for payload-born IDs (post-deferred, pre-hooks/patches)
	Artifacts          []ContextArtifact
	NormalizedSnapshot ConversationSnapshot
	Writeback          CompileWritebackIntent
	Projections        map[string]CompileProjection
	Lineage            Lineage
	Manifest           *CompileManifest
	Record             *SavedCompileRecord
	Estimates          []ManifestEstimateReport
	ArtifactEstimates  []ArtifactBudgetEstimate
	Compactions        []CompactionRecord
}

// CompileRequest is the single exhaustive compile input (including stateless CompileSnapshot).
type CompileRequest struct {
	// DeferredResources is populated from engine declarations before resolution.
	// It is retained in Source, without raw body or authorization scope.
	DeferredResources      []ResourceSelection
	TurnID                 string
	System                 []Message
	History                []Message
	Memory                 []Message
	Tools                  []Message
	Pending                []Message
	CurrentTurn            *CurrentTurn
	Artifacts              []ContextArtifact
	Options                []CompileOption
	IdentityPolicy         MessageIdentityPolicy
	RequireDurableIdentity bool
	Targets                []CompileTarget
	CompilationID          string
	Lineage                Lineage
	Origins                []ContentRef
	SourceRevision         int64
	PreviousRecord         *ContentRef
}

// Normalize ensures every message has a non-empty ID and returns durable ID
// writebacks for messages whose IDs were assigned during normalization.
func (r CompileRequest) Normalize() (CompileRequest, []MessageIdentityWriteback, error) {
	return normalizeCompileRequest(r)
}

// Freeze returns a deep copy of all messages for immutable CompileResult.Source.
func (r CompileRequest) Freeze() CompileRequest {
	return CompileRequest{ //nolint:exhaustruct_v5 // Options omitted from immutable source snapshot
		DeferredResources:      cloneResourceSelections(r.DeferredResources),
		TurnID:                 r.TurnID,
		System:                 cloneMessageSlice(r.System),
		History:                cloneMessageSlice(r.History),
		Memory:                 cloneMessageSlice(r.Memory),
		Tools:                  cloneMessageSlice(r.Tools),
		Pending:                cloneMessageSlice(r.Pending),
		CurrentTurn:            cloneCurrentTurnPtr(r.CurrentTurn),
		Artifacts:              mergeArtifacts(r.Artifacts),
		IdentityPolicy:         r.IdentityPolicy,
		RequireDurableIdentity: r.RequireDurableIdentity,
		Targets:                append([]CompileTarget(nil), r.Targets...),
		CompilationID:          r.CompilationID,
		Lineage:                r.Lineage.Clone(),
		Origins:                append([]ContentRef(nil), r.Origins...),
		SourceRevision:         r.SourceRevision,
		PreviousRecord:         cloneContentRef(r.PreviousRecord),
	}
}

// Validate checks compile input invariants after Normalize.
func (r CompileRequest) Validate() error {
	if r.SourceRevision < 0 {
		return ErrInvalidManifest
	}
	if r.PreviousRecord != nil {
		if err := r.PreviousRecord.Validate(); err != nil {
			return err
		}
	}
	if r.CurrentTurn != nil {
		if err := r.CurrentTurn.validate(); err != nil {
			return err
		}
	}
	if err := validateUniqueMessageIDs(r.AllMessages()); err != nil {
		return err
	}
	if err := validateUniqueArtifactIDs(r.Artifacts); err != nil {
		return err
	}
	return validateCompileTargets(r.Targets)
}

func normalizeCompileRequest(r CompileRequest) (CompileRequest, []MessageIdentityWriteback, error) {
	var writebacks []MessageIdentityWriteback
	var err error
	next := CompileRequest{
		DeferredResources:      cloneResourceSelections(r.DeferredResources),
		TurnID:                 r.TurnID,
		System:                 nil,
		History:                nil,
		Memory:                 nil,
		Tools:                  nil,
		Pending:                nil,
		CurrentTurn:            nil,
		Artifacts:              mergeArtifacts(r.Artifacts),
		Options:                r.Options,
		IdentityPolicy:         r.IdentityPolicy,
		RequireDurableIdentity: r.RequireDurableIdentity,
		Targets:                append([]CompileTarget(nil), r.Targets...),
		CompilationID:          r.CompilationID,
		Lineage:                r.Lineage.Clone(),
		Origins:                append([]ContentRef(nil), r.Origins...),
		SourceRevision:         r.SourceRevision,
		PreviousRecord:         cloneContentRef(r.PreviousRecord),
	}
	for i := range next.Targets {
		next.Targets[i].Name = strings.TrimSpace(next.Targets[i].Name)
	}
	next.System, writebacks, err = normalizeMessagesForCompile(
		r.System,
		SegmentSystem,
		0,
		r.TurnID,
		r.IdentityPolicy,
		r.RequireDurableIdentity,
		writebacks,
	)
	if err != nil {
		return CompileRequest{}, nil, err
	}
	next.History, writebacks, err = normalizeMessagesForCompile(
		r.History,
		SegmentHistory,
		0,
		r.TurnID,
		r.IdentityPolicy,
		r.RequireDurableIdentity,
		writebacks,
	)
	if err != nil {
		return CompileRequest{}, nil, err
	}
	next.Memory, writebacks, err = normalizeMessagesForCompile(
		r.Memory,
		SegmentMemory,
		0,
		r.TurnID,
		r.IdentityPolicy,
		r.RequireDurableIdentity,
		writebacks,
	)
	if err != nil {
		return CompileRequest{}, nil, err
	}
	next.Tools, writebacks, err = normalizeMessagesForCompile(
		r.Tools,
		SegmentTools,
		0,
		r.TurnID,
		r.IdentityPolicy,
		r.RequireDurableIdentity,
		writebacks,
	)
	if err != nil {
		return CompileRequest{}, nil, err
	}
	next.Pending, writebacks, err = normalizeMessagesForCompile(
		r.Pending,
		SegmentHistory,
		len(next.History),
		r.TurnID,
		r.IdentityPolicy,
		r.RequireDurableIdentity,
		writebacks,
	)
	if err != nil {
		return CompileRequest{}, nil, err
	}
	next.CurrentTurn, writebacks, err = normalizeCurrentTurnForCompile(
		r.CurrentTurn,
		r.TurnID,
		len(next.History)+len(next.Pending),
		r.IdentityPolicy,
		r.RequireDurableIdentity,
		writebacks,
	)
	if err != nil {
		return CompileRequest{}, nil, err
	}
	return next, writebacks, nil
}

func normalizeMessagesForCompile(
	msgs []Message,
	seg SegmentName,
	indexOffset int,
	turnID string,
	policy MessageIdentityPolicy,
	requireDurable bool,
	writebacks []MessageIdentityWriteback,
) ([]Message, []MessageIdentityWriteback, error) {
	if len(msgs) == 0 {
		return nil, writebacks, nil
	}
	out := make([]Message, len(msgs))
	for i, msg := range msgs {
		normalized, wb, err := normalizeMessageForCompile(
			msg,
			MessageIdentityContext{
				Segment:          seg,
				Index:            indexOffset + i,
				TurnID:           turnID,
				TargetName:       "",
				CurrentTurn:      false,
				PromptProjection: false,
			},
			policy,
			requireDurable,
		)
		if err != nil {
			return nil, nil, err
		}
		out[i] = normalized
		if wb != nil {
			writebacks = append(writebacks, *wb)
		}
	}
	return out, writebacks, nil
}

func normalizeMessageForCompile(
	msg Message,
	idCtx MessageIdentityContext,
	policy MessageIdentityPolicy,
	requireDurable bool,
) (Message, *MessageIdentityWriteback, error) {
	if msg.ID != "" {
		return msg.Clone(), nil, nil
	}
	if policy == nil && requireDurable {
		return Message{}, nil, ErrMissingIdentityPolicy
	}
	normalized := msg.Clone()
	if policy == nil {
		normalized = EnsureMessageID(normalized)
	} else {
		id, err := policy.ResolveMessageID(idCtx, msg.Clone())
		if err != nil {
			return Message{}, nil, fmt.Errorf("contexty: identity policy: %w", err)
		}
		if id == "" {
			return Message{}, nil, ErrMissingIdentityPolicy
		}
		normalized.ID = id
	}
	return normalized, &MessageIdentityWriteback{
		Segment:     idCtx.Segment,
		Index:       idCtx.Index,
		ID:          normalized.ID,
		Before:      msg.Clone(),
		After:       normalized.Clone(),
		CurrentTurn: idCtx.CurrentTurn,
	}, nil
}

func normalizeCurrentTurnForCompile(
	turn *CurrentTurn,
	turnID string,
	index int,
	policy MessageIdentityPolicy,
	requireDurable bool,
	writebacks []MessageIdentityWriteback,
) (*CurrentTurn, []MessageIdentityWriteback, error) {
	if turn == nil || !turn.hasRaw() {
		return nil, writebacks, nil
	}
	raw, wb, err := normalizeMessageForCompile(
		turn.Raw,
		MessageIdentityContext{
			Segment:          SegmentHistory,
			Index:            index,
			TurnID:           turnID,
			TargetName:       "",
			CurrentTurn:      true,
			PromptProjection: false,
		},
		policy,
		requireDurable,
	)
	if err != nil {
		return nil, nil, err
	}
	if wb != nil {
		writebacks = append(writebacks, *wb)
	}
	normalized := CurrentTurn{
		Raw:         raw,
		PromptSafe:  Message{},
		Persistence: turn.Persistence,
	}
	if turn.hasPromptSafe() {
		prompt := turn.PromptSafe.Clone()
		if prompt.ID != "" && raw.ID != "" && prompt.ID != raw.ID {
			return nil, nil, ErrCurrentTurnIDMismatch
		}
		prompt.ID = raw.ID
		normalized.PromptSafe = prompt
	}
	return &normalized, writebacks, nil
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
	if r.CurrentTurn != nil && r.CurrentTurn.hasRaw() {
		out = append(out, r.CurrentTurn.Raw)
	}
	return out
}

// RequestFromSnapshot builds a CompileRequest from snapshot segments (no Pending).
func RequestFromSnapshot(snap ConversationSnapshot) CompileRequest {
	return CompileRequest{ //nolint:exhaustruct_v5 // Pending is compile-time only
		System:         snap.Segment(SegmentSystem),
		History:        snap.Segment(SegmentHistory),
		Memory:         snap.Segment(SegmentMemory),
		Tools:          snap.Segment(SegmentTools),
		Artifacts:      snap.Artifacts(),
		SourceRevision: snap.Version(),
	}
}

// ToSnapshot builds a conversation snapshot from request segments (excludes Pending).
func (r CompileRequest) ToSnapshot() ConversationSnapshot {
	snap := EmptySnapshot().WithVersion(r.SourceRevision)
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

func (r CompileRequest) WritebackSnapshot() ConversationSnapshot {
	snap := r.ToSnapshot()
	if r.CurrentTurn != nil {
		if msg, ok := r.CurrentTurn.persistedMessage(); ok {
			history := snap.Segment(SegmentHistory)
			history = append(history, msg)
			snap = snap.WithSegment(SegmentHistory, history)
		}
	}
	return snap.WithArtifacts(persistentArtifacts(r.Artifacts))
}
