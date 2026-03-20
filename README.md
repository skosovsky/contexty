# contexty

[![Go Reference](https://pkg.go.dev/badge/github.com/skosovsky/contexty.svg)](https://pkg.go.dev/github.com/skosovsky/contexty)
[![Go Report Card](https://goreportcard.com/badge/github.com/skosovsky/contexty)](https://goreportcard.com/report/github.com/skosovsky/contexty)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

`contexty` is a Go library for budgeted LLM message assembly. You register named blocks in priority order, assign an eviction strategy to each block, optionally plug in a formatter, and call `Build(ctx)` to get a final `[]Message` that fits the configured token budget.

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
	"errors"
	"log"

	"github.com/skosovsky/contexty"
)

func main() {
	ctx := context.Background()
	counter := &contexty.CharFallbackCounter{CharsPerToken: 4}
	builder := contexty.NewBuilder(2000, counter)

	builder.AddBlock("instructions", contexty.MemoryBlock{
		Strategy: contexty.NewStrictStrategy(),
		Messages: []contexty.Message{
			contexty.TextMessage("system", "You are a helpful assistant."),
		},
	})

	builder.AddBlock("reference", contexty.MemoryBlock{
		Strategy:  contexty.NewDropTailStrategy(),
		MaxTokens: 400,
		Messages: []contexty.Message{
			contexty.TextMessage("system", "Document chunk 1"),
			contexty.TextMessage("system", "Document chunk 2"),
			contexty.TextMessage("system", "Document chunk 3"),
		},
	})

	msgs, err := builder.Build(ctx)
	if err != nil {
		if errors.Is(err, contexty.ErrBudgetExceeded) {
			log.Fatal("required block does not fit budget")
		}
		if errors.Is(err, contexty.ErrFormatterExceededBudget) {
			log.Fatal("formatter returned too many messages")
		}
		log.Fatal(err)
	}

	log.Printf("built %d messages", len(msgs))
}
```

## API

- `NewBuilder(maxTokens, counter)` creates a reusable builder. Validation happens in `Build(ctx)`.
- `AddBlock(name, block)` registers a block in priority order. Earlier calls always consume budget before later ones, and the builder snapshots the block on registration.
- `WithFormatter(formatter)` installs a final formatting step over post-eviction named block snapshots.
- `Build(ctx)` applies strategies in registration order, runs the formatter with the caller context, and verifies that the final output still fits the budget.

```go
type MemoryBlock struct {
	Strategy     EvictionStrategy
	Messages     []Message
	MaxTokens    int
	CacheControl map[string]any
}

type NamedBlock struct {
	Name  string
	Block MemoryBlock
}

type Formatter interface {
	Format(ctx context.Context, blocks []NamedBlock) ([]Message, error)
}

type EvictionMiddleware func(EvictionStrategy) EvictionStrategy
type FormatterMiddleware func(Formatter) Formatter
```

## Strategies

| Strategy | Behavior |
| --- | --- |
| `NewStrictStrategy()` | Returns `ErrBudgetExceeded` when the block does not fit. |
| `NewDropStrategy()` | Drops the whole block when it does not fit. |
| `NewDropTailStrategy()` | Removes trailing messages until the block fits; returns `ErrBlockTooLarge` if one remaining message is still too large. |
| `NewTruncateOldestStrategy(opts...)` | Removes older messages from the front, with optional turn atomicity, protected roles, and minimum message count. |
| `NewSummarizeStrategy(summarizer)` | Replaces an oversized block with a summary message. |

`Build(ctx)` re-counts strategy output and returns `ErrStrategyExceededBudget` if a strategy violates the contract.

## Formatter Behavior

- If no formatter is configured, `contexty` uses `DefaultFormatter`, which concatenates block messages in registration order.
- Formatters run after budgeting is finished.
- `Build(ctx)` passes formatter implementations their own deep-copied block snapshots so formatter-side mutation cannot affect later builds.
- `Build(ctx)` re-counts formatter output and returns `ErrFormatterExceededBudget` if the formatter expands beyond `maxTokens`.

## Error Handling

Use `errors.Is(err, contexty.Err...)`:

- `ErrInvalidConfig`: `maxTokens <= 0` or `TokenCounter` is nil.
- `ErrNilStrategy`: a block is missing its strategy.
- `ErrTokenCountFailed`: token counting failed.
- `ErrBudgetExceeded`: a strict block does not fit.
- `ErrBlockTooLarge`: `DropTailStrategy` could not shrink a single remaining message.
- `ErrStrategyExceededBudget`: a strategy returned output above the allowed limit.
- `ErrFormatterExceededBudget`: the formatter returned output above the builder budget.
- `ErrInvalidCharsPerToken`: `CharFallbackCounter` received an invalid ratio.

## Testing Helpers

[`FixedCounter`](testing_helpers.go) is intended for tests and examples where you want deterministic token counts without a tokenizer implementation.

```go
counter := &contexty.FixedCounter{
	TokensPerMessage:     10,
	TokensPerContentPart: 5,
	TokensPerToolCall:    20,
}
```

## Example Program

See [`examples/full_assembly`](examples/full_assembly) for a complete runnable example that shows strict, truncate, and drop-tail behavior under one shared token budget.

## License

MIT. See [LICENSE](LICENSE).
