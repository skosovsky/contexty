// Package contexty implements token-budgeted message assembly and persistence
// contracts for LLM message windows.
//
// Callers register named blocks in priority order, choose an eviction strategy
// per block, and optionally install a formatter for the final message assembly.
// Build enforces the hard token budget while leaving token counting,
// summarization, and history storage to caller-provided interfaces.
//
// Builder is not safe for concurrent mutation; use Clone per goroutine or request when sharing a template.
// The builder snapshots blocks on registration and hands formatters deep-copied
// post-eviction snapshots so repeated builds remain deterministic. BuildDetailed
// returns raw post-eviction blocks for orchestration and persistence.
//
// HistoryStore and Thread coordinate persisted conversation history around a
// dedicated builder block. Storage adapters live in nested modules under
// contexty/adapters/store/* and are responsible only for CRUD over serialized
// Message values.
//
// Example:
//
//	counter := &contexty.CharFallbackCounter{CharsPerToken: 4}
//	builder := contexty.NewBuilder(4000, counter)
//	builder.AddBlock("instructions", contexty.MemoryBlock{
//	    Strategy: contexty.NewStrictStrategy(),
//	    Messages: []contexty.Message{
//	        contexty.TextMessage(contexty.RoleSystem, "You are a helpful assistant."),
//	    },
//	})
//	msgs, err := builder.Build(ctx)
//	_, _ = msgs, err
package contexty
