package contexty

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type formatterFunc func(context.Context, []NamedBlock) ([]Message, error)

func (f formatterFunc) Format(ctx context.Context, blocks []NamedBlock) ([]Message, error) {
	return f(ctx, blocks)
}

type stubSummarizer struct {
	calls  int
	result Message
	err    error
}

func (s *stubSummarizer) Summarize(_ context.Context, _ []Message) (Message, error) {
	s.calls++
	if s.err != nil {
		return Message{}, s.err
	}
	return s.result, nil
}

type mutationCheckingFormatter struct {
	calls int
}

type invalidBudgetStrategy struct {
	out []Message
}

func (f *mutationCheckingFormatter) Format(ctx context.Context, blocks []NamedBlock) ([]Message, error) {
	if got := blocks[0].Block.Messages[0].Content[0].Text; got != "original" {
		return nil, errors.New("formatter mutation leaked into next build")
	}
	blocks[0].Block.Messages[0].Content[0].Text = "mutated"
	f.calls++
	return DefaultFormatter{}.Format(ctx, blocks)
}

func (s invalidBudgetStrategy) Apply(_ context.Context, _ []Message, _ int, _ int, _ TokenCounter) ([]Message, error) {
	return cloneMessages(s.out), nil
}

func TestBuild_InvalidConfig(t *testing.T) {
	t.Run("max tokens must be positive", func(t *testing.T) {
		_, err := NewBuilder(0, &FixedCounter{TokensPerMessage: 1}).Build(context.Background())
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidConfig)
	})

	t.Run("token counter is required", func(t *testing.T) {
		_, err := NewBuilder(10, nil).Build(context.Background())
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidConfig)
	})
}

func TestAddBlock_EmptyNamePanics(t *testing.T) {
	b := NewBuilder(10, &FixedCounter{TokensPerMessage: 1})
	assert.Panics(t, func() {
		b.AddBlock("", MemoryBlock{Strategy: NewStrictStrategy()})
	})
}

func TestWithFormatter_NilPanics(t *testing.T) {
	b := NewBuilder(10, &FixedCounter{TokensPerMessage: 1})
	assert.Panics(t, func() {
		b.WithFormatter(nil)
	})
}

func TestBuild_OrderOnlyPriority(t *testing.T) {
	b := NewBuilder(10, &FixedCounter{TokensPerMessage: 10})
	b.AddBlock("first", MemoryBlock{
		Strategy: NewDropStrategy(),
		Messages: []Message{TextMessage("system", "first")},
	})
	b.AddBlock("second", MemoryBlock{
		Strategy: NewDropStrategy(),
		Messages: []Message{TextMessage("system", "second")},
	})

	msgs, err := b.Build(context.Background())
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "first", msgs[0].Content[0].Text)
}

func TestBuild_NilStrategyErrorIncludesName(t *testing.T) {
	b := NewBuilder(10, &FixedCounter{TokensPerMessage: 10})
	b.AddBlock("broken", MemoryBlock{
		Messages: []Message{TextMessage("system", "x")},
	})

	_, err := b.Build(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNilStrategy)
	assert.Contains(t, err.Error(), `block "broken"`)
}

func TestBuild_IsRepeatableAcrossStrategies(t *testing.T) {
	sum := &stubSummarizer{result: TextMessage("system", "summary")}
	b := NewBuilder(100, &FixedCounter{TokensPerMessage: 10})
	b.AddBlock("keep", MemoryBlock{
		Strategy: NewStrictStrategy(),
		Messages: []Message{TextMessage("system", "keep")},
	})
	b.AddBlock("drop", MemoryBlock{
		Strategy:  NewDropStrategy(),
		MaxTokens: 10,
		Messages: []Message{
			TextMessage("system", "drop-1"),
			TextMessage("system", "drop-2"),
		},
	})
	b.AddBlock("truncate", MemoryBlock{
		Strategy:  NewTruncateOldestStrategy(),
		MaxTokens: 10,
		Messages: []Message{
			TextMessage("user", "old"),
			TextMessage("assistant", "new"),
		},
	})
	b.AddBlock("summary", MemoryBlock{
		Strategy:  NewSummarizeStrategy(sum),
		MaxTokens: 10,
		Messages: []Message{
			TextMessage("user", "long"),
			TextMessage("assistant", "block"),
		},
	})
	b.AddBlock("tail", MemoryBlock{
		Strategy:  NewDropTailStrategy(),
		MaxTokens: 20,
		Messages: []Message{
			TextMessage("system", "tail-1"),
			TextMessage("system", "tail-2"),
			TextMessage("system", "tail-3"),
		},
	})

	first, err := b.Build(context.Background())
	require.NoError(t, err)
	second, err := b.Build(context.Background())
	require.NoError(t, err)

	require.Len(t, first, 5)
	require.Len(t, second, 5)
	assert.Equal(t, first, second)
	assert.Equal(t, []string{"keep", "new", "summary", "tail-1", "tail-2"}, []string{
		first[0].Content[0].Text,
		first[1].Content[0].Text,
		first[2].Content[0].Text,
		first[3].Content[0].Text,
		first[4].Content[0].Text,
	})
	assert.Equal(t, 2, sum.calls)
}

