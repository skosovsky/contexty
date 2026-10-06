package contexty

import (
	"context"
	"maps"
	"math"
	"sync"
)

// SegmentName identifies a named conversation segment.
type SegmentName string

const (
	SegmentSystem  SegmentName = "system"
	SegmentHistory SegmentName = "history"
	SegmentTools   SegmentName = "tools"
	SegmentMemory  SegmentName = "memory"
)

// ConversationState is an immutable read view of segments and artifacts.
type ConversationState struct {
	segments  map[SegmentName][]Message
	artifacts map[string]ContextArtifact
	version   int64
}

// ConversationSnapshot names the read-side state used by compile/render APIs.
type ConversationSnapshot = ConversationState

// EmptyState returns a zero-version empty state.
func EmptyState() ConversationState {
	return ConversationState{
		segments:  map[SegmentName][]Message{},
		artifacts: map[string]ContextArtifact{},
		version:   0,
	}
}

// EmptySnapshot returns a zero-version snapshot with an empty segment map.
func EmptySnapshot() ConversationSnapshot {
	return EmptyState()
}

// Version returns the optimistic concurrency version.
func (s ConversationState) Version() int64 { return s.version }

// Segment returns a defensive copy of messages for the segment.
func (s ConversationState) Segment(name SegmentName) []Message {
	msgs := s.segments[name]
	if len(msgs) == 0 {
		return nil
	}
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		out[i] = m.Clone()
	}
	return out
}

// SegmentNames returns segment keys present in the snapshot.
func (s ConversationState) SegmentNames() []SegmentName {
	names := make([]SegmentName, 0, len(s.segments))
	for n := range s.segments {
		names = append(names, n)
	}
	return names
}

// AllSegments returns shallow copies of every segment (COW container).
func (s ConversationState) AllSegments() map[SegmentName][]Message {
	if len(s.segments) == 0 {
		return nil
	}
	out := make(map[SegmentName][]Message, len(s.segments))
	for k, v := range s.segments {
		out[k] = cloneMessageSlice(v)
	}
	return out
}

// AllSegmentsSnapshot builds a snapshot with cloned messages.
func (s ConversationState) AllSegmentsSnapshot() ConversationSnapshot {
	return ConversationSnapshot{
		segments:  s.AllSegments(),
		artifacts: cloneArtifactMap(s.artifacts),
		version:   s.version,
	}
}

// WithSegment returns a new snapshot sharing unchanged segments (structural sharing).
func (s ConversationState) WithSegment(name SegmentName, msgs []Message) ConversationState {
	segs := s.segments
	if segs == nil {
		segs = map[SegmentName][]Message{}
	}
	next := make(map[SegmentName][]Message, len(segs)+1)
	maps.Copy(next, segs)
	next[name] = cloneMessageSlice(msgs)
	return ConversationState{
		segments:  next,
		artifacts: s.artifacts,
		version:   s.version,
	}
}

// WithVersion returns a snapshot with updated version (segments shared).
func (s ConversationState) WithVersion(v int64) ConversationState {
	return ConversationState{
		segments:  s.segments,
		artifacts: s.artifacts,
		version:   v,
	}
}

// Artifacts returns state artifacts in deterministic ID order.
func (s ConversationState) Artifacts() []ContextArtifact {
	return artifactMapValues(s.artifacts)
}

// ToolRounds returns cloned canonical tool rounds.
func (s ConversationState) ToolRounds() []ToolRound {
	return toolRoundsFromMessages(s.segments[SegmentHistory])
}

// WithArtifact returns state with artifact upserted by ID.
func (s ConversationState) WithArtifact(artifact ContextArtifact) ConversationState {
	next := mergeArtifactMaps(s.artifacts, []ContextArtifact{artifact})
	return ConversationState{
		segments:  s.segments,
		artifacts: next,
		version:   s.version,
	}
}

// WithoutArtifact returns state without the artifact ID.
func (s ConversationState) WithoutArtifact(id string) ConversationState {
	next := cloneArtifactMap(s.artifacts)
	delete(next, id)
	return ConversationState{
		segments:  s.segments,
		artifacts: next,
		version:   s.version,
	}
}

