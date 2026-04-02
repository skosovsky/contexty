# contexty

[![Go Reference](https://pkg.go.dev/badge/github.com/skosovsky/contexty.svg)](https://pkg.go.dev/github.com/skosovsky/contexty)
[![Go Report Card](https://goreportcard.com/badge/github.com/skosovsky/contexty)](https://goreportcard.com/report/github.com/skosovsky/contexty)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

`contexty` is a Go library for budgeted LLM message assembly and persisted thread history. You register named blocks in priority order, assign an eviction strategy to each block, optionally plug in a formatter, and call `Build(ctx)` to get a final `[]Message` that fits the configured token budget. When you need persisted chat history, `HistoryStore` and `Thread` let you load, compact, and save a dedicated history block around the same builder.

## Installation

```bash
go get github.com/skosovsky/contexty
```

Requires Go 1.26+.

## Development

The repo root [`go.work`](go.work) lists the main module and both adapter modules. `make test`, `make bench`, and `make cover` pass **all** workspace module import paths (`go list -m -f '{{.Path}}/...'`), because a bare `./...` from the repo root only matches the main module. `make lint` and `make fix` run `golangci-lint` (and `go fix` for `fix`) **per module** using directories from `go list -m`.

```bash
make test
make lint
make fix
```

`make test-all`, `make lint-all`, and `make fix-all` are aliases for `make test`, `make lint`, and `make fix`. Use a local clone with `go.work` so adapters resolve the **local** `contexty` tree (no unpublished version tags required for day-to-day work).

`make bench` runs benchmarks across the workspace. `make cover` writes a single `coverage.out` at the repo root and prints `go tool cover -func`. Fuzz targets live in `*_test.go` files with `//go:build fuzz` so they are not compiled by plain `go test ./...`. `make fuzz` runs `go test -tags=fuzz -fuzz` **per package** in each module that has fuzz tests (Go does not allow `-fuzz` across multiple packages in one invocation).

## Fail-fast API (panics)

`Builder.AddBlock`, `WithFormatter`, and store options such as `postgres.WithSerializer` / `redis.WithSerializer` panic on invalid arguments (empty name, nil serializer, invalid table name, etc.). Invalid configuration is treated as a **programming error**. Validate inputs before constructing builders and options.

## Builder concurrency

`Builder` is **not** safe for concurrent mutation: do not call `AddBlock`, `WithFormatter`, or `SetBlockMessages` on the same instance from multiple goroutines without external synchronization. For concurrent requests, register a template with `NewBuilder` and `AddBlock`, then call `Clone()` per request or goroutine and run `Build` / `BuildDetailed` on the copy.

## Token counting

`CharFallbackCounter` divides character count by a ratio; it is **not** a real tokenizer (BPE/tiktoken). Do not rely on it for production cost limits or strict context windows—inject a model-specific [`TokenCounter`](https://pkg.go.dev/github.com/skosovsky/contexty#TokenCounter) implementation.

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
			contexty.TextMessage(contexty.RoleSystem, "You are a helpful assistant."),
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
- `BuildDetailed(ctx)` returns the final messages plus raw post-eviction named blocks for orchestration.
- `Clone()` returns an independently mutable snapshot of the builder state (use this for safe concurrent use patterns).
- `SetBlockMessages(name, msgs)` replaces a registered block payload with a deep-copied message slice.

```go
type MemoryBlock struct {
	Strategy  EvictionStrategy
	Messages  []Message
	MaxTokens int
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

## Persistence

`contexty` defines storage contracts in the root module. History uses **optimistic concurrency**: each `Load` returns `HistorySnapshot` with `Messages` and a monotonic `Version`. Mutating calls (`Append`, `Save`, `Clear`) take `expectedVersion`; if another writer advanced the thread first, they return `ErrHistoryVersionConflict` and callers should reload and retry (see `Thread.BuildContext`).

```go
type HistorySnapshot struct {
	Messages []Message
	Version  int64
}

type HistoryStore interface {
	Load(ctx context.Context, threadID string) (HistorySnapshot, error)
	Append(ctx context.Context, threadID string, expectedVersion int64, msgs ...Message) error
	Save(ctx context.Context, threadID string, expectedVersion int64, msgs []Message) error
	Clear(ctx context.Context, threadID string, expectedVersion int64) error
}
```

**PostgreSQL:** messages live in your configured table (default `contexty_messages`); OCC uses a companion `{table}_meta` row per `thread_id` (`version BIGINT`). **Redis:** list key `contexty:thread:<id>` plus version key `contexty:thread:<id>:ver`.

### Migration from pre-OCC / cache API

- Replace `Load` → `[]Message` with `snap, err := store.Load(...); msgs := snap.Messages; v := snap.Version`.
- Pass `v` into the next `Append` / `Save` / `Clear`; handle `errors.Is(err, contexty.ErrHistoryVersionConflict)` with reload + retry.
- Remove `CacheControl` from `Message`, `MemoryBlock`, and builder/clone code; it is no longer part of the public model.
- Create `{your_messages_table}_meta` when using the postgres adapter (see adapter tests for DDL).

Use `Thread` to wire a store to a dedicated builder block:

```go
store := testutil.NewMemoryStore()
thread, err := contexty.NewThread(store, "thread-1", "history")
if err != nil {
	log.Fatal(err)
}

builder := contexty.NewBuilder(2000, counter)
builder.AddBlock("instructions", contexty.MemoryBlock{
	Strategy: contexty.NewStrictStrategy(),
		Messages: []contexty.Message{
			contexty.TextMessage(contexty.RoleSystem, "Keep answers concise."),
		},
})
builder.AddBlock("history", contexty.MemoryBlock{
	Strategy: contexty.NewDropHeadStrategy(contexty.DropHeadConfig{}),
})

msgs, err := thread.BuildContext(ctx, builder, []contexty.Message{
	contexty.TextMessage(contexty.RoleUser, "What changed?"),
})
```

Storage adapters live in nested modules:

- `github.com/skosovsky/contexty/adapters/store/postgres`
- `github.com/skosovsky/contexty/adapters/store/redis`

For releases, bump adapter `require github.com/skosovsky/contexty` versions and run `go mod tidy` / `go work sync` as needed so published tags match what consumers resolve.

*(Some specs refer to a generic “store” and “version conflict”; in this module the contracts are named [`HistoryStore`](https://pkg.go.dev/github.com/skosovsky/contexty#HistoryStore) and [`ErrHistoryVersionConflict`](https://pkg.go.dev/github.com/skosovsky/contexty#ErrHistoryVersionConflict).)*

## Storage resilience and execution policies

- **Caller-controlled timeouts:** the library respects `ctx` and does not impose its own storage deadlines.
- **Clean break:** retries, circuit breakers, and bulkheads live outside the core package—typically as a decorator around `HistoryStore`, a pool wrapper, or app-level policy (no resilience types in the root module).
- **Postgres vs generic SQL docs:** resilience write-ups sometimes show wrapping `*sql.DB`; this project’s Postgres adapter uses **pgx** with a `*pgxpool.Pool` (or the connections behind it). Apply timeouts, retries, or pool-level policies around that pool or around a custom `HistoryStore` implementation—the same separation of concerns as with `database/sql`.
- **Transient errors:** PostgreSQL and Redis adapters classify common network and deadline failures so `errors.Is(err, contexty.ErrUnavailable)` is often enough without importing driver-specific types.

| Error                       | Retry? |
| --------------------------- | ------ |
| `ErrUnavailable`            | Yes (with backoff and limits), when your policy allows |
| `ErrHistoryVersionConflict` | No—`Load` first, then reconcile version and retry the write |
| `ErrInvalidThreadConfig`    | No (fix configuration) |
| `context.Canceled`          | Usually no |

Decorator sketch (execute is your retry/backoff policy):

```go
type resilientStore struct {
	base    contexty.HistoryStore
	execute func(ctx context.Context, op func(context.Context) error) error
}

func (s *resilientStore) Load(ctx context.Context, threadID string) (contexty.HistorySnapshot, error) {
	var snap contexty.HistorySnapshot
	err := s.execute(ctx, func(ctx context.Context) error {
		var e error
		snap, e = s.base.Load(ctx, threadID)
		return e
	})
	return snap, err
}

// Wrap Append, Save, and Clear the same way: s.execute(ctx, func(ctx context.Context) error {
//   return s.base.Append(ctx, threadID, v, msgs...)
// })
```

Runnable demo: [`examples/resilient_store`](examples/resilient_store) (stdlib-only retry on `ErrUnavailable` over [`testutil.MemoryStore`](https://pkg.go.dev/github.com/skosovsky/contexty/testutil#MemoryStore)).

## Strategies

| Strategy                           | Behavior                                                                                                                |
| ---------------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| `NewStrictStrategy()`              | Returns `ErrBudgetExceeded` when the block does not fit.                                                                |
| `NewDropStrategy()`                | Drops the whole block when it does not fit.                                                                             |
| `NewDropTailStrategy()`            | Removes trailing messages until the block fits; returns `ErrBlockTooLarge` if one remaining message is still too large. |
| `NewDropHeadStrategy(cfg)`         | Removes older messages from the front, with optional turn atomicity, protected roles, and minimum message count.        |
| `NewSummarizeStrategy(summarizer)` | Replaces an oversized block with a summary message.                                                                     |

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
- `ErrBlockNotFound`: `SetBlockMessages` could not find the named block.
- `ErrInvalidThreadConfig`: `Thread` received a nil store, nil builder, or empty identifiers.
- `ErrHistoryVersionConflict`: `Append` / `Save` / `Clear` saw a newer `version` than `expectedVersion` (reload and reconcile before retrying the write).
- `ErrUnavailable`: transient storage failure from adapters or wrappers; optional retry with backoff (see **Storage resilience** above).

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

See [`examples/full_assembly`](examples/full_assembly) for a complete runnable example that shows strict, drop-head, and drop-tail behavior under one shared token budget. For retry-on-unavailable over an in-memory store, run `go run ./examples/resilient_store`.

## License

MIT. See [LICENSE](LICENSE).
