# contexty

[![Go Reference](https://pkg.go.dev/badge/github.com/skosovsky/contexty.svg)](https://pkg.go.dev/github.com/skosovsky/contexty)
[![Go Report Card](https://goreportcard.com/badge/github.com/skosovsky/contexty)](https://goreportcard.com/report/github.com/skosovsky/contexty)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

`contexty` is a **semantic context engine** for LLM applications: typed message AST, segment-based `ConversationStore`, named views (`RenderView`), unified budgeting, and `Compile()` → `CompileResult` (payload + immutable `Source` + `Introduced` + `DerivePersistenceProjection`).

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
    Options: []contexty.CompileOption{
        contexty.WithResolveVar("locale", "en-US"),
    },
})
toSave := result.DerivePersistenceProjection(contexty.SegmentHistory)
_ = toSave
```

## Compile API

```go
result, err := engine.Compile(ctx, contexty.CompileRequest{
    System:  systemMsgs,
    History: historyMsgs,
    Memory:  memoryMsgs,
    Tools:   toolMsgs,
    Pending: pendingMsgs,
    Options: []contexty.CompileOption{
        contexty.WithEphemeralPatch(contexty.MessageSelector{
            Segment: contexty.SegmentHistory, Role: contexty.RoleUser, Position: contexty.PositionLast,
        }, "REDACTED"),
        contexty.WithResolveVar("locale", "ru-RU"),
    },
})
payload := result.Payload
toSave := result.DerivePersistenceProjection(contexty.SegmentHistory)
```

`CompileResult.Source` is an immutable freeze of input messages (before pipeline mutations). `CompileResult.Introduced` captures pre-transform baselines for payload-born IDs (registered post-deferred, before hooks/patches). Use `DerivePersistenceProjection` for checkpoint persistence instead of parsing `Transformations`.

**Pipeline order:** freeze Source → deferred → ephemeral patches (pre-budget) → hooks → segment formatters → budget preflight → budget(history) → ephemeral patches (post-budget, history + Pending) → payload.

See [ADR-002](docs/adr/002-clear-break-compile-contract.md) and [ADR-003](docs/adr/003-strict-contracts-task14.md).

### Migrating from Task13

1. `PromptOrigin` / `ManifestID` → `Origin` / `TemplateID`.
2. Replace `WithOverlay` with `CompileRequest.Options` (`WithResolveVar`, `WithEphemeralPatch`).
3. Use `RenderView` + `WithNamedView` for classifier projections.
4. Set `DeferredBlock.MergePolicy` for origin/layer collision handling.
5. Persist with `DerivePersistenceProjection`.

### Migrating from Task12

1. Use `CompileRequest` / `CompileResult` instead of snapshot-only compile and `AbstractPayload`.
2. Set `Message.ID` at ingest; use `Attributes` for host metadata (not text patching).
3. Put the current turn in `Pending`, not post-compile append.
4. Pass `Tools` explicitly when needed.
5. Inspect `result.Transformations[msgID]` instead of string diffs on payload.

## Stateless compilation

```go
engine := contexty.NewEngine(
    contexty.WithTransformHooks(contexty.NewRedactionHook()),
    contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
)
result, _ := engine.CompileSnapshot(ctx, contexty.CompileRequest{
    History: msgs,
    Pending: []contexty.Message{currentTurn},
})
```

Observer telemetry (`WithObserver`, `WithBudgetObserver`) behaves the same on `Compile` and `CompileSnapshot`.

## Views (non-mutating render)

Built-in views render all segments (system → history → tools → memory):

```go
snap, _ := store.Load(ctx, "chat-1")
engine := contexty.NewEngine()
xml, _ := engine.RenderView(ctx, snap, string(contexty.ViewLLMXML))
flat, _ := contexty.Render(ctx, snap, contexty.ViewFlatClassifier) // shortcut: NewEngine() + builtin RenderView only
```

Custom named views with budget/formatter:

```go
engine := contexty.NewEngine(
    contexty.WithNamedView("classifier", contexty.ViewConfiguration{
        SourceSegment: contexty.SegmentHistory,
        Budget:        classifierBudgetPipe,
    }),
)
out, _ := engine.RenderView(ctx, snap, "classifier")
```

`Render` / `RenderView` never mutate the input snapshot. They do **not** apply transform hooks — use `Engine.Compile()` when you need redaction or truncation before sending to an LLM.

Built-in view names (`llm_xml`, `flat_classifier`) are resolved before the custom registry; `WithNamedView("llm_xml", …)` does not override the built-in formatter. Custom views join segment messages as plain text (not LLMXML).

## Message origin and cache hints

Attach generation metadata as first-class fields (not `Attributes`):

```go
msg := contexty.TextMessage(contexty.RoleSystem, "persona rules")
msg.Origin = &contexty.MessageOrigin{TemplateID: "agents/sales", LayerID: "persona-v1"}
msg.LLMCache = &contexty.CachePolicyRef{Type: "ephemeral"}
```

Architecture tests forbid tunneling generation metadata into `Attributes`.

## Deferred blocks and merge policies

```go
engine := contexty.NewEngine(
    contexty.WithStore(store),
    contexty.WithConversationID("chat-1"),
    contexty.WithDeferredBlocks(contexty.DeferredBlock{
        Name:        "persona",
        Segment:     contexty.SegmentSystem,
        MergePolicy: contexty.PolicyReplaceByOrigin,
        Resolve: func(ctx context.Context) ([]contexty.Message, error) {
            return []contexty.Message{contexty.TextMessage(contexty.RoleSystem, "dynamic")}, nil
        },
    }),
)
result, _ := engine.Compile(ctx, contexty.CompileRequest{
    Options: []contexty.CompileOption{
        contexty.WithResolveVar("tenant", "acme"),
    },
})
```

Deferred content resolves at compile time and is not persisted unless written to the store separately. Use `contexty.CompileResolveVarFromContext(ctx)` inside `Resolve`.

`MergePolicy` values: `PolicyAppend` (default), `PolicyReplaceByOrigin`, `PolicyDeduplicateByLayer`.

## Persistence projection

After compile, persist checkpoint segments without parsing `Transformations`. Payload-born messages (deferred, summarize) use `result.Introduced` baselines when hooks or patches redact payload text:

```go
toSave := result.DerivePersistenceProjection(contexty.SegmentHistory)
_ = result.Introduced // pre-transform baselines for payload-born IDs
```

Patches and `Pending` are compile-only. `DerivePersistenceProjection` excludes evicted/truncated messages and returns Source originals for formatted messages. If `Pending` alone exceeds `TokenLimit`, compile returns `ErrPendingExceedsBudget`.

## Ephemeral patches

```go
result, _ := engine.Compile(ctx, contexty.CompileRequest{
    Pending: []contexty.Message{currentUserTurn},
    Options: []contexty.CompileOption{
        contexty.WithEphemeralPatch(contexty.MessageSelector{
            Segment:  contexty.SegmentHistory,
            Role:     contexty.RoleUser,
            Position: contexty.PositionLast,
        }, "REDACTED"),
    },
})
```

`MessageSelector.Position`: zero value is `PositionFirst`; an unrecognized value defaults to `PositionLast`. Pre-budget patches apply to non-history segments; post-budget patches apply to history (including merged `Pending`).

## Segment formatters

Register host-side projection before budgeting (e.g. wrap memory in XML):

```go
engine := contexty.NewEngine(
    contexty.WithSegmentFormatter(contexty.SegmentMemory, func(msgs []contexty.Message) []contexty.Message {
        // return formatted messages; preserve IDs when updating content in place
        return msgs
    }),
)
```

## Redaction hooks

```go
engine := contexty.NewEngine(
    contexty.WithStore(store),
    contexty.WithConversationID("chat-1"),
    contexty.WithTransformHooks(contexty.NewRedactionHook()),
)
result, _ := engine.Compile(ctx, contexty.CompileRequest{})
```

Hooks run after deferred resolution and before segment formatters and budgeting. For ad-hoc transforms on a snapshot, use `TransformPipeline`.

## Budget pipeline and truncation

```go
pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
    TokenLimit: 4000,
    DropHead:   contexty.DropHeadConfig{MinMessages: 2},
}, &contexty.CharFallbackEstimator{CharsPerToken: 4})

