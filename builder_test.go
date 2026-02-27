package contexty

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompile_Validation(t *testing.T) {
	counter := &FixedCounter{1}
	tests := []struct {
		name   string
		maxTok int
		tc     TokenCounter
	}{
		{"MaxTokens zero", 0, counter},
		{"MaxTokens negative", -1, counter},
		{"TokenCounter nil", 100, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewBuilder(AllocatorConfig{MaxTokens: tt.maxTok, TokenCounter: tt.tc})
			_, _, err := b.Compile(context.Background())
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalidConfig)
		})
	}
}

// errorCounter is a test double that always returns an error from Count.
type errorCounter struct{}

func (errorCounter) Count(string) (int, error) {
	return 0, errors.New("boom")
}

func TestCompile_TokenCounterError(t *testing.T) {
	b := NewBuilder(AllocatorConfig{MaxTokens: 100, TokenCounter: errorCounter{}})
	b.AddBlock(MemoryBlock{
		ID: "x", Tier: TierSystem, Strategy: NewStrictStrategy(),
		Messages: []Message{{Role: "system", Content: "hello"}},
	})
	_, _, err := b.Compile(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTokenCountFailed)
}

func TestCompile_NilStrategy(t *testing.T) {
	t.Run("block does not fit", func(t *testing.T) {
		// Block has 2 msgs = 20 tokens; budget 15 so strategy would be applied -> nil Strategy triggers ErrNilStrategy.
		b := NewBuilder(AllocatorConfig{MaxTokens: 15, TokenCounter: &FixedCounter{10}})
		b.AddBlock(MemoryBlock{
			ID: "overflow", Tier: TierRAG, Strategy: nil,
			Messages: []Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}},
		})
		_, _, err := b.Compile(context.Background())
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNilStrategy)
	})
	t.Run("block fits budget", func(t *testing.T) {
		// Block fits (2 msgs = 20 tokens, budget 100). Early validation still rejects nil Strategy.
		b := NewBuilder(AllocatorConfig{MaxTokens: 100, TokenCounter: &FixedCounter{10}})
		b.AddBlock(MemoryBlock{
			ID: "fits", Tier: TierRAG, Strategy: nil,
			Messages: []Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}},
		})
		_, _, err := b.Compile(context.Background())
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNilStrategy)
	})
}

func TestCompile_EmptyBuilder(t *testing.T) {
	b := NewBuilder(AllocatorConfig{MaxTokens: 100, TokenCounter: &FixedCounter{1}})
	msgs, report, err := b.Compile(context.Background())
	require.NoError(t, err)
	assert.Empty(t, msgs)
	assert.Zero(t, report.TotalTokensUsed)
	assert.Empty(t, report.BlocksDropped)
}

func TestCompile_BuilderReuse(t *testing.T) {
	counter := &FixedCounter{TokensPerMessage: 10}
	b := NewBuilder(AllocatorConfig{MaxTokens: 100, TokenCounter: counter})
	b.AddBlock(MemoryBlock{
		ID: "a", Tier: TierSystem, Strategy: NewStrictStrategy(),
		Messages: []Message{{Role: "system", Content: "first"}},
	})
	msgs1, report1, err := b.Compile(context.Background())
	require.NoError(t, err)
	require.Len(t, msgs1, 1)
	assert.Equal(t, "first", msgs1[0].Content)
	assert.Equal(t, 10, report1.TotalTokensUsed)

	b.AddBlock(MemoryBlock{
		ID: "b", Tier: TierCore, Strategy: NewStrictStrategy(),
		Messages: []Message{{Role: "system", Content: "second"}},
	})
	msgs2, report2, err := b.Compile(context.Background())
	require.NoError(t, err)
	require.Len(t, msgs2, 2)
	assert.Equal(t, "first", msgs2[0].Content)
	assert.Equal(t, "second", msgs2[1].Content)
	assert.Equal(t, 20, report2.TotalTokensUsed)
	assert.Equal(t, 10, report2.TokensPerBlock["a"])
	assert.Equal(t, 10, report2.TokensPerBlock["b"])
}

func TestCompile_OneBlockFits(t *testing.T) {
	b := NewBuilder(AllocatorConfig{MaxTokens: 100, TokenCounter: &FixedCounter{10}})
	b.AddBlock(MemoryBlock{
		ID: "a", Tier: TierSystem, Strategy: NewStrictStrategy(),
		Messages: []Message{{Role: "user", Content: "x"}, {Role: "assistant", Content: "y"}},
	})
	msgs, report, err := b.Compile(context.Background())
	require.NoError(t, err)
	assert.Len(t, msgs, 2)
	assert.Equal(t, 20, report.TotalTokensUsed)
	assert.Equal(t, 20, report.TokensPerBlock["a"])
	assert.Empty(t, report.BlocksDropped)
}

