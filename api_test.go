package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

// TestCompile_BlackBox runs an end-to-end scenario using only the public API.
func TestCompile_BlackBox(t *testing.T) {
	counter := &contexty.CharFallbackCounter{CharsPerToken: 4}
	b := contexty.NewBuilder(contexty.AllocatorConfig{
		MaxTokens:    200,
		TokenCounter: counter,
	})
	b.AddBlock(contexty.MemoryBlock{
		ID:       "persona",
		Tier:     contexty.TierSystem,
		Strategy: contexty.NewStrictStrategy(),
		Messages: []contexty.Message{contexty.TextMessage("system", "You are a medical assistant.")},
	})
	b.AddBlock(contexty.MemoryBlock{
		ID:       "facts",
		Tier:     contexty.TierCore,
		Strategy: contexty.NewDropStrategy(),
		Messages: []contexty.Message{contexty.TextMessage("system", "Patient: Anna, 30yo.")},
	})
	b.AddBlock(contexty.MemoryBlock{
		ID:       "chat",
		Tier:     contexty.TierHistory,
		Strategy: contexty.NewTruncateOldestStrategy(),
		Messages: []contexty.Message{
			contexty.TextMessage("user", "What should I take?"),
			contexty.TextMessage("assistant", "Consider vitamin D."),
			contexty.TextMessage("user", "Any side effects?"),
		},
	})
	msgs, report, err := b.Compile(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(msgs), 1)
	assert.LessOrEqual(t, report.TotalTokensUsed, 200)
	assert.NotNil(t, report.TokensPerBlock)
	assert.NotNil(t, report.Evictions)
}
