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

	t.Run("keep pairs with non-user first removes one by one", func(t *testing.T) {
		// First message is "system", so not a user+assistant pair; remove one at a time.
		withSystem := []Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "1"},
			{Role: "assistant", Content: "2"},
		}
		s := NewTruncateOldestStrategy(KeepUserAssistantPairs(true))
		got, err := s.Apply(context.Background(), withSystem, 25, counter)
		require.NoError(t, err)
		// System (10) removed first; user+assistant (20) fits in 25
		require.Len(t, got, 2)
		assert.Equal(t, "user", got[0].Role)
		assert.Equal(t, "assistant", got[1].Role)
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
