package contexty

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCharFallbackCounter_Count(t *testing.T) {
	tests := []struct {
		name          string
		charsPerToken int
		text          string
		want          int
		wantErr       bool
	}{
		{"empty string", 4, "", 0, false},
		{"ascii four chars", 4, "abcd", 1, false},
		{"ascii eight chars", 4, "abcdefgh", 2, false},
		{"ascii five chars ceil", 4, "hello", 2, false},
		{"single char", 4, "x", 1, false},
		{"chars per token 1", 1, "hello", 5, false},
		{"cyrillic", 4, "привет", 2, false},
		{"emoji", 4, "🔥", 1, false},
		{"mixed", 4, "Hi 世界", 2, false},
		{"zero chars per token", 0, "hello", 0, true},
		{"negative chars per token", -1, "hello", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &CharFallbackCounter{CharsPerToken: tt.charsPerToken}
			msgs := []Message{TextMessage("user", tt.text)}
			got, err := c.Count(context.Background(), msgs)
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidCharsPerToken)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got, "Count(%q) with CharsPerToken=%d", tt.text, tt.charsPerToken)
		})
	}
}

func TestFixedCounter_Count(t *testing.T) {
	ctx := context.Background()
	c := &FixedCounter{TokensPerMessage: 10}
	got, err := c.Count(ctx, []Message{TextMessage("user", "anything")})
	require.NoError(t, err)
	assert.Equal(t, 10, got)
	got, _ = c.Count(ctx, []Message{TextMessage("user", "")})
	assert.Equal(t, 10, got)
}

func TestCharFallbackCounter_Count_withEstimateTool(t *testing.T) {
	ctx := context.Background()
	// Message with text "ab" (2 runes) and one ToolCall. Without EstimateTool: (2+2)/4 = 1 token (Name empty, Arguments "{}" = 2 runes).
	msgs := []Message{{
		Role:    "assistant",
		Content: []ContentPart{{Type: "text", Text: "ab"}},
		ToolCalls: []ToolCall{{
			ID:       "call_1",
			Type:     "function",
			Function: FunctionCall{Name: "foo", Arguments: "{}"},
		}},
	}}
	// With EstimateTool returning 10 per call: text tokens 1 + tool 10 = 11.
	c := &CharFallbackCounter{CharsPerToken: 4, EstimateTool: func(_ ToolCall) int { return 10 }}
	got, err := c.Count(ctx, msgs)
	require.NoError(t, err)
	assert.Equal(t, 11, got, "text tokens (1) + EstimateTool(10) = 11")
	// Without EstimateTool: runes from text (2) + Name (0) + Function.Name (3) + Arguments (2) = 7 runes -> 2 tokens.
	cNoEst := &CharFallbackCounter{CharsPerToken: 4}
	gotNoEst, err := cNoEst.Count(ctx, msgs)
	require.NoError(t, err)
	assert.Equal(t, 2, gotNoEst, "rune-based fallback for tool call")
}

func FuzzCharFallbackCounter(f *testing.F) {
	f.Add("hello world", 4)
	f.Add("", 1)
	f.Add("привет", 4)
	f.Fuzz(func(t *testing.T, text string, charsPerToken int) {
		if charsPerToken <= 0 {
			t.Skip()
		}
		c := &CharFallbackCounter{CharsPerToken: charsPerToken}
		msgs := []Message{TextMessage("user", text)}
		n, err := c.Count(context.Background(), msgs)
		if err != nil {
			t.Fatal(err)
		}
		if n < 0 {
			t.Fatalf("negative token count: %d", n)
		}
	})
}

func BenchmarkCharFallbackCounter(b *testing.B) {
	c := &CharFallbackCounter{CharsPerToken: 4}
	msgs := []Message{TextMessage("user", "The quick brown fox jumps over the lazy dog. ")}
	for i := 0; i < b.N; i++ {
		_, _ = c.Count(context.Background(), msgs)
	}
}
