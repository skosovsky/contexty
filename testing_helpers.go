package contexty

// FixedCounter always returns a fixed token count per call, ignoring the input.
// Intended for testing only; allows deterministic budget behavior in tests.
type FixedCounter struct {
	// TokensPerMessage is the constant value returned by Count.
	TokensPerMessage int
}

// Count returns FixedCounter.TokensPerMessage regardless of text.
func (c *FixedCounter) Count(_ string) (int, error) {
	return c.TokensPerMessage, nil
}

// Compile-time interface check for testing helper.
var _ TokenCounter = (*FixedCounter)(nil)
