// Package contexty implements a token budget allocator for LLM context windows.
//
// LLMs have a fixed token limit (e.g. 8192). Contexty helps fit system prompts,
// pinned facts, RAG results, chat history, and tool outputs into that budget
// by treating memory as tiers: higher-priority blocks are allocated first,
// and configurable eviction strategies (strict, drop, truncate, summarize)
// apply when a block does not fit.
//
// The library does not tokenize text itself. Callers inject a TokenCounter
// (e.g. tiktoken for a specific model, or CharFallbackCounter for tests).
// See [AllocatorConfig], [Builder], and [Builder.Compile] for the main API.
//
// Example:
//
//	counter := &contexty.CharFallbackCounter{CharsPerToken: 4}
//	builder := contexty.NewBuilder(contexty.AllocatorConfig{
//	    MaxTokens:    4000,
//	    TokenCounter: counter,
//	})
//	builder.AddBlock(contexty.MemoryBlock{
//	    ID: "persona", Tier: contexty.TierSystem,
//	    Strategy: contexty.NewStrictStrategy(),
//	    Messages: []contexty.Message{contexty.TextMessage("system", "You are a helpful assistant.")},
//	})
//	msgs, report, err := builder.Compile(ctx)
//	// report.TotalTokensUsed, report.Evictions, report.BlocksDropped describe what happened.
//	// See examples/full_assembly for a full multi-tier setup.
package contexty
