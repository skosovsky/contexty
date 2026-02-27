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

func TestCountBlockTokens(t *testing.T) {
	counter := &FixedCounter{TokensPerMessage: 5}
	t.Run("empty slice", func(t *testing.T) {
		n, err := countBlockTokens(counter, nil)
		require.NoError(t, err)
		assert.Equal(t, 0, n)
		n, err = countBlockTokens(counter, []Message{})
		require.NoError(t, err)
		assert.Equal(t, 0, n)
	})
	t.Run("single message", func(t *testing.T) {
		msgs := []Message{{Role: "user", Content: "hi"}}
		n, err := countBlockTokens(counter, msgs)
		require.NoError(t, err)
		assert.Equal(t, 5, n)
	})
	t.Run("message with Name", func(t *testing.T) {
		msgs := []Message{{Role: "tool", Name: "get_weather", Content: "sunny"}}
		n, err := countBlockTokens(counter, msgs)
		require.NoError(t, err)
		assert.Equal(t, 5, n)
	})
	t.Run("two messages", func(t *testing.T) {
		msgs := []Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}}
		n, err := countBlockTokens(counter, msgs)
		require.NoError(t, err)
		assert.Equal(t, 10, n)
	})
	t.Run("Name included in token count", func(t *testing.T) {
		// CharFallbackCounter counts by rune length; Name is prepended to the text, so more chars = more tokens.
		c := &CharFallbackCounter{CharsPerToken: 4}
		withoutName := []Message{{Role: "tool", Content: "result"}}
		withName := []Message{{Role: "tool", Name: "get_weather", Content: "result"}}
		nWithout, err := countBlockTokens(c, withoutName)
		require.NoError(t, err)
		nWith, err := countBlockTokens(c, withName)
		require.NoError(t, err)
		assert.Greater(t, nWith, nWithout, "message with Name should yield more tokens than without")
	})
}