func TestBuild_AddBlockSnapshotsInput(t *testing.T) {
	block := MemoryBlock{
		Strategy: NewStrictStrategy(),
		Messages: []Message{{
			Role: "system",
			Content: []ContentPart{
				{Type: "text", Text: "original"},
				{Type: "image_url", ImageURL: &ImageURL{URL: "https://example.com/original.png", Detail: "low"}},
			},
			Metadata: map[string]any{"scope": "original"},
		}},
		CacheControl: map[string]any{"type": "ephemeral"},
	}

	b := NewBuilder(20, &FixedCounter{TokensPerMessage: 10})
	b.AddBlock("snapshot", block)

	block.Messages[0].Content[0].Text = "mutated"
	block.Messages[0].Content[1].ImageURL.URL = "https://example.com/mutated.png"
	block.Messages[0].Metadata["scope"] = "mutated"
	block.CacheControl["type"] = "changed"

	msgs, err := b.Build(context.Background())
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "original", msgs[0].Content[0].Text)
	require.NotNil(t, msgs[0].Content[1].ImageURL)
	assert.Equal(t, "https://example.com/original.png", msgs[0].Content[1].ImageURL.URL)
	assert.Equal(t, "original", msgs[0].Metadata["scope"])
	assert.Equal(t, "ephemeral", msgs[0].CacheControl["type"])
}

func TestBuild_FormatterMutationDoesNotAffectFutureBuilds(t *testing.T) {
	formatter := &mutationCheckingFormatter{}
	b := NewBuilder(10, &FixedCounter{TokensPerMessage: 10})
	b.AddBlock("snapshot", MemoryBlock{
		Strategy: NewStrictStrategy(),
		Messages: []Message{TextMessage("system", "original")},
	})
	b.WithFormatter(formatter)

	first, err := b.Build(context.Background())
	require.NoError(t, err)
	require.Len(t, first, 1)
	assert.Equal(t, "mutated", first[0].Content[0].Text)

	second, err := b.Build(context.Background())
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.Equal(t, "mutated", second[0].Content[0].Text)
	assert.Equal(t, 2, formatter.calls)
}

func TestBuild_FormatterOverBudget(t *testing.T) {
	b := NewBuilder(10, &FixedCounter{TokensPerMessage: 10})
	b.AddBlock("only", MemoryBlock{
		Strategy: NewStrictStrategy(),
		Messages: []Message{TextMessage("system", "x")},
	})
	b.WithFormatter(formatterFunc(func(_ context.Context, _ []NamedBlock) ([]Message, error) {
		return []Message{
			TextMessage("system", "x"),
			TextMessage("system", "y"),
		}, nil
	}))

	_, err := b.Build(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, ErrFormatterExceededBudget)
}

func TestBuild_StrategyCannotExceedLocalBlockBudget(t *testing.T) {
	b := NewBuilder(30, &FixedCounter{TokensPerMessage: 10})
	b.AddBlock("limited", MemoryBlock{
		Strategy: invalidBudgetStrategy{
			out: []Message{
				TextMessage("system", "first"),
				TextMessage("system", "second"),
			},
		},
		MaxTokens: 10,
		Messages: []Message{
			TextMessage("system", "first"),
			TextMessage("system", "second"),
		},
	})

	_, err := b.Build(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, ErrStrategyExceededBudget)
}

func TestBuild_DefaultFormatterPreservesOrderAndCacheControl(t *testing.T) {
	b := NewBuilder(30, &FixedCounter{TokensPerMessage: 10})
	b.AddBlock("alpha", MemoryBlock{
		Strategy: NewStrictStrategy(),
		Messages: []Message{
			TextMessage("system", "alpha-1"),
			TextMessage("system", "alpha-2"),
		},
		CacheControl: map[string]any{"type": "ephemeral"},
	})
	b.AddBlock("beta", MemoryBlock{
		Strategy: NewStrictStrategy(),
		Messages: []Message{
			TextMessage("system", "beta-1"),
		},
		CacheControl: map[string]any{"mode": "sticky"},
	})

	msgs, err := b.Build(context.Background())
	require.NoError(t, err)
	require.Len(t, msgs, 3)
	assert.Equal(t, "alpha-1", msgs[0].Content[0].Text)
	assert.Nil(t, msgs[0].CacheControl)
	assert.Equal(t, "alpha-2", msgs[1].Content[0].Text)
	assert.Equal(t, "ephemeral", msgs[1].CacheControl["type"])
	assert.Equal(t, "beta-1", msgs[2].Content[0].Text)
	assert.Equal(t, "sticky", msgs[2].CacheControl["mode"])
}

func TestBuild_CustomFormatterReceivesNamedBlocks(t *testing.T) {
	b := NewBuilder(20, &FixedCounter{TokensPerMessage: 10})
	b.AddBlock("first", MemoryBlock{
		Strategy: NewStrictStrategy(),
		Messages: []Message{TextMessage("system", "a")},
	})
	b.AddBlock("second", MemoryBlock{
		Strategy: NewStrictStrategy(),
		Messages: []Message{TextMessage("system", "b")},
	})

	var gotNames []string
	b.WithFormatter(formatterFunc(func(ctx context.Context, blocks []NamedBlock) ([]Message, error) {
		for _, block := range blocks {
			gotNames = append(gotNames, block.Name)
		}
		return DefaultFormatter{}.Format(ctx, blocks)
	}))

	_, err := b.Build(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"first", "second"}, gotNames)
}

func TestBuild_PassesContextToFormatter(t *testing.T) {
	type ctxKey string

	const key ctxKey = "trace"
	b := NewBuilder(10, &FixedCounter{TokensPerMessage: 10})
	b.AddBlock("only", MemoryBlock{
		Strategy: NewStrictStrategy(),
		Messages: []Message{TextMessage("system", "x")},
	})

	var got any
	b.WithFormatter(formatterFunc(func(ctx context.Context, blocks []NamedBlock) ([]Message, error) {
		got = ctx.Value(key)
		return DefaultFormatter{}.Format(ctx, blocks)
	}))

	_, err := b.Build(context.WithValue(context.Background(), key, "span-123"))
	require.NoError(t, err)
	assert.Equal(t, "span-123", got)
}
