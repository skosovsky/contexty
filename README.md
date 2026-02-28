# contexty

[![Go Reference](https://pkg.go.dev/badge/github.com/skosovsky/contexty.svg)](https://pkg.go.dev/github.com/skosovsky/contexty)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**contexty** is a token budget allocator for LLM context windows. It helps fit system prompts, pinned facts, RAG results, chat history, and tool outputs into a fixed token limit by treating memory as tiers: higher-priority blocks are allocated first, and configurable eviction strategies apply when a block does not fit.

## Installation

```bash
go get github.com/skosovsky/contexty
```

Requires Go 1.23+.

## Quick Start

```go
package main

import (
    "context"
    "github.com/skosovsky/contexty"
)

func main() {
    ctx := context.Background()
    counter := &contexty.CharFallbackCounter{CharsPerToken: 4}
    builder := contexty.NewBuilder(contexty.AllocatorConfig{
        MaxTokens:    4000,
        TokenCounter: counter,
    })
    builder.AddBlock(contexty.MemoryBlock{
        ID:       "persona",
        Tier:     contexty.TierSystem,
        Strategy: contexty.NewStrictStrategy(),
        Messages: []contexty.Message{contexty.TextMessage("system", "You are a helpful assistant.")},
    })
    msgs, report, err := builder.Compile(ctx)
    if err != nil {
        panic(err)
    }
    // report.TotalTokensUsed, report.Evictions, report.BlocksDropped describe what happened.
    _ = msgs
}
```

## Features

- **Tiered memory**: Blocks have a priority tier (System, Core, RAG, History, Scratchpad). Lower tier number = higher priority. Blocks are processed in tier order; within the same tier, insertion order is preserved.
- **Eviction strategies**: Each block has a strategy for when it does not fit:
  - **Strict** — error if block does not fit (for critical system prompts).
  - **Drop** — remove the block entirely (e.g. RAG).
  - **Truncate** — remove oldest messages until it fits (e.g. chat history).
  - **Summarize** — call your `Summarizer` to compress the block.
- **Token counting**: The library does not tokenize; you inject a `TokenCounter` whose `Count(msgs []Message) (int, error)` counts tokens for a slice of messages (e.g. [CharFallbackCounter](https://pkg.go.dev/github.com/skosovsky/contexty#CharFallbackCounter) for tests, or a tiktoken-based counter for production). Message content is typed as `[]ContentPart` (text, image_url, etc.); use [TextMessage](https://pkg.go.dev/github.com/skosovsky/contexty#TextMessage) or [MultipartMessage](https://pkg.go.dev/github.com/skosovsky/contexty#MultipartMessage) for ergonomics.
- **CompileReport**: After `Compile`, you get `TotalTokensUsed`, `TokensPerBlock`, `Evictions`, and `BlocksDropped` for observability.
- **InjectIntoSystem**: Utility to merge auxiliary blocks into a single system message with XML-style tags. Only text parts (`ContentPart.Type == "text"`) are included; non-text parts are ignored. Content is XML-escaped.

## Strategies at a glance

| Strategy              | When to use                          | If block doesn't fit        |
|-----------------------|--------------------------------------|-----------------------------|
| `NewStrictStrategy()` | System persona, rules (must fit)     | Returns error               |
| `NewDropStrategy()`   | RAG, optional facts                 | Block removed               |
| `NewTruncateOldestStrategy()` | Chat history                  | Oldest messages removed     |
| `NewSummarizeStrategy(summarizer)` | Long blocks to compress   | Summarizer called; else dropped |

Custom strategies implement `Apply(ctx, msgs, originalTokens, limit, counter)` and must return messages whose total token count does not exceed the given limit; `Compile` validates this and returns `ErrStrategyExceededBudget` if the contract is violated. The library performs minimal validation (no provider-specific role/URL/JSON checks); the only strict guarantee is `TotalTokensUsed <= MaxTokens`.

## Example output (CompileReport)

After `Compile(ctx)`:

- `TotalTokensUsed` — tokens in the final `[]Message`.
- `OriginalTokens` — total tokens before eviction.
- `TokensPerBlock` — map of block ID → tokens used in output.
- `Evictions` — map of block ID → eviction label (`"rejected"`, `"dropped"`, `"truncated"`, or `"summarized"`). Only blocks for which an eviction strategy was actually applied (block did not fit and strategy ran) appear here; blocks that fit without changes are not listed.
- `BlocksDropped` — slice of block IDs that were fully removed.

## Full example

See [examples/full_assembly](examples/full_assembly) for a multi-tier setup (system, core, RAG, history) and how to run it.

## Documentation

Full API: [pkg.go.dev/github.com/skosovsky/contexty](https://pkg.go.dev/github.com/skosovsky/contexty).

## License

MIT. See [LICENSE](LICENSE).
