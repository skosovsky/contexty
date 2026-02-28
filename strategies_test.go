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
		TextMessage("user", "a"),
		TextMessage("assistant", "b"),
	}
	originalTokens := 20 // 2 msgs * 10
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
			got, err := NewStrictStrategy().Apply(ctx, msgs, originalTokens, tt.limit, counter)
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
	_, err := NewStrictStrategy().Apply(ctx, []Message{TextMessage("user", "x")}, 1, 100, &FixedCounter{TokensPerMessage: 1})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestStrictStrategy_ContextDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	_, err := NewStrictStrategy().Apply(ctx, []Message{TextMessage("user", "x")}, 1, 100, &FixedCounter{TokensPerMessage: 1})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestDropStrategy(t *testing.T) {
	counter := &FixedCounter{TokensPerMessage: 5}
	msgs := []Message{TextMessage("user", "a"), TextMessage("assistant", "b")}
	originalTokens := 10 // 2 * 5
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
			got, err := NewDropStrategy().Apply(context.Background(), msgs, originalTokens, tt.limit, counter)
			require.NoError(t, err)
			assert.Len(t, got, tt.want)
		})
	}
}

func TestDropStrategy_TokenCounterError(t *testing.T) {
	// DropStrategy uses originalTokens only; it does not call counter. Use a strategy that would call counter, or test via builder.
	// DropStrategy.Apply with originalTokens never calls counter, so we test errorCounter via strict path (strict also doesn't call in Apply).
	// Actually Drop fits/exceeds is based on originalTokens vs limit, no Count. So errorCounter isn't used by Drop.
	// Keep test: pass errorCounter; Drop still doesn't call it. So this test would pass. Remove or change: we can pass 1 as originalTokens, 100 as limit; Drop returns msgs. So no error. So the test was for when counter is used - but Drop doesn't use counter. So let's just keep the signature and pass originalTokens 1, limit 100. The test expects error - but with new Drop we won't get error. So change test to expect no error and that we get the message back, since Drop doesn't call counter.
	got, err := NewDropStrategy().Apply(context.Background(), []Message{TextMessage("user", "x")}, 1, 100, errorCounter{})
	require.NoError(t, err) // Drop uses originalTokens only, does not call counter
	require.Len(t, got, 1)
}

