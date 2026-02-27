package contexty

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStrictStrategy(t *testing.T) {
	counter := &FixedCounter{TokensPerMessage: 10}
	msgs := []Message{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
	}
	// Each message counted as role+content; FixedCounter returns 10 per call, so countBlockTokens = 10+10 = 20.
	// Actually countBlockTokens passes "Role\nContent" so it's one Count per message -> 10 per message -> 20 total.
	tests := []struct {
		name    string
		limit   int
		want    int
		wantErr bool
	}{
		{"fits", 20, 2, false},
		{"fits with spare", 100, 2, false},
		{"exceeds", 19, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			got, err := NewStrictStrategy().Apply(ctx, msgs, tt.limit, counter)
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrBudgetExceeded)
				return
			}
			require.NoError(t, err)
			assert.Len(t, got, tt.want)
		})
	}
}

func TestStrictStrategy_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewStrictStrategy().Apply(ctx, []Message{{Role: "user", Content: "x"}}, 100, &FixedCounter{1})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestStrictStrategy_ContextDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	_, err := NewStrictStrategy().Apply(ctx, []Message{{Role: "user", Content: "x"}}, 100, &FixedCounter{1})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestDropStrategy(t *testing.T) {
	counter := &FixedCounter{TokensPerMessage: 5}
	msgs := []Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}}
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{"fits", 20, 2},
		{"exceeds returns empty", 9, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewDropStrategy().Apply(context.Background(), msgs, tt.limit, counter)
			require.NoError(t, err)
			assert.Len(t, got, tt.want)
		})
	}
}