func TestCompile_TierOrdering(t *testing.T) {
	b := NewBuilder(AllocatorConfig{MaxTokens: 100, TokenCounter: &FixedCounter{1}})
	b.AddBlock(MemoryBlock{ID: "history", Tier: TierHistory, Strategy: NewDropStrategy(), Messages: []Message{{Role: "user", Content: "h"}}})
	b.AddBlock(MemoryBlock{ID: "system", Tier: TierSystem, Strategy: NewStrictStrategy(), Messages: []Message{{Role: "system", Content: "s"}}})
	msgs, _, err := b.Compile(context.Background())
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, "system", msgs[0].Role)
	assert.Equal(t, "user", msgs[1].Role)
}

func TestCompile_SameTierInsertionOrder(t *testing.T) {
	b := NewBuilder(AllocatorConfig{MaxTokens: 100, TokenCounter: &FixedCounter{1}})
	b.AddBlock(MemoryBlock{ID: "first", Tier: TierCore, Strategy: NewDropStrategy(), Messages: []Message{{Role: "system", Content: "1"}}})
	b.AddBlock(MemoryBlock{ID: "second", Tier: TierCore, Strategy: NewDropStrategy(), Messages: []Message{{Role: "system", Content: "2"}}})
	msgs, _, err := b.Compile(context.Background())
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, "1", msgs[0].Content)
	assert.Equal(t, "2", msgs[1].Content)
}

func TestCompile_StrictOverflow(t *testing.T) {
	b := NewBuilder(AllocatorConfig{MaxTokens: 5, TokenCounter: &FixedCounter{10}})
	b.AddBlock(MemoryBlock{ID: "sys", Tier: TierSystem, Strategy: NewStrictStrategy(), Messages: []Message{{Role: "system", Content: "big"}}})
	_, _, err := b.Compile(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBudgetExceeded)
}

func TestCompile_DropOverflow(t *testing.T) {
	// Budget 15: core (1 msg = 10) fits; rag (2 msgs = 20) exceeds remaining 5 and is dropped.
	b := NewBuilder(AllocatorConfig{MaxTokens: 15, TokenCounter: &FixedCounter{10}})
	b.AddBlock(MemoryBlock{ID: "rag", Tier: TierRAG, Strategy: NewDropStrategy(), Messages: []Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}}})
	b.AddBlock(MemoryBlock{ID: "core", Tier: TierCore, Strategy: NewDropStrategy(), Messages: []Message{{Role: "system", Content: "c"}}})
	msgs, report, err := b.Compile(context.Background())
	require.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, "c", msgs[0].Content)
	assert.Contains(t, report.BlocksDropped, "rag")
}

func TestCompile_TruncateHistory(t *testing.T) {
	b := NewBuilder(AllocatorConfig{MaxTokens: 25, TokenCounter: &FixedCounter{10}})
	b.AddBlock(MemoryBlock{
		ID: "chat", Tier: TierHistory, Strategy: NewTruncateOldestStrategy(),
		Messages: []Message{
			{Role: "user", Content: "1"},
			{Role: "assistant", Content: "2"},
			{Role: "user", Content: "3"},
		},
	})
	msgs, report, err := b.Compile(context.Background())
	require.NoError(t, err)
	assert.LessOrEqual(t, len(msgs), 2)
	assert.Equal(t, "truncated", report.Evictions["chat"])
	total := report.TotalTokensUsed
	assert.LessOrEqual(t, total, 25)
}

func TestCompile_BlockWithEmptyMessagesSkipped(t *testing.T) {
	b := NewBuilder(AllocatorConfig{MaxTokens: 100, TokenCounter: &FixedCounter{1}})
	b.AddBlock(MemoryBlock{ID: "empty", Tier: TierRAG, Strategy: NewDropStrategy(), Messages: nil})
	b.AddBlock(MemoryBlock{ID: "ok", Tier: TierCore, Strategy: NewDropStrategy(), Messages: []Message{{Role: "system", Content: "x"}}})
	msgs, report, err := b.Compile(context.Background())
	require.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, "x", msgs[0].Content)
	_, has := report.TokensPerBlock["empty"]
	assert.False(t, has)
}