func TestTruncateOldestStrategy(t *testing.T) {
	// 3 messages, 10 tokens each = 30 total
	msgs := []Message{
		TextMessage("user", "1"),
		TextMessage("assistant", "2"),
		TextMessage("user", "3"),
	}
	counter := &FixedCounter{TokensPerMessage: 10}
	originalTokens := 30

	t.Run("no options truncates one by one", func(t *testing.T) {
		s := NewTruncateOldestStrategy()
		got, err := s.Apply(context.Background(), msgs, originalTokens, 15, counter)
		require.NoError(t, err)
		assert.Len(t, got, 1)
		require.Len(t, got[0].Content, 1)
		assert.Equal(t, "3", got[0].Content[0].Text)
	})

	t.Run("keep pairs removes user+assistant together", func(t *testing.T) {
		s := NewTruncateOldestStrategy(KeepUserAssistantPairs(true))
		got, err := s.Apply(context.Background(), msgs, originalTokens, 15, counter)
		require.NoError(t, err)
		assert.Len(t, got, 1)
		require.Len(t, got[0].Content, 1)
		assert.Equal(t, "3", got[0].Content[0].Text)
	})

	t.Run("keep pairs with non-user first removes one at a time", func(t *testing.T) {
		msgsWithSystem := []Message{
			TextMessage("system", "0"),
			TextMessage("user", "1"),
			TextMessage("assistant", "2"),
		}
		s := NewTruncateOldestStrategy(KeepUserAssistantPairs(true))
		got, err := s.Apply(context.Background(), msgsWithSystem, 30, 25, counter)
		require.NoError(t, err)
		assert.Len(t, got, 2)
		require.Len(t, got[0].Content, 1)
		require.Len(t, got[1].Content, 1)
		assert.Equal(t, "1", got[0].Content[0].Text)
		assert.Equal(t, "2", got[1].Content[0].Text)
	})

	t.Run("keep pairs with non-user first can drop block when pair does not fit", func(t *testing.T) {
		msgsWithSystem := []Message{
			TextMessage("system", "0"),
			TextMessage("user", "1"),
			TextMessage("assistant", "2"),
		}
		s := NewTruncateOldestStrategy(KeepUserAssistantPairs(true))
		got, err := s.Apply(context.Background(), msgsWithSystem, 30, 15, counter)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("min messages drops block when cannot keep minimum", func(t *testing.T) {
		s := NewTruncateOldestStrategy(MinMessages(2))
		got, err := s.Apply(context.Background(), msgs, originalTokens, 15, counter)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("empty input", func(t *testing.T) {
		got, err := NewTruncateOldestStrategy().Apply(context.Background(), nil, 0, 10, counter)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("token counter error in loop propagates", func(t *testing.T) {
		// truncateOldestStrategy calls counter.Count(cur) only inside the loop (after each trim).
		// With 3 msgs, limit 15: first trim -> cur has 2 msgs, Count(cur) is 1st call. Fail on 1st call.
		failAfter := &failAfterNCallsCounter{n: 1, inner: &FixedCounter{TokensPerMessage: 10}}
		_, err := NewTruncateOldestStrategy().Apply(context.Background(), msgs, originalTokens, 15, failAfter)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "count failed")
	})

	t.Run("inconsistent token counter returns error", func(t *testing.T) {
		// badCounter returns 30 for 3 msgs, 100 for 2 msgs. After first trim cur has 2 msgs, Count returns 100 > originalTokens 30.
		badCounter := &inconsistentCounter{fullCount: 30, sliceCount: 100}
		_, err := NewTruncateOldestStrategy().Apply(context.Background(), msgs, originalTokens, 15, badCounter)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrTokenCountFailed)
		assert.Contains(t, err.Error(), "exceeded original")
	})
}

// failAfterNCallsCounter fails from the Nth Count call; used to trigger errors inside strategy loop.
type failAfterNCallsCounter struct {
	n     int
	calls int
	inner TokenCounter
}

func (f *failAfterNCallsCounter) Count(msgs []Message) (int, error) {
	f.calls++
	if f.calls >= f.n {
		return 0, errors.New("count failed")
	}
	return f.inner.Count(msgs)
}

// failWhenCountingCounter fails when Count is called with a single message matching role+summary text (for summarize strategy).
type failWhenCountingCounter struct {
	inner TokenCounter
}

func (f *failWhenCountingCounter) Count(msgs []Message) (int, error) {
	if len(msgs) == 1 && msgs[0].Role == "system" && len(msgs[0].Content) == 1 && msgs[0].Content[0].Text == "summary" {
		return 0, errors.New("count failed")
	}
	return f.inner.Count(msgs)
}

// inconsistentCounter returns fullCount for full block, sliceCount for a smaller slice; used to simulate removed > total.
type inconsistentCounter struct {
	calls      int
	fullCount  int // total for full block (e.g. 30)
	sliceCount int // returned for slice to trigger removed > total (e.g. 100)
}

func (c *inconsistentCounter) Count(msgs []Message) (int, error) {
	c.calls++
	if len(msgs) == 3 {
		return c.fullCount, nil
	}
	return c.sliceCount, nil
}

func TestNewSummarizeStrategy_NilPanics(t *testing.T) {
	assert.Panics(t, func() { NewSummarizeStrategy(nil) })
}

func TestSummarizeStrategy(t *testing.T) {
	msgs := []Message{TextMessage("user", "a"), TextMessage("assistant", "b")}
	counter := &FixedCounter{TokensPerMessage: 10}
	originalTokens := 20

	t.Run("fits passes through", func(t *testing.T) {
		sum := &mockSummarizer{}
		s := NewSummarizeStrategy(sum)
		got, err := s.Apply(context.Background(), msgs, originalTokens, 20, counter)
		require.NoError(t, err)
		assert.Len(t, got, 2)
		assert.False(t, sum.called)
	})

	t.Run("exceeds calls summarizer", func(t *testing.T) {
		sum := &mockSummarizer{result: TextMessage("system", "summary")}
		s := NewSummarizeStrategy(sum)
		got, err := s.Apply(context.Background(), msgs, originalTokens, 15, counter)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Len(t, got[0].Content, 1)
		assert.Equal(t, "summary", got[0].Content[0].Text)
		assert.True(t, sum.called)
	})

	t.Run("summary still too large drops block", func(t *testing.T) {
		sum := &mockSummarizer{result: TextMessage("system", "long summary")}
		s := NewSummarizeStrategy(sum)
		got, err := s.Apply(context.Background(), msgs, originalTokens, 0, counter)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("summarizer error propagates", func(t *testing.T) {
		sum := &mockSummarizer{err: errors.New("llm failed")}
		s := NewSummarizeStrategy(sum)
		_, err := s.Apply(context.Background(), msgs, originalTokens, 5, counter)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "llm failed")
	})

	t.Run("token counter error when counting summary propagates", func(t *testing.T) {
		sum := &mockSummarizer{result: TextMessage("system", "summary")}
		failOnSummary := &failWhenCountingCounter{inner: counter}
		s := NewSummarizeStrategy(sum)
		_, err := s.Apply(context.Background(), msgs, originalTokens, 5, failOnSummary)
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