engine := contexty.NewEngine(
    contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
)
```

`TokenEstimator` is passed to `NewBudgetPipeline`, not to `Engine`. Estimator failures surface as `ErrTokenCountFailed`.

Tool-call turns are truncated atomically by default (`KeepTurnAtomicity` defaults to `true`). Setting `KeepTurnAtomicity` to `false` enables fast-path index truncation at the strategy level; `BudgetPipeline` still repairs orphan tool pairs via `enforceToolPairAtomicity`.

When using a custom `Summarizer`, return a summary with a **new** `Message.ID`. Reusing a truncated message ID prevents the summary from appearing in `DerivePersistenceProjection`.

**Canonical tool-turn layout** for atomic truncation: `RoleAssistant` with `ToolCallPart`(s), then `RoleTool` message(s) with matching `ToolResultPart.ToolCallID`. Use `ToolTurnUsesCanonicalLayout` to validate. In-message call+result in a single `Message` is valid for JSON transport but is not the canonical multi-message turn block.

## Observer (telemetry)

The library does not import OpenTelemetry or other metrics SDKs. Pass your own `contexty.Observer` to receive compile-time events with the same `context.Context` as `Compile()` / `Apply()` (trace correlation).

```go
type metricsObserver struct{}

func (metricsObserver) OnTokensEstimated(ctx context.Context, blockID string, count int) {}
func (metricsObserver) OnNodeEvicted(ctx context.Context, nodeID string, reason contexty.EvictionReason) {}
func (metricsObserver) OnContextSummarized(ctx context.Context, compressionRatio float64) {}
func (metricsObserver) OnPipelineCompiled(ctx context.Context, totalCost int, duration time.Duration) {}

engine := contexty.NewEngine(
    contexty.WithObserver(metricsObserver{}),
    contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
)

