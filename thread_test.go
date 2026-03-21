package contexty

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubHistoryStore struct {
	loadData     []Message
	loadErr      error
	appendErr    error
	saveErr      error
	clearErr     error
	appended     []Message
	saved        []Message
	appendCalls  int
	saveCalls    int
	clearCalls   int
	loadedThread string
	version      int64
}

func (s *stubHistoryStore) Load(_ context.Context, threadID string) (HistorySnapshot, error) {
	s.loadedThread = threadID
	if s.loadErr != nil {
		return HistorySnapshot{}, s.loadErr
	}
	return HistorySnapshot{Messages: cloneMessages(s.loadData), Version: s.version}, nil
}

func (s *stubHistoryStore) Append(_ context.Context, _ string, expectedVersion int64, msgs ...Message) error {
	s.appendCalls++
	if s.version != expectedVersion {
		return ErrHistoryVersionConflict
	}
	s.appended = cloneMessages(msgs)
	s.version++
	return s.appendErr
}

func (s *stubHistoryStore) Save(_ context.Context, _ string, expectedVersion int64, msgs []Message) error {
	s.saveCalls++
	if s.version != expectedVersion {
		return ErrHistoryVersionConflict
	}
	s.saved = cloneMessages(msgs)
	s.version++
	return s.saveErr
}

func (s *stubHistoryStore) Clear(_ context.Context, _ string, expectedVersion int64) error {
	s.clearCalls++
	if s.version != expectedVersion {
		return ErrHistoryVersionConflict
	}
	s.version++
	return s.clearErr
}

func TestNewThread_InvalidConfig(t *testing.T) {
	_, err := NewThread(nil, "thread-1", "history")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidThreadConfig)
}

func TestThreadBuildContext_AppendsWhenHistoryIsUnchanged(t *testing.T) {
	store := &stubHistoryStore{
		loadData: []Message{TextMessage(RoleUser, "old")},
	}
	thread, err := NewThread(store, "thread-1", "history")
	require.NoError(t, err)

	builder := NewBuilder(100, &FixedCounter{TokensPerMessage: 10})
	builder.AddBlock("instructions", MemoryBlock{
		Strategy: NewStrictStrategy(),
		Messages: []Message{TextMessage(RoleSystem, "helpful")},
	})
	builder.AddBlock("history", MemoryBlock{
		Strategy: NewStrictStrategy(),
		Messages: []Message{TextMessage(RoleUser, "placeholder")},
	})

	out, err := thread.BuildContext(context.Background(), builder, []Message{TextMessage(RoleAssistant, "new")})
	require.NoError(t, err)
	require.Len(t, out, 3)
	assert.Equal(t, 1, store.appendCalls)
	assert.Equal(t, 0, store.saveCalls)
	assert.Equal(t, []Message{TextMessage(RoleAssistant, "new")}, store.appended)
}

func TestThreadBuildContext_SavesCompactedHistory(t *testing.T) {
	store := &stubHistoryStore{
		loadData: []Message{
			TextMessage(RoleUser, "old"),
			TextMessage(RoleAssistant, "older"),
		},
	}
	thread, err := NewThread(store, "thread-1", "history")
	require.NoError(t, err)

	builder := NewBuilder(10, &FixedCounter{TokensPerMessage: 10})
	builder.AddBlock("history", MemoryBlock{
		Strategy: NewDropHeadStrategy(DropHeadConfig{}),
		Messages: []Message{TextMessage(RoleUser, "placeholder")},
	})

	_, err = thread.BuildContext(context.Background(), builder, []Message{TextMessage(RoleUser, "latest")})
	require.NoError(t, err)
	assert.Equal(t, 0, store.appendCalls)
	assert.Equal(t, 1, store.saveCalls)
	assert.Equal(t, []Message{TextMessage(RoleUser, "latest")}, store.saved)
}

func TestThreadBuildContext_SavesEmptyHistoryWhenBlockDropsAllMessages(t *testing.T) {
	store := &stubHistoryStore{
		loadData: []Message{TextMessage(RoleUser, "old")},
	}
	thread, err := NewThread(store, "thread-1", "history")
	require.NoError(t, err)

	builder := NewBuilder(10, &FixedCounter{TokensPerMessage: 10})
	builder.AddBlock("instructions", MemoryBlock{
		Strategy: NewStrictStrategy(),
		Messages: []Message{TextMessage(RoleSystem, "reserved")},
	})
	builder.AddBlock("history", MemoryBlock{
		Strategy: NewDropStrategy(),
		Messages: []Message{TextMessage(RoleUser, "placeholder")},
	})

	_, err = thread.BuildContext(context.Background(), builder, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, store.appendCalls)
	assert.Equal(t, 1, store.saveCalls)
	assert.Nil(t, store.saved)
}

func TestThreadBuildContext_DoesNotWriteWhenHistoryIsUnchangedAndNoNewMessages(t *testing.T) {
	store := &stubHistoryStore{
		loadData: []Message{TextMessage(RoleUser, "old")},
	}
	thread, err := NewThread(store, "thread-1", "history")
	require.NoError(t, err)

	builder := NewBuilder(100, &FixedCounter{TokensPerMessage: 10})
	builder.AddBlock("history", MemoryBlock{
		Strategy: NewStrictStrategy(),
		Messages: []Message{TextMessage(RoleUser, "placeholder")},
	})

	_, err = thread.BuildContext(context.Background(), builder, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, store.appendCalls)
	assert.Equal(t, 0, store.saveCalls)
}

