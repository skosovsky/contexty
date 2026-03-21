package contexty

import (
	"context"
	"fmt"
)

// DefaultFormatter concatenates messages from each block in registration order.
type DefaultFormatter struct{}

// Format concatenates block messages in registration order.
func (DefaultFormatter) Format(ctx context.Context, blocks []NamedBlock) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("contexty: default formatter: %w", err)
	}
	var out []Message
	for _, block := range blocks {
		out = append(out, cloneMessages(block.Block.Messages)...)
	}
	return out, nil
}
