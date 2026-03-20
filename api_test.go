package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

// TestBuild_BlackBox runs an end-to-end scenario using only the public API.
func TestBuild_BlackBox(t *testing.T) {
	counter := &contexty.CharFallbackCounter{CharsPerToken: 4}
	builder := contexty.NewBuilder(200, counter)
	builder.AddBlock("instructions", contexty.MemoryBlock{
		Strategy: contexty.NewStrictStrategy(),
		Messages: []contexty.Message{
			contexty.TextMessage("system", "Keep answers concise."),
		},
	})
	builder.AddBlock("profile", contexty.MemoryBlock{
		Strategy:  contexty.NewDropTailStrategy(),
		MaxTokens: 20,
		Messages: []contexty.Message{
			contexty.TextMessage("system", "Name: Anna"),
			contexty.TextMessage("system", "Timezone: ICT"),
			contexty.TextMessage("system", "Prefers plain text"),
		},
	})
	builder.AddBlock("dialogue", contexty.MemoryBlock{
		Strategy: contexty.NewTruncateOldestStrategy(),
		Messages: []contexty.Message{
			contexty.TextMessage("user", "What should we do next?"),
			contexty.TextMessage("assistant", "Implement the builder refactor."),
			contexty.TextMessage("user", "Keep it compact."),
		},
	})

	msgs, err := builder.Build(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(msgs), 1)
	total, err := counter.Count(context.Background(), msgs)
	require.NoError(t, err)
	assert.LessOrEqual(t, total, 200)
}
