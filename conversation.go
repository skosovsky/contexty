package contexty

import (
	"context"
	"errors"
	"maps"
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

// ConversationSnapshot is an immutable read view of all segments.
type ConversationSnapshot struct {
	segments map[SegmentName][]Message
	version  int64
}

// EmptySnapshot returns a zero-version snapshot with an empty segment map.
func EmptySnapshot() ConversationSnapshot {
	return ConversationSnapshot{segments: map[SegmentName][]Message{}, version: 0}
}

// Version returns the optimistic concurrency version.
func (s ConversationSnapshot) Version() int64 { return s.version }

// Segment returns a defensive copy of messages for the segment.
func (s ConversationSnapshot) Segment(name SegmentName) []Message {
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
func (s ConversationSnapshot) SegmentNames() []SegmentName {
	names := make([]SegmentName, 0, len(s.segments))
	for n := range s.segments {
		names = append(names, n)
	}
	return names
}

// AllSegments returns shallow copies of every segment (COW container).
func (s ConversationSnapshot) AllSegments() map[SegmentName][]Message {
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
func (s ConversationSnapshot) AllSegmentsSnapshot() ConversationSnapshot {
	return ConversationSnapshot{
		segments: s.AllSegments(),
		version:  s.version,
	}
}

// WithSegment returns a new snapshot sharing unchanged segments (structural sharing).
func (s ConversationSnapshot) WithSegment(name SegmentName, msgs []Message) ConversationSnapshot {
	segs := s.segments
	if segs == nil {
		segs = map[SegmentName][]Message{}
	}
	next := make(map[SegmentName][]Message, len(segs)+1)
	maps.Copy(next, segs)
	next[name] = cloneMessageSlice(msgs)
	return ConversationSnapshot{segments: next, version: s.version}
}

// WithVersion returns a snapshot with updated version (segments shared).
func (s ConversationSnapshot) WithVersion(v int64) ConversationSnapshot {
	return ConversationSnapshot{segments: s.segments, version: v}
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

// ConversationStore persists named dialogue segments with OCC.
type ConversationStore interface {
	Load(ctx context.Context, conversationID string) (ConversationSnapshot, error)
	UpdateSegment(
		ctx context.Context,
		conversationID string,
		expectedVersion int64,
		name SegmentName,
		msgs []Message,
	) error
	AppendSegment(
		ctx context.Context,
		conversationID string,
		expectedVersion int64,
		name SegmentName,
		msgs ...Message,
	) error
	Clear(ctx context.Context, conversationID string, expectedVersion int64) error
}

// ErrSegmentNotFound is returned when a segment is missing.
var ErrSegmentNotFound = errors.New("contexty: segment not found")

// MemoryConversationStore is an in-memory reference ConversationStore.
type MemoryConversationStore struct {
	mu            sync.RWMutex
	conversations map[string]ConversationSnapshot
}

// NewMemoryConversationStore returns an empty store.
func NewMemoryConversationStore() *MemoryConversationStore {
	//nolint:exhaustruct // sync.RWMutex zero-initializes
	return &MemoryConversationStore{conversations: make(map[string]ConversationSnapshot)}
}

// Load returns a snapshot with cloned segment slices.
func (s *MemoryConversationStore) Load(_ context.Context, conversationID string) (ConversationSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.conversations[conversationID]
	if !ok {
		return EmptySnapshot(), nil
	}
	return st.AllSegmentsSnapshot(), nil
}

// UpdateSegment replaces a segment when expectedVersion matches.
func (s *MemoryConversationStore) UpdateSegment(
	_ context.Context,
	conversationID string,
	expectedVersion int64,
	name SegmentName,
	msgs []Message,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateSegmentLocked(conversationID, expectedVersion, name, msgs)
}

func (s *MemoryConversationStore) updateSegmentLocked(
	conversationID string,
	expectedVersion int64,
	name SegmentName,
	msgs []Message,
) error {
	cur, ok := s.conversations[conversationID]
	if !ok {
		if expectedVersion != 0 {
			return ErrConversationVersionConflict
		}
		cur = EmptySnapshot()
	} else if cur.version != expectedVersion {
		return ErrConversationVersionConflict
	}
	next := cur.WithSegment(name, msgs)
	next.version = cur.version + 1
	s.conversations[conversationID] = next
	return nil
}

// AppendSegment appends messages to a segment.
func (s *MemoryConversationStore) AppendSegment(
	_ context.Context,
	conversationID string,
	expectedVersion int64,
	name SegmentName,
	msgs ...Message,
) error {
	if len(msgs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.conversations[conversationID]
	if !ok {
		if expectedVersion != 0 {
			return ErrConversationVersionConflict
		}
		cur = EmptySnapshot()
	} else if cur.version != expectedVersion {
		return ErrConversationVersionConflict
	}
	existing := cur.segments[name]
	combined := append(cloneMessageSlice(existing), cloneMessageSlice(msgs)...)
	next := cur.WithSegment(name, combined)
	next.version = cur.version + 1
	s.conversations[conversationID] = next
	return nil
}

// Clear removes all segments for a thread.
func (s *MemoryConversationStore) Clear(_ context.Context, conversationID string, expectedVersion int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.conversations[conversationID]
	if !ok {
		if expectedVersion != 0 {
			return ErrConversationVersionConflict
		}
		return nil
	}
	if cur.version != expectedVersion {
		return ErrConversationVersionConflict
	}
	delete(s.conversations, conversationID)
	return nil
}

var _ ConversationStore = (*MemoryConversationStore)(nil)