// Or attach observer only to budget events:
pipe := contexty.NewBudgetPipeline(cfg, estimator, contexty.WithBudgetObserver(metricsObserver{}))
```

| Callback              | When                                                                                                           |
| --------------------- | -------------------------------------------------------------------------------------------------------------- |
| `OnTokensEstimated`   | After initial token estimate for a budget block (`blockID` = segment name)                                     |
| `OnNodeEvicted`       | Strategy truncation, block drop, or orphan tool-pair repair (`nodeID` = `Message.ID` or fallback hash)         |
| `OnContextSummarized` | After summarizer runs (`compressionRatio` = tokens before / tokens after)                                      |
| `OnPipelineCompiled`  | Successful `Compile()` / `CompileSnapshot()` with total payload cost and duration; failures skip callback only |

`CompileResult.Transformations` is updated even when no `Observer` is configured. Use `contexty.NoopObserver` when telemetry is disabled.

**Observer semantics:**

- `WithObserver` on `Engine` receives `OnPipelineCompiled` only (not budget events).
- `WithBudgetObserver` on `BudgetPipeline` receives budget events. If only `WithObserver` is set, budget callbacks are not emitted.
- The same `Observer` instance may be passed to both `WithObserver` and `WithBudgetObserver`.
- Observer is passive: telemetry estimate failures do not fail `Compile()`.

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

### Adapter contract tests

Postgres and Redis adapters share a minimum integration contract (testcontainers):

| Case                                        | Expected behavior                                                    |
| ------------------------------------------- | -------------------------------------------------------------------- |
| Empty `Load`                                | `Version()==0`, empty segments                                       |
| Append / update / OCC                       | Monotonic version, stale write → `ErrConversationVersionConflict`    |
| `Clear` missing thread, `expectedVersion=0` | No-op                                                                |
| `Clear` stale version                       | `ErrConversationVersionConflict`                                     |
| `Clear` existing thread                     | `Version()==0`, segments empty; other threads isolated               |
| Semantic round-trip                         | `ToolCallPart`, `ToolResultPart`, `Annotations`, `UserProvenance`    |
| Expanded round-trip                         | `ImagePart`, `SystemProvenance`, `Origin`, `LLMCache`, multi-segment |
| Redis: version without payload              | `ErrUnavailable` (corrupt state)                                     |
| Postgres: concurrent first insert           | One success, one `ErrConversationVersionConflict`                    |

Run adapter suites locally when Docker is available (also covered by CI `integration` job):

```bash
go test -v ./adapters/store/postgres/...
go test -v ./adapters/store/redis/...
```

Adapters must use `contexty.ConversationCodec` / `MarshalMessageJSON` — no custom part parsing in storage layers.

## Storage resilience

| Error                            | Meaning                                        |
| -------------------------------- | ---------------------------------------------- |
| `ErrConversationVersionConflict` | OCC mismatch — reload and merge                |
| `ErrUnavailable`                 | Transient storage failure — retry with backoff |

Wrap `ConversationStore` with retry logic on `ErrUnavailable`. Respect `context.Context` deadlines in storage calls.

## Wire JSON contract (v1)

Messages and segments serialize as JSON with explicit discriminators:

- Content parts: `kind` ∈ `text`, `image`, `tool_call`, `tool_result`
- Provenance: `type_id` resolved via `ProvenanceRegistry` (unknown types error at decode)
- Message origin: `origin` object with `template_id`, `layer_id` (optional)
- LLM cache hint: `llm_cache` object (provider-specific fields)

## Architecture guardrails

AST tests in `architecture_test.go` (run via `make test-dod`):

- `TestArchitecture_NoStringHeuristicsForSemantics` — no string-prefix heuristics in semantic core
- `TestArchitecture_NoForbiddenExternalImports` — stdlib + `github.com/skosovsky/contexty/*` only in core
- `TestArchitecture_NoJSONMetadataInTextParts` — no JSON tunneling in `TextPart`
- `TestArchitecture_NoGenerationMetadataInAttributes` — use `Origin` / `LLMCache`
- `TestArchitecture_NoBase64InCore`
- `TestArchitecture_NoRemovedOverlayAPIInCore` — Overlay / `PromptOrigin` / `ManifestID` must not reappear

## Development

```bash
make test              # all modules, race
make test-dod          # DoD + atomicity acceptance subset
make lint
make bench-guardrails  # allocation guardrails (CI gate)
make validate          # lint + test-dod + bench-guardrails + full test
```

Hot-path benchmarks live in `bench_test.go`. Full acceptance gate: `make validate` plus adapter integration tests when Docker is available.

## Policy

Do **not** encode transport metadata in message text or use string heuristics (`strings.HasPrefix`, `strings.Contains`) on message history for business logic. Use typed `ContentPart`, `Annotations`, `Provenance`, and `ProvenanceRegistry`.

## ADRs

- [ADR-001](docs/adr/001-semantic-context-engine.md) — semantic context engine foundations
- [ADR-002](docs/adr/002-clear-break-compile-contract.md) — CompileRequest, Transformations, formatters
- [ADR-003](docs/adr/003-strict-contracts-task14.md) — origin, views, merge, patches, persistence
