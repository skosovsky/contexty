package contexty

import "unicode/utf8"

// DefaultTokensPerNonTextPart is the fallback token count for content parts
// that are not Type "text" (e.g. image_url). No validation or network checks.
const DefaultTokensPerNonTextPart = 85

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
}

// Count returns the estimated token count for all messages.
// Text from ContentPart (Type "text") and ToolCalls[].Function.Arguments is measured in runes;
// non-text parts use a constant weight (TokensPerNonTextPart or DefaultTokensPerNonTextPart).
// Returns ErrInvalidCharsPerToken if CharsPerToken <= 0.
func (c *CharFallbackCounter) Count(msgs []Message) (int, error) {
	if c.CharsPerToken <= 0 {
		return 0, ErrInvalidCharsPerToken
	}
	nonTextWeight := c.TokensPerNonTextPart
	if nonTextWeight <= 0 {
		nonTextWeight = DefaultTokensPerNonTextPart
	}
	var totalRunes int
	for _, m := range msgs {
		totalRunes += utf8.RuneCountInString(m.Name)
		for _, p := range m.Content {
			if p.Type == "text" {
				totalRunes += utf8.RuneCountInString(p.Text)
			} else {
				// Safe fallback: constant weight for non-text (image_url, etc.)
				totalRunes += nonTextWeight * c.CharsPerToken
			}
		}
		for _, tc := range m.ToolCalls {
			totalRunes += utf8.RuneCountInString(tc.Function.Arguments)
			totalRunes += utf8.RuneCountInString(tc.Function.Name)
		}
	}
	if totalRunes == 0 {
		return 0, nil
	}
	return (totalRunes + c.CharsPerToken - 1) / c.CharsPerToken, nil
}

// Compile-time interface check.
var _ TokenCounter = (*CharFallbackCounter)(nil)