func TestThreadBuildContext_DoesNotWriteWhenStoredHistoryIsEmptySliceAndNoNewMessages(t *testing.T) {
	store := &stubHistoryStore{
		loadData: []Message{},
	}
	thread, err := NewThread(store, "thread-1", "history")
	require.NoError(t, err)

	builder := NewBuilder(100, &FixedCounter{TokensPerMessage: 10})
	builder.AddBlock("history", MemoryBlock{
		Strategy: NewStrictStrategy(),
		Messages: []Message{TextMessage(RoleUser, "placeholder")},
	})

	_, err = thread.BuildContext(context.Background(), builder, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, store.appendCalls)
	assert.Equal(t, 0, store.saveCalls)
	assert.Nil(t, store.saved)
}

func TestThreadBuildContext_RetriesOnVersionConflict(t *testing.T) {
	store := &conflictOnceAppendStore{}
	thread, err := NewThread(store, "thread-1", "history")
	require.NoError(t, err)

	builder := NewBuilder(100, &FixedCounter{TokensPerMessage: 10})
	builder.AddBlock("history", MemoryBlock{
		Strategy: NewStrictStrategy(),
		Messages: []Message{TextMessage(RoleUser, "placeholder")},
	})

	_, err = thread.BuildContext(context.Background(), builder, []Message{TextMessage(RoleAssistant, "new")})
	require.NoError(t, err)
	assert.Equal(t, 2, store.appendCalls)
}

// conflictOnceAppendStore returns ErrHistoryVersionConflict on the first Append only.
type conflictOnceAppendStore struct {
	appendCalls int
}

func (s *conflictOnceAppendStore) Load(context.Context, string) (HistorySnapshot, error) {
	return HistorySnapshot{
		Messages: []Message{TextMessage(RoleUser, "old")},
		Version:  0,
	}, nil
}

func (s *conflictOnceAppendStore) Append(_ context.Context, _ string, _ int64, _ ...Message) error {
	s.appendCalls++
	if s.appendCalls == 1 {
		return ErrHistoryVersionConflict
	}
	return nil
}

func (s *conflictOnceAppendStore) Save(context.Context, string, int64, []Message) error {
	return nil
}

func (s *conflictOnceAppendStore) Clear(context.Context, string, int64) error {
	return nil
}

func TestThreadBuildContext_PropagatesStoreAndBuilderErrors(t *testing.T) {
	t.Run("load", func(t *testing.T) {
		store := &stubHistoryStore{loadErr: errors.New("boom")}
		thread, err := NewThread(store, "thread-1", "history")
		require.NoError(t, err)

		builder := NewBuilder(100, &FixedCounter{TokensPerMessage: 10})
		builder.AddBlock("history", MemoryBlock{
			Strategy: NewStrictStrategy(),
			Messages: []Message{TextMessage(RoleUser, "placeholder")},
		})

		_, err = thread.BuildContext(context.Background(), builder, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "thread load")
	})

	t.Run("append", func(t *testing.T) {
		store := &stubHistoryStore{
			loadData:  []Message{TextMessage(RoleUser, "old")},
			appendErr: errors.New("append failed"),
		}
		thread, err := NewThread(store, "thread-1", "history")
		require.NoError(t, err)

		builder := NewBuilder(100, &FixedCounter{TokensPerMessage: 10})
		builder.AddBlock("history", MemoryBlock{
			Strategy: NewStrictStrategy(),
			Messages: []Message{TextMessage(RoleUser, "placeholder")},
		})

		_, err = thread.BuildContext(context.Background(), builder, []Message{TextMessage(RoleAssistant, "new")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "thread persist")
	})

	t.Run("save", func(t *testing.T) {
		store := &stubHistoryStore{
			loadData: []Message{
				TextMessage(RoleUser, "old"),
				TextMessage(RoleAssistant, "older"),
			},
			saveErr: errors.New("save failed"),
		}
		thread, err := NewThread(store, "thread-1", "history")
		require.NoError(t, err)

		builder := NewBuilder(20, &FixedCounter{TokensPerMessage: 10})
		builder.AddBlock("history", MemoryBlock{
			Strategy: NewDropHeadStrategy(DropHeadConfig{}),
			Messages: []Message{TextMessage(RoleUser, "placeholder")},
		})

		_, err = thread.BuildContext(context.Background(), builder, []Message{TextMessage(RoleUser, "latest")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "thread persist")
	})

	t.Run("builder block missing", func(t *testing.T) {
		store := &stubHistoryStore{}
		thread, err := NewThread(store, "thread-1", "history")
		require.NoError(t, err)

		builder := NewBuilder(100, &FixedCounter{TokensPerMessage: 10})
		builder.AddBlock("other", MemoryBlock{
			Strategy: NewStrictStrategy(),
			Messages: []Message{TextMessage(RoleUser, "placeholder")},
		})

		_, err = thread.BuildContext(context.Background(), builder, nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrBlockNotFound)
	})
}
