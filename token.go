package contexty

import (
	"context"
	"unicode/utf8"
)

// DefaultTokensPerNonTextPart is the fallback token count for content parts
// that are not Type "text" (e.g. image_url). No validation or network checks.
const DefaultTokensPerNonTextPart = 85

// ToolCallOverhead is the pessimistic token overhead added per tool call (structure, IDs, etc.).
const ToolCallOverhead = 20

// ToolCallEstimator returns the token weight of a single tool call.
// When non-nil in CharFallbackCounter, it is used for ToolCalls instead of rune-based fallback.
type ToolCallEstimator func(call ToolCall) int

// CharFallbackCounter approximates token count by dividing character count
// by a configurable ratio. It does not use a real tokenizer (BPE/tiktoken).
// Suitable for prototyping and environments where exact counting is not critical.
// For production, inject a model-specific TokenCounter (e.g. tiktoken).
type CharFallbackCounter struct {
	// CharsPerToken is the character-to-token ratio (e.g. 4 for English).
	// Must be positive.
	CharsPerToken int
	// TokensPerNonTextPart is the weight for content parts with Type != "text"
	// (e.g. image_url). Zero means use DefaultTokensPerNonTextPart.
	TokensPerNonTextPart int
	// EstimateTool is optional; when set, used for each ToolCall instead of rune-based fallback.
	EstimateTool ToolCallEstimator
}

// Count returns the estimated token count for all messages.
// Text from ContentPart (Type "text") is measured in runes; non-text parts use a constant weight.
// ToolCalls: if EstimateTool is set, its result is summed; otherwise runes of Arguments+Name are used, plus ToolCallOverhead per call.
// Returns ErrInvalidCharsPerToken if CharsPerToken <= 0.
func (c *CharFallbackCounter) Count(ctx context.Context, msgs []Message) (int, error) {
	weights, err := c.CountPerMessage(ctx, msgs)
	if err != nil {
		return 0, err
	}
	var sum int
	for _, w := range weights {
		sum += w
	}
	return sum, nil
}

// CountPerMessage returns one token weight per message in the same order as msgs.
// Used by eviction strategies for O(1) truncation without re-counting in a loop.
func (c *CharFallbackCounter) CountPerMessage(ctx context.Context, msgs []Message) ([]int, error) {
	if c.CharsPerToken <= 0 {
		return nil, ErrInvalidCharsPerToken
	}
	_ = ctx
	nonTextWeight := c.TokensPerNonTextPart
	if nonTextWeight <= 0 {
		nonTextWeight = DefaultTokensPerNonTextPart
	}
	out := make([]int, len(msgs))
	for i, m := range msgs {
		var runes int
		runes += utf8.RuneCountInString(m.Name)
		for _, p := range m.Content {
			if p.Type == "text" {
				runes += utf8.RuneCountInString(p.Text)
			} else {
				runes += nonTextWeight * c.CharsPerToken
			}
		}
		var toolTokens int
		if len(m.ToolCalls) > 0 && c.EstimateTool != nil {
			for _, tc := range m.ToolCalls {
				toolTokens += c.EstimateTool(tc) + ToolCallOverhead
			}
		} else {
			for _, tc := range m.ToolCalls {
				runes += utf8.RuneCountInString(tc.Function.Arguments)
				runes += utf8.RuneCountInString(tc.Function.Name)
				toolTokens += ToolCallOverhead
			}
		}
		tokensFromRunes := 0
		if runes > 0 {
			tokensFromRunes = (runes + c.CharsPerToken - 1) / c.CharsPerToken
		}
		out[i] = tokensFromRunes + toolTokens
	}
	return out, nil
}

// Compile-time interface check.
var _ TokenCounter = (*CharFallbackCounter)(nil)