// WithArtifacts returns state with the exact artifact set.
func (s ConversationState) WithArtifacts(artifacts []ContextArtifact) ConversationState {
	next := make(map[string]ContextArtifact, len(artifacts))
	for _, artifact := range artifacts {
		next[artifact.ID] = artifact.Clone()
	}
	return ConversationState{
		segments:  s.segments,
		artifacts: next,
		version:   s.version,
	}
}

func cloneMessageSlice(msgs []Message) []Message {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		out[i] = m.Clone()
	}
	return out
}

// MemoryConversationStateStore is an in-memory reference ConversationStateStore.
// One mutex serializes IDs, including codec callbacks. Callbacks must not reenter
// this store and must synchronize their own shared state when reused elsewhere.
type MemoryConversationStateStore struct {
	mu            sync.RWMutex
	conversations map[string]ConversationSnapshot
	codec         ConversationCodec
}

// NewMemoryConversationStateStore returns an empty store.
func NewMemoryConversationStateStore(opts ...MemoryStateStoreOption) *MemoryConversationStateStore {
	//nolint:exhaustruct_v5 // sync.RWMutex zero-initializes
	store := &MemoryConversationStateStore{
		conversations: make(map[string]ConversationSnapshot),
		codec: ConversationCodec{
			Provenance:    DefaultProvenanceRegistry(),
			Extensions:    nil,
			OpaqueProfile: Descriptor{ID: "", Revision: ""},
		},
	}
	for _, opt := range opts {
		opt(store)
	}
	return store
}

// LoadState returns the full immutable state for a conversation.
func (s *MemoryConversationStateStore) LoadState(
	ctx context.Context,
	conversationID string,
) (ConversationState, error) {
	if err := ctx.Err(); err != nil {
		return ConversationState{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return ConversationState{}, err
	}
	st, ok := s.conversations[conversationID]
	if !ok {
		return EmptySnapshot(), nil
	}
	return st.AllSegmentsSnapshot(), nil
}

// CommitState atomically applies a nonempty batch when expectedVersion matches.
func (s *MemoryConversationStateStore) CommitState(
	ctx context.Context,
	conversationID string,
	expectedVersion int64,
	deltas ...ConversationDelta,
) error {
	if len(deltas) == 0 {
		return ErrEmptyCheckpointCommit
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	cur, ok := s.conversations[conversationID]
	if !ok {
		if expectedVersion != 0 {
			return ErrConversationVersionConflict
		}
		cur = EmptyState()
	} else if cur.version != expectedVersion {
		return ErrConversationVersionConflict
	}
	next, err := ApplyDeltas(cur, deltas...)
	if err != nil {
		return err
	}
	next, err = ProjectCheckpoint(
		next,
		JSONSerializer{Provenance: s.codec.Provenance, Extensions: s.codec.Extensions},
		s.codec.OpaqueProfile,
	)
	if err != nil {
		return err
	}
	wire, err := s.codec.Encode(next)
	if err != nil {
		return err
	}
	next, err = s.codec.Decode(wire)
	if err != nil {
		return err
	}
	next.version, err = NextConversationVersion(cur.version)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.conversations[conversationID] = next
	return nil
}

// ClearState deletes payload while advancing the durable OCC tombstone.
// LoadState after clear returns empty content with the new version. Callers must
// reload this token before recreating the same conversation ID.
func (s *MemoryConversationStateStore) ClearState(
	ctx context.Context,
	conversationID string,
	expectedVersion int64,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	cur, ok := s.conversations[conversationID]
	if !ok {
		if expectedVersion != 0 {
			return ErrConversationVersionConflict
		}
		cur = EmptyState()
	}
	if cur.version != expectedVersion {
		return ErrConversationVersionConflict
	}
	nextVersion, err := NextConversationVersion(cur.version)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.conversations[conversationID] = EmptyState().WithVersion(nextVersion)
	return nil
}

// NextConversationVersion advances an OCC token without wrapping or resetting.
// Stores must retain this identity across clear and content expiry.
func NextConversationVersion(current int64) (int64, error) {
	if current < 0 || current == math.MaxInt64 {
		return 0, ErrConversationVersionExhausted
	}
	return current + 1, nil
}

var _ ConversationStateStore = (*MemoryConversationStateStore)(nil)