func TestCompile_ContextCanceled(t *testing.T) {
	b := NewBuilder(AllocatorConfig{MaxTokens: 1000, TokenCounter: &FixedCounter{1}})
	b.AddBlock(MemoryBlock{ID: "a", Tier: TierSystem, Strategy: NewStrictStrategy(), Messages: []Message{{Role: "system", Content: "x"}}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := b.Compile(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestCompile_ReportAccuracy(t *testing.T) {
	b := NewBuilder(AllocatorConfig{MaxTokens: 15, TokenCounter: &FixedCounter{10}})
	b.AddBlock(MemoryBlock{ID: "must", Tier: TierSystem, Strategy: NewStrictStrategy(), Messages: []Message{{Role: "system", Content: "s"}}})
	b.AddBlock(MemoryBlock{ID: "drop", Tier: TierRAG, Strategy: NewDropStrategy(), Messages: []Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}}})
	msgs, report, err := b.Compile(context.Background())
	require.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, 10, report.TotalTokensUsed)
	assert.Equal(t, 30, report.OriginalTokens)
	assert.Equal(t, 10, report.TokensPerBlock["must"])
	assert.Contains(t, report.BlocksDropped, "drop")
	assert.Equal(t, "dropped", report.Evictions["drop"])
}

// customStrategy is a test double that implements EvictionStrategy to exercise evictionLabel default.
type customStrategy struct{}

func (customStrategy) Apply(ctx context.Context, msgs []Message, limit int, counter TokenCounter) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n, _ := countBlockTokens(counter, msgs)
	if n <= limit {
		return msgs, nil
	}
	return nil, nil
}

func TestCompile_CustomStrategyEvictionLabel(t *testing.T) {
	b := NewBuilder(AllocatorConfig{MaxTokens: 5, TokenCounter: &FixedCounter{10}})
	b.AddBlock(MemoryBlock{ID: "custom", Tier: TierRAG, Strategy: customStrategy{}, Messages: []Message{{Role: "user", Content: "x"}}})
	_, report, err := b.Compile(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "evicted", report.Evictions["custom"])
}

func TestCompile_DuplicateBlockID(t *testing.T) {
	b := NewBuilder(AllocatorConfig{MaxTokens: 100, TokenCounter: &FixedCounter{1}})
	b.AddBlock(MemoryBlock{ID: "dup", Tier: TierCore, Strategy: NewDropStrategy(), Messages: []Message{{Role: "system", Content: "first"}}})
	b.AddBlock(MemoryBlock{ID: "dup", Tier: TierCore, Strategy: NewDropStrategy(), Messages: []Message{{Role: "system", Content: "second"}}})
	msgs, report, err := b.Compile(context.Background())
	require.NoError(t, err)
	assert.Len(t, msgs, 2)
	// Last block overwrites in report (same ID).
	assert.Contains(t, report.TokensPerBlock, "dup")
	assert.Equal(t, 1, report.TokensPerBlock["dup"])
}

func FuzzCompile(f *testing.F) {
	f.Add(50, 5, 10) // maxTokens, numBlocks, tokensPerMsg
	f.Fuzz(func(t *testing.T, maxTokens, numBlocks, tokensPerMsg int) {
		if maxTokens <= 0 || numBlocks <= 0 || numBlocks > 20 || tokensPerMsg <= 0 || tokensPerMsg > 100 {
			t.Skip()
		}
		counter := &FixedCounter{TokensPerMessage: tokensPerMsg}
		b := NewBuilder(AllocatorConfig{MaxTokens: maxTokens, TokenCounter: counter})
		for i := 0; i < numBlocks; i++ {
			b.AddBlock(MemoryBlock{
				ID: "b", Tier: TierHistory, Strategy: NewDropStrategy(),
				Messages: []Message{{Role: "user", Content: "x"}},
			})
		}
		msgs, report, err := b.Compile(context.Background())
		if err != nil {
			return
		}
		if report.TotalTokensUsed > maxTokens {
			t.Errorf("TotalTokensUsed %d > MaxTokens %d", report.TotalTokensUsed, maxTokens)
		}
		if len(msgs) > numBlocks {
			t.Errorf("len(msgs)=%d > numBlocks=%d", len(msgs), numBlocks)
		}
	})
}

func BenchmarkCompile(b *testing.B) {
	counter := &FixedCounter{TokensPerMessage: 10}
	blocks := make([]MemoryBlock, 0, 10)
	for i := 0; i < 10; i++ {
		blocks = append(blocks, MemoryBlock{
			ID: "b", Tier: TierHistory, Strategy: NewTruncateOldestStrategy(),
			Messages: []Message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Hi there!"},
			},
		})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		build := NewBuilder(AllocatorConfig{MaxTokens: 500, TokenCounter: counter})
		for _, blk := range blocks {
			build.AddBlock(blk)
		}
		_, _, _ = build.Compile(context.Background())
	}
}
