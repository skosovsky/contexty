package contexty

import "context"

// DefaultFormatter concatenates messages from each block in registration order.
// It ignores ctx because formatting is purely in-memory.
type DefaultFormatter struct{}

// Format concatenates block messages in registration order.
func (DefaultFormatter) Format(_ context.Context, blocks []NamedBlock) ([]Message, error) {
	var out []Message
	for _, block := range blocks {
		out = append(out, cloneMessages(block.Block.Messages)...)
	}
	return out, nil
}
