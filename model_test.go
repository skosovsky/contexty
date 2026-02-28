package contexty

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTier_String(t *testing.T) {
	tests := []struct {
		tier Tier
		want string
	}{
		{TierSystem, "system"},
		{TierCore, "core"},
		{TierRAG, "rag"},
		{TierHistory, "history"},
		{TierScratchpad, "scratchpad"},
		{Tier(42), "Tier(42)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.tier.String())
		})
	}
}

func TestTokenCounter_CountMessages(t *testing.T) {
	t.Run("FixedCounter empty slice", func(t *testing.T) {
		counter := &FixedCounter{TokensPerMessage: 5}
		n, err := counter.Count(nil)
		require.NoError(t, err)
		assert.Equal(t, 0, n)
		n, err = counter.Count([]Message{})
		require.NoError(t, err)
		assert.Equal(t, 0, n)
	})
	t.Run("FixedCounter single message", func(t *testing.T) {
		counter := &FixedCounter{TokensPerMessage: 5}
		msgs := []Message{TextMessage("user", "hi")}
		n, err := counter.Count(msgs)
		require.NoError(t, err)
		assert.Equal(t, 5, n)
	})
	t.Run("FixedCounter message with Name", func(t *testing.T) {
		counter := &FixedCounter{TokensPerMessage: 5}
		msgs := []Message{{Role: "tool", Name: "get_weather", Content: []ContentPart{{Type: "text", Text: "sunny"}}}}
		n, err := counter.Count(msgs)
		require.NoError(t, err)
		assert.Equal(t, 5, n)
	})
	t.Run("FixedCounter two messages", func(t *testing.T) {
		counter := &FixedCounter{TokensPerMessage: 5}
		msgs := []Message{TextMessage("user", "a"), TextMessage("assistant", "b")}
		n, err := counter.Count(msgs)
		require.NoError(t, err)
		assert.Equal(t, 10, n)
	})
	t.Run("CharFallbackCounter Name included in token count", func(t *testing.T) {
		c := &CharFallbackCounter{CharsPerToken: 4}
		withoutName := []Message{{Role: "tool", Content: []ContentPart{{Type: "text", Text: "result"}}}}
		withName := []Message{{Role: "tool", Name: "get_weather", Content: []ContentPart{{Type: "text", Text: "result"}}}}
		nWithout, err := c.Count(withoutName)
		require.NoError(t, err)
		nWith, err := c.Count(withName)
		require.NoError(t, err)
		assert.Greater(t, nWith, nWithout, "message with Name should yield more tokens than without")
	})
}
