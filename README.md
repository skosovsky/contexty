# contexty

[![Go Reference](https://pkg.go.dev/badge/github.com/skosovsky/contexty.svg)](https://pkg.go.dev/github.com/skosovsky/contexty)
[![Go Report Card](https://goreportcard.com/badge/github.com/skosovsky/contexty)](https://goreportcard.com/report/github.com/skosovsky/contexty)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**TL;DR** — contexty is a Go library for dynamic LLM context window management. It lets you assemble, format, and intelligently truncate or drop chat history and RAG documents against strict token limits using configurable eviction strategies.

## Installation

```bash
go get github.com/skosovsky/contexty
```

Requires Go 1.23+.

## Quick Start (AI-friendly)

Full pipeline: init counter and builder, add system + RAG blocks, compile, handle errors.

```go
package main

import (
	"context"
	"errors"
	"log"

	"github.com/skosovsky/contexty"
)

func main() {
	ctx := context.Background()
	counter := &contexty.CharFallbackCounter{CharsPerToken: 4}
	builder := contexty.NewBuilder(contexty.AllocatorConfig{
		MaxTokens:    2000,
		TokenCounter: counter,
	})

	// Required system prompt (must fit; otherwise Compile returns error)
	builder.AddBlock(contexty.MemoryBlock{
		ID:       "system",
		Tier:     contexty.TierSystem,
		Strategy: contexty.NewStrictStrategy(),
		Messages: []contexty.Message{contexty.TextMessage("system", "You are a helpful assistant.")},
	})

	// RAG block: drop entirely if it does not fit
	builder.AddBlock(contexty.MemoryBlock{
		ID:       "rag",
		Tier:     contexty.TierRAG,
		Strategy: contexty.NewDropStrategy(),
		Messages: []contexty.Message{
			contexty.TextMessage("system", "Retrieved doc 1..."),
			contexty.TextMessage("system", "Retrieved doc 2..."),
		},
	})

	msgs, report, err := builder.Compile(ctx)
	if err != nil {
		if errors.Is(err, contexty.ErrBudgetExceeded) {
			log.Fatal("system block or strategy contract: block exceeds budget")
		}
		if errors.Is(err, contexty.ErrInvalidConfig) {
			log.Fatal("invalid config: MaxTokens or TokenCounter")
		}
		log.Fatal(err)
	}
	log.Printf("compiled %d messages, tokens used: %d", len(msgs), report.TotalTokensUsed)
}
```

## Key abstractions and contracts

- **Token Counter** ([token.go](token.go)): Implement `TokenCounter` with `Count(ctx context.Context, msgs []Message) (int, error)`. The context is passed from `Compile` for cancellation and timeouts (e.g. when calling a remote tokenization service). Use [CharFallbackCounter](https://pkg.go.dev/github.com/skosovsky/contexty#CharFallbackCounter) for prototyping; optional [EstimateTool](https://pkg.go.dev/github.com/skosovsky/contexty#CharFallbackCounter.EstimateTool) for custom tool-call token weights. To plug in your own tokenizer (e.g. tiktoken), implement the interface and pass it in `AllocatorConfig.TokenCounter`.
- **Strategies** ([strategies.go](strategies.go)): Built-in strategies — **Strict** (error if block does not fit), **Drop** (remove block), **Truncate** (remove oldest messages; options: KeepUserAssistantPairs, MinMessages, ProtectRole), **Summarize** (call your `Summarizer`). Custom strategies implement `Apply(ctx, msgs, originalTokens, limit, counter)` and must return messages whose total token count ≤ limit; `Compile` enforces this and returns `ErrStrategyExceededBudget` on violation.
- **Formatter** ([formatter.go](formatter.go)): [InjectIntoSystem](https://pkg.go.dev/github.com/skosovsky/contexty#InjectIntoSystem) merges auxiliary blocks into a single system message with XML-style tags (`<context>`, `<fact>`); only text parts are included; content is XML-escaped.
- **Fact Extractor** ([factextractor.go](factextractor.go)): Interface for extracting facts from conversation history; reserved for v2 — the allocator does not use it yet.

## How limits are resolved

Blocks are processed in **tier order**: System (0), Core (1), RAG (2), History (3), Scratchpad (4). Within the same tier, insertion order (AddBlock) is preserved. For each block:

1. Token count is computed; if the block has optional `MaxTokens` and it is less than the remaining budget, the strategy receives that as the limit (per-block cap).
2. If the block fits within the remaining budget, it is appended as-is.
3. If not, the block’s `EvictionStrategy.Apply` is called; the result is re-counted and must satisfy `used ≤ remaining`; otherwise `Compile` returns `ErrStrategyExceededBudget`.
4. Remaining budget is decreased by the tokens used.

What gets dropped or truncated is thus determined by block order (tiers + AddBlock) and each block’s strategy, not by a single global “priority” field.

## Features

- **Message model**: [Message](https://pkg.go.dev/github.com/skosovsky/contexty#Message) has Role, Content (`[]ContentPart`), Name, ToolCalls, ToolCallID, Metadata. [ContentPart](https://pkg.go.dev/github.com/skosovsky/contexty#ContentPart): Type (e.g. `"text"`, `"image_url"`), Text, ImageURL. Helpers: [TextMessage](https://pkg.go.dev/github.com/skosovsky/contexty#TextMessage), [MultipartMessage](https://pkg.go.dev/github.com/skosovsky/contexty#MultipartMessage). Multimodal content is supported without provider-specific validation in core.
- **Tiers**: System, Core, RAG, History, Scratchpad; lower number = higher priority. Custom tiers via `Tier(N)`.
- **MemoryBlock**: ID, Messages, Tier, Strategy; optional **MaxTokens** (per-block token cap), **CacheControl** (for provider prompt caching; not interpreted in core).
- **Builder**: [NewBuilder](https://pkg.go.dev/github.com/skosovsky/contexty#NewBuilder)(config), [AddBlock](https://pkg.go.dev/github.com/skosovsky/contexty#Builder.AddBlock), [Compile](https://pkg.go.dev/github.com/skosovsky/contexty#Builder.Compile)(ctx). A builder can be reused: call AddBlock and Compile multiple times; each Compile uses the current list of blocks (blocks are not cleared).
- **Token counting**: You inject a `TokenCounter`; context is passed from Compile for cancellation/timeouts. CharFallbackCounter and optional EstimateTool; or your own implementation (e.g. tiktoken).
- **Eviction strategies**: Strict, Drop, TruncateOldest (KeepUserAssistantPairs, MinMessages, ProtectRole), Summarize(Summarizer). Custom strategy: implement Apply; contract enforced by Compile. Eviction labels in report: `"rejected"`, `"dropped"`, `"truncated"`, `"summarized"`, or `"evicted"` for custom.
- **Summarizer**: Interface used by SummarizeStrategy: `Summarize(ctx, msgs) (Message, error)`.
- **CompileReport**: TotalTokensUsed, RemainingTokens, OriginalTokens, OriginalTokensPerBlock, TokensPerBlock, Evictions, BlocksDropped (see below).
- **Validation policy**: Minimal validation in core (no provider-specific role/URL/JSON checks). The only hard guarantee is `TotalTokensUsed ≤ MaxTokens`; strategy output is checked and `ErrStrategyExceededBudget` is returned on violation.

## Strategies at a glance

| Strategy              | When to use                          | If block doesn't fit        |
|-----------------------|--------------------------------------|-----------------------------|
| `NewStrictStrategy()` | System persona, rules (must fit)     | Returns error               |
| `NewDropStrategy()`   | RAG, optional facts                 | Block removed               |
| `NewTruncateOldestStrategy(opts...)` | Chat history                  | Oldest messages removed; opts: KeepUserAssistantPairs, MinMessages, ProtectRole |
| `NewSummarizeStrategy(summarizer)` | Long blocks to compress   | Summarizer called; else dropped |

Truncate options: `KeepUserAssistantPairs(true)` keeps user/assistant pairs; `MinMessages(n)` drops the block if fewer than n messages would remain; `ProtectRole("developer")` never removes messages with that role—the first removable message (or pair) is removed instead.

## CompileReport

After `Compile(ctx)`:

- **TotalTokensUsed** — tokens in the final `[]Message`.
- **RemainingTokens** — `MaxTokens - TotalTokensUsed` after compile.
- **OriginalTokens** — total tokens before eviction (all blocks).
- **OriginalTokensPerBlock** — map block ID → tokens before eviction (before strategy was applied).
- **TokensPerBlock** — map block ID → tokens used in output.
- **Evictions** — map block ID → eviction label (`"rejected"`, `"dropped"`, `"truncated"`, `"summarized"`). Only blocks for which an eviction strategy was actually applied appear here.
- **BlocksDropped** — slice of block IDs that were fully removed.

## Error handling

Use `errors.Is(err, contexty.Err...)` to handle specific failures:

| Error | When |
|-------|------|
| `ErrInvalidConfig` | MaxTokens ≤ 0 or TokenCounter == nil |
| `ErrNilStrategy` | A MemoryBlock has nil Strategy |
| `ErrTokenCountFailed` | TokenCounter.Count returned an error |
| `ErrBudgetExceeded` | StrictStrategy: block does not fit in remaining budget |
| `ErrStrategyExceededBudget` | Strategy returned messages exceeding remaining budget (contract violation) |
| `ErrInvalidCharsPerToken` | CharFallbackCounter with CharsPerToken ≤ 0 |

Example: if the system block with Strict strategy does not fit, `Compile` returns an error that wraps or equals `ErrBudgetExceeded`.

## Testing

Use [testing_helpers.go](testing_helpers.go) for unit tests without heavy CGO tokenizers. [FixedCounter](https://pkg.go.dev/github.com/skosovsky/contexty#FixedCounter) returns a count based on message structure: set `TokensPerMessage`, and optionally `TokensPerContentPart` and `TokensPerToolCall`, to simulate realistic eviction (e.g. removing one “heavy” message frees many tokens). Example:

```go
counter := &contexty.FixedCounter{
	TokensPerMessage:    10,
	TokensPerContentPart: 5,
	TokensPerToolCall:   20,
}
// Use counter in AllocatorConfig and assert report.TotalTokensUsed, evictions, etc.
```

## Full example

See [examples/full_assembly](examples/full_assembly) for a multi-tier setup (system, core, RAG, history) that demonstrates compilation when total content exceeds the token limit, with evictions and dropped blocks.

## Documentation

Full API: [pkg.go.dev/github.com/skosovsky/contexty](https://pkg.go.dev/github.com/skosovsky/contexty).

## License

MIT. See [LICENSE](LICENSE).
