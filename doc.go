// Package contexty implements a token budget builder for LLM message windows.
//
// Callers register named blocks in priority order, choose an eviction strategy
// per block, and optionally install a formatter for the final message assembly.
// Build enforces the hard token budget while leaving token counting and any
// summarization logic to caller-provided interfaces. The builder snapshots
// blocks on registration and hands formatters deep-copied post-eviction
// snapshots so repeated builds remain deterministic.
//
// Example:
//
//	counter := &contexty.CharFallbackCounter{CharsPerToken: 4}
//	builder := contexty.NewBuilder(4000, counter)
//	builder.AddBlock("instructions", contexty.MemoryBlock{
//	    Strategy: contexty.NewStrictStrategy(),
//	    Messages: []contexty.Message{
//	        contexty.TextMessage("system", "You are a helpful assistant."),
//	    },
//	})
//	msgs, err := builder.Build(ctx)
//	_, _ = msgs, err
package contexty
