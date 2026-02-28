package contexty

import "context"

// FixedCounter returns a token count derived from message structure for testing.
// Enables realistic eviction tests: removing one "heavy" message frees many tokens.
type FixedCounter struct {
	// TokensPerMessage is the base weight per message (always applied).
	TokensPerMessage int
	// TokensPerContentPart is added for each ContentPart in a message (0 = not used).
	TokensPerContentPart int
	// TokensPerToolCall is added for each ToolCall in a message (0 = not used).
	TokensPerToolCall int
}

// Count returns the sum over msgs of (base + len(Content)*TokensPerContentPart + len(ToolCalls)*TokensPerToolCall).
func (c *FixedCounter) Count(ctx context.Context, msgs []Message) (int, error) {
	_ = ctx
	var total int
	for _, m := range msgs {
		total += c.TokensPerMessage
		if c.TokensPerContentPart != 0 {
			total += len(m.Content) * c.TokensPerContentPart
		}
		if c.TokensPerToolCall != 0 {
			total += len(m.ToolCalls) * c.TokensPerToolCall
		}
	}
	return total, nil
}

// Compile-time interface check for testing helper.
var _ TokenCounter = (*FixedCounter)(nil)
