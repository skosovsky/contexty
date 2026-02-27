package contexty

import "unicode/utf8"

// CharFallbackCounter approximates token count by dividing character count
// by a configurable ratio. It does not use a real tokenizer (BPE/tiktoken).
// Suitable for prototyping and environments where exact counting is not critical.
// For production, inject a model-specific TokenCounter (e.g. tiktoken).
type CharFallbackCounter struct {
	// CharsPerToken is the character-to-token ratio (e.g. 4 for English).
	// Must be positive.
	CharsPerToken int
}

// Count returns the estimated token count: ceil(rune_count / CharsPerToken).
// Returns ErrInvalidCharsPerToken if CharsPerToken <= 0.
func (c *CharFallbackCounter) Count(text string) (int, error) {
	if c.CharsPerToken <= 0 {
		return 0, ErrInvalidCharsPerToken
	}
	runeCount := utf8.RuneCountInString(text)
	if runeCount == 0 {
		return 0, nil
	}
	return (runeCount + c.CharsPerToken - 1) / c.CharsPerToken, nil
}

// Compile-time interface check.
var _ TokenCounter = (*CharFallbackCounter)(nil)
