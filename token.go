package contexty

import (
	"context"
	"unicode/utf8"
)

// DefaultTokensPerNonTextPart is the fallback token count for content parts
// that are not Type "text" (e.g. image_url). No validation or network checks.
const DefaultTokensPerNonTextPart = 85

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
// ToolCalls: if EstimateTool is set, its result is summed; otherwise runes of Arguments+Name are used.
// Returns ErrInvalidCharsPerToken if CharsPerToken <= 0.
func (c *CharFallbackCounter) Count(ctx context.Context, msgs []Message) (int, error) {
	if c.CharsPerToken <= 0 {
		return 0, ErrInvalidCharsPerToken
	}
	_ = ctx // reserved for cancellation/timeouts in network-backed implementations
	nonTextWeight := c.TokensPerNonTextPart
	if nonTextWeight <= 0 {
		nonTextWeight = DefaultTokensPerNonTextPart
	}
	var totalRunes int
	var toolTokens int
	for _, m := range msgs {
		totalRunes += utf8.RuneCountInString(m.Name)
		for _, p := range m.Content {
			if p.Type == "text" {
				totalRunes += utf8.RuneCountInString(p.Text)
			} else {
				totalRunes += nonTextWeight * c.CharsPerToken
			}
		}
		if len(m.ToolCalls) > 0 && c.EstimateTool != nil {
			for _, tc := range m.ToolCalls {
				toolTokens += c.EstimateTool(tc)
			}
		} else {
			for _, tc := range m.ToolCalls {
				totalRunes += utf8.RuneCountInString(tc.Function.Arguments)
				totalRunes += utf8.RuneCountInString(tc.Function.Name)
			}
		}
	}
	tokensFromRunes := 0
	if totalRunes > 0 {
		tokensFromRunes = (totalRunes + c.CharsPerToken - 1) / c.CharsPerToken
	}
	return tokensFromRunes + toolTokens, nil
}

// Compile-time interface check.
var _ TokenCounter = (*CharFallbackCounter)(nil)