func TestDropStrategy_TokenCounterError(t *testing.T) {
	_, err := NewDropStrategy().Apply(context.Background(), []Message{{Role: "user", Content: "x"}}, 100, errorCounter{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestTruncateOldestStrategy(t *testing.T) {
	// 3 messages, 10 tokens each = 30 total
	msgs := []Message{
		{Role: "user", Content: "1"},
		{Role: "assistant", Content: "2"},
		{Role: "user", Content: "3"},
	}
	counter := &FixedCounter{TokensPerMessage: 10}

	t.Run("no options truncates one by one", func(t *testing.T) {
		s := NewTruncateOldestStrategy()
		got, err := s.Apply(context.Background(), msgs, 15, counter)
		require.NoError(t, err)
		assert.Len(t, got, 1)
		assert.Equal(t, "3", got[0].Content)
	})

	t.Run("keep pairs removes user+assistant together", func(t *testing.T) {
		s := NewTruncateOldestStrategy(KeepUserAssistantPairs(true))
		got, err := s.Apply(context.Background(), msgs, 15, counter)
		require.NoError(t, err)
		// First pair (user, assistant) removed -> one message left
		assert.Len(t, got, 1)
		assert.Equal(t, "3", got[0].Content)
	})

	t.Run("keep pairs with non-user first removes one at a time", func(t *testing.T) {
		// First message is "system", so no user+assistant pair at start; only one message removed per iteration until it fits.
		msgsWithSystem := []Message{
			{Role: "system", Content: "0"},
			{Role: "user", Content: "1"},
			{Role: "assistant", Content: "2"},
		}
		s := NewTruncateOldestStrategy(KeepUserAssistantPairs(true))
		got, err := s.Apply(context.Background(), msgsWithSystem, 25, counter)
		require.NoError(t, err)
		// system (10) removed first; remaining 20 <= 25, stop. Two messages (user, assistant) kept.
		assert.Len(t, got, 2)
		assert.Equal(t, "1", got[0].Content)
		assert.Equal(t, "2", got[1].Content)
	})

	t.Run("keep pairs with non-user first can drop block when pair does not fit", func(t *testing.T) {
		// Same 3 msgs, limit 15: system removed (1), then [user, assistant] pair exceeds 15 -> pair removed -> empty.
		msgsWithSystem := []Message{
			{Role: "system", Content: "0"},
			{Role: "user", Content: "1"},
			{Role: "assistant", Content: "2"},
		}
		s := NewTruncateOldestStrategy(KeepUserAssistantPairs(true))
		got, err := s.Apply(context.Background(), msgsWithSystem, 15, counter)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("min messages drops block when cannot keep minimum", func(t *testing.T) {
		s := NewTruncateOldestStrategy(MinMessages(2))
		got, err := s.Apply(context.Background(), msgs, 15, counter)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("empty input", func(t *testing.T) {
		got, err := NewTruncateOldestStrategy().Apply(context.Background(), nil, 10, counter)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("token counter error in loop propagates", func(t *testing.T) {
		// Fail on 4th call: 3 for countBlockTokens(cur), 1 for first countBlockTokens(cur[:remove]).
		failAfter := &failAfterNCallsCounter{n: 4, inner: &FixedCounter{TokensPerMessage: 10}}
		_, err := NewTruncateOldestStrategy().Apply(context.Background(), msgs, 15, failAfter)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "count failed")
	})

	t.Run("inconsistent token counter returns error", func(t *testing.T) {
		// Counter reports 100 for first message slice but 30 for full block -> removedTokens > total.
		badCounter := &inconsistentCounter{fullCount: 30, sliceCount: 100}
		_, err := NewTruncateOldestStrategy().Apply(context.Background(), msgs, 15, badCounter)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrTokenCountFailed)
		assert.Contains(t, err.Error(), "inconsistent")
	})
}

// failAfterNCallsCounter fails from the Nth Count call; used to trigger errors inside strategy loop.
type failAfterNCallsCounter struct {
	n     int
	calls int
	inner TokenCounter
}

func (f *failAfterNCallsCounter) Count(text string) (int, error) {
	f.calls++
	if f.calls >= f.n {
		return 0, errors.New("count failed")
	}
	return f.inner.Count(text)
}

// failWhenCountingCounter fails when Count is called with text equal to trigger.
type failWhenCountingCounter struct {
	trigger string
	inner   TokenCounter
}

func (f *failWhenCountingCounter) Count(text string) (int, error) {
	if text == f.trigger {
		return 0, errors.New("count failed")
	}
	return f.inner.Count(text)
}

// inconsistentCounter returns sliceCount for the first Count call in a batch, then fullCount/3 for the rest; used to simulate removed > total.
type inconsistentCounter struct {
	calls      int
	fullCount  int // total for full block (e.g. 30)
	sliceCount int // returned for first message when counting a slice (e.g. 100)
}

func (c *inconsistentCounter) Count(_ string) (int, error) {
	c.calls++
	// countBlockTokens(cur) does 3 calls -> 10 each so total 30. Then countBlockTokens(cur[:1]) does 1 call -> return 100.
	if c.calls <= 3 {
		return c.fullCount / 3, nil
	}
	return c.sliceCount, nil
}

func TestNewSummarizeStrategy_NilPanics(t *testing.T) {
	assert.Panics(t, func() { NewSummarizeStrategy(nil) })
}

func TestSummarizeStrategy(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}}
	counter := &FixedCounter{TokensPerMessage: 10}

	t.Run("fits passes through", func(t *testing.T) {
		sum := &mockSummarizer{}
		s := NewSummarizeStrategy(sum)
		got, err := s.Apply(context.Background(), msgs, 20, counter)
		require.NoError(t, err)
		assert.Len(t, got, 2)
		assert.False(t, sum.called)
	})

	t.Run("exceeds calls summarizer", func(t *testing.T) {
		sum := &mockSummarizer{result: Message{Role: "system", Content: "summary"}}
		s := NewSummarizeStrategy(sum)
		// Limit 15 so the summary (1 msg = 10 tokens with FixedCounter) fits.
		got, err := s.Apply(context.Background(), msgs, 15, counter)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "summary", got[0].Content)
		assert.True(t, sum.called)
	})

	t.Run("summary still too large drops block", func(t *testing.T) {
		sum := &mockSummarizer{result: Message{Role: "system", Content: "long summary"}}
		s := NewSummarizeStrategy(sum)
		got, err := s.Apply(context.Background(), msgs, 0, counter)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("summarizer error propagates", func(t *testing.T) {
		sum := &mockSummarizer{err: errors.New("llm failed")}
		s := NewSummarizeStrategy(sum)
		_, err := s.Apply(context.Background(), msgs, 5, counter)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "llm failed")
	})

	t.Run("token counter error when counting summary propagates", func(t *testing.T) {
		sum := &mockSummarizer{result: Message{Role: "system", Content: "summary"}}
		// Fail when counting the summary message (Role+"\n"+Content = "system\nsummary").
		failOnSummary := &failWhenCountingCounter{trigger: "system\nsummary", inner: counter}
		s := NewSummarizeStrategy(sum)
		_, err := s.Apply(context.Background(), msgs, 5, failOnSummary)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "count failed")
	})
}

type mockSummarizer struct {
	called bool
	result Message
	err    error
}

func (m *mockSummarizer) Summarize(_ context.Context, _ []Message) (Message, error) {
	m.called = true
	if m.err != nil {
		return Message{}, m.err
	}
	return m.result, nil
}
