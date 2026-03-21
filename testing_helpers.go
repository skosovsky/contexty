package contexty

import (
	"context"
	"fmt"
)

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
	weights, err := c.CountPerMessage(ctx, msgs)
	if err != nil {
		return 0, err
	}
	var total int
	for _, w := range weights {
		total += w
	}
	return total, nil
}

// CountPerMessage returns one weight per message: TokensPerMessage + optional ContentPart/ToolCall extras.
func (c *FixedCounter) CountPerMessage(ctx context.Context, msgs []Message) ([]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("contexty: fixed counter: %w", err)
	}
	out := make([]int, len(msgs))
	for i, m := range msgs {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("contexty: fixed counter: %w", err)
		}
		w := c.TokensPerMessage
		if c.TokensPerContentPart != 0 {
			w += len(m.Content) * c.TokensPerContentPart
		}
		if c.TokensPerToolCall != 0 {
			w += len(m.ToolCalls) * c.TokensPerToolCall
		}
		out[i] = w
	}
	return out, nil
}

// Compile-time interface check for testing helper.
var _ TokenCounter = (*FixedCounter)(nil)
