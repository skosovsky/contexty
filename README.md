# contexty

[![Go Reference](https://pkg.go.dev/badge/github.com/skosovsky/contexty.svg)](https://pkg.go.dev/github.com/skosovsky/contexty)
[![Go Report Card](https://goreportcard.com/badge/github.com/skosovsky/contexty)](https://goreportcard.com/report/github.com/skosovsky/contexty)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

`contexty` is a **semantic context engine** for LLM applications: typed message AST, segment-based `ConversationStore`, non-mutating views, unified budgeting, and `Compile()` → `CompileResult` (payload + transformations by `Message.ID`).

## Installation

```bash
go get github.com/skosovsky/contexty
```

Requires Go 1.26+.

## Quick Start

```go
ctx := context.Background()
store := contexty.NewMemoryConversationStore()

s0, _ := store.Load(ctx, "chat-1")
_ = store.UpdateSegment(ctx, "chat-1", s0.Version(), contexty.SegmentSystem, []contexty.Message{
    contexty.TextMessage(contexty.RoleSystem, "You are helpful."),
})
_ = store.AppendSegment(ctx, "chat-1", 1, contexty.SegmentHistory,
    contexty.TextMessage(contexty.RoleUser, "Hello"),
)

engine := contexty.NewEngine(
    contexty.WithConversationID("chat-1"),
    contexty.WithStore(store),
    contexty.WithBudgetPipeline(contexty.SegmentHistory, contexty.NewBudgetPipeline(
        contexty.BudgetConfig{TokenLimit: 4000},
        contexty.CharTokenEstimator{},
    )),
)

	result, err := engine.Compile(ctx, contexty.CompileRequest{
		Pending: []contexty.Message{contexty.TextMessage(contexty.RoleUser, "Current turn")},
	})
	_ = result.Payload.FlattenMessages()
```

See [Developer Guide](docs/developer-guide.md), [ADR-001](docs/adr/001-semantic-context-engine.md), and [ADR-002](docs/adr/002-clear-break-compile-contract.md).

## Views (non-mutating render)

```go
snap, _ := store.Load(ctx, "chat-1")
xml, _ := contexty.Render(ctx, snap, contexty.ViewLLMXML)
flat, _ := contexty.Render(ctx, snap, contexty.ViewFlatClassifier)
```

## Storage adapters (Postgres / Redis)

```go
import postgresstore "github.com/skosovsky/contexty/adapters/store/postgres"

store := postgresstore.New(pool)
snap, err := store.Load(ctx, conversationID)
err = store.AppendSegment(ctx, conversationID, snap.Version(), contexty.SegmentHistory, msg)
```

Schema (Postgres):

```sql
CREATE TABLE contexty_conversations (
    thread_id VARCHAR(255) PRIMARY KEY,
    version BIGINT NOT NULL DEFAULT 0,
    segments JSONB NOT NULL DEFAULT '{}'
);
```

## Storage resilience

| Error                            | Meaning                                        |
| -------------------------------- | ---------------------------------------------- |
| `ErrConversationVersionConflict` | OCC mismatch — reload and merge                |
| `ErrUnavailable`                 | Transient storage failure — retry with backoff |

Wrap `ConversationStore` with retry logic on `ErrUnavailable`. Respect `context.Context` deadlines in storage calls.

## Development

```bash
make test              # all modules, race
make test-dod          # DoD + atomicity acceptance subset
make lint
make bench-guardrails  # allocation guardrails (CI gate)
make validate          # lint + test-dod + bench-guardrails + full test
```

## Deprecation policy

Do **not** encode transport metadata in message text or use string heuristics on history. Use `Annotations`, typed `ToolCallPart` / `ToolResultPart`, and `Provenance` with `ProvenanceRegistry`.
