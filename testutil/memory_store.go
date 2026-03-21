package testutil

import (
	"context"
	"sync"

	"github.com/skosovsky/contexty"
)

// threadState holds messages and a monotonic version for optimistic concurrency.
type threadState struct {
	msgs []contexty.Message
	ver  int64
}

// MemoryStore is an in-memory HistoryStore implementation for tests and local development.
type MemoryStore struct {
	mu      sync.RWMutex
	threads map[string]threadState
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		threads: make(map[string]threadState),
	}
}

// Load returns a deep-copied history snapshot and version for threadID.
func (s *MemoryStore) Load(_ context.Context, threadID string) (contexty.HistorySnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	st, ok := s.threads[threadID]
	if !ok {
		return contexty.HistorySnapshot{Messages: []contexty.Message{}, Version: 0}, nil
	}
	return contexty.HistorySnapshot{
		Messages: cloneMessages(st.msgs),
		Version:  st.ver,
	}, nil
}

// Append appends deep-copied msgs when expectedVersion matches.
func (s *MemoryStore) Append(
	_ context.Context,
	threadID string,
	expectedVersion int64,
	msgs ...contexty.Message,
) error {
	if len(msgs) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	st, ok := s.threads[threadID]
	if !ok {
		if expectedVersion != 0 {
			return contexty.ErrHistoryVersionConflict
		}
		st = threadState{ver: 0}
	} else if st.ver != expectedVersion {
		return contexty.ErrHistoryVersionConflict
	}
	st.msgs = append(st.msgs, cloneMessages(msgs)...)
	st.ver++
	s.threads[threadID] = st
	return nil
}

// Save replaces the full stored history when expectedVersion matches.
func (s *MemoryStore) Save(_ context.Context, threadID string, expectedVersion int64, msgs []contexty.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, ok := s.threads[threadID]
	if !ok {
		if expectedVersion != 0 {
			return contexty.ErrHistoryVersionConflict
		}
		if len(msgs) == 0 {
			return nil
		}
		st = threadState{ver: 0}
	} else if st.ver != expectedVersion {
		return contexty.ErrHistoryVersionConflict
	}

	if len(msgs) == 0 {
		delete(s.threads, threadID)
		return nil
	}
	st.msgs = cloneMessages(msgs)
	st.ver++
	s.threads[threadID] = st
	return nil
}

// Clear removes all stored history when expectedVersion matches.
func (s *MemoryStore) Clear(_ context.Context, threadID string, expectedVersion int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, ok := s.threads[threadID]
	if !ok {
		if expectedVersion != 0 {
			return contexty.ErrHistoryVersionConflict
		}
		return nil
	}
	if st.ver != expectedVersion {
		return contexty.ErrHistoryVersionConflict
	}
	delete(s.threads, threadID)
	return nil
}

func cloneMessages(msgs []contexty.Message) []contexty.Message {
	if msgs == nil {
		return nil
	}
	cloned := make([]contexty.Message, len(msgs))
	for i, msg := range msgs {
		cloned[i] = msg.Clone()
	}
	return cloned
}

// Compile-time check.
var _ contexty.HistoryStore = (*MemoryStore)(nil)
