# Developer Guide: Semantic Context Engine

## Views

```go
snap, _ := store.Load(ctx, "chat-1")
xml, _ := contexty.Render(ctx, snap, contexty.ViewLLMXML)
flat, _ := contexty.Render(ctx, snap, contexty.ViewFlatClassifier)
```

`Render` never mutates the input snapshot. It does **not** apply transform hooks or budgeting — use `Engine.Compile()` when you need redaction or truncation before sending to an LLM.

## Compile API (Task13)

```go
result, err := engine.Compile(ctx, contexty.CompileRequest{
    System:  systemMsgs,
    History: historyMsgs,
    Memory:  memoryMsgs,
    Tools:   toolMsgs,   // only from req — not loaded from store
    Pending: pendingMsgs, // protected current turn; merged after history budget
})
payload := result.Payload
if rec, ok := result.Transformations["msg-id"]; ok {
    _ = rec.Action // passed | evicted | truncated | formatted
}
```

`CompileSnapshot(ctx, req)` uses the same `CompileRequest` contract without `Store` / `conversationID`. Assign `Message.ID` on ingest (or rely on `Normalize()` UUIDs). Host metadata belongs in `Message.Attributes`, not in message text.

**Pipeline order:** deferred → hooks → segment formatters → budget preflight → budget(history) → merge `Pending` → payload.

See [ADR-002](adr/002-clear-break-compile-contract.md) for breaking changes and pending preflight rules.

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

## Deferred blocks

```go
engine := contexty.NewEngine(
    contexty.WithStore(store),
    contexty.WithConversationID("chat-1"),
    contexty.WithDeferredBlocks(contexty.DeferredBlock{
        Name:    "dossier",
        Segment: contexty.SegmentSystem,
        Resolve: func(ctx context.Context) ([]contexty.Message, error) {
            return []contexty.Message{contexty.TextMessage(contexty.RoleSystem, "dynamic")}, nil
        },
    }),
)
result, _ := engine.Compile(ctx, contexty.CompileRequest{})
```

Deferred content resolves at compile time and is not persisted unless written to the store separately. Use `contexty.CompileOverlayFromContext(ctx)` inside `Resolve` to read vars from `Engine.WithOverlay`.

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

## Overlay and pending

```go
engine := contexty.NewEngine(
    contexty.WithStore(store),
    contexty.WithConversationID("chat-1"),
).WithOverlay(contexty.Overlay{"reason": "scheduled-wake"})

result, _ := engine.Compile(ctx, contexty.CompileRequest{
    Pending: []contexty.Message{currentUserTurn}, // never evicted; reserved in preflight
})
```

Overlay is not part of `AbstractPayload`. If `Pending` alone exceeds `TokenLimit`, compile returns `ErrPendingExceedsBudget`.

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

Events:

| Callback              | When                                                                                                           |
| --------------------- | -------------------------------------------------------------------------------------------------------------- |
| `OnTokensEstimated`   | After initial token estimate for a budget block (`blockID` = segment name)                                     |
| `OnNodeEvicted`       | Strategy truncation, block drop, or orphan tool-pair repair (`nodeID` = `Message.ID` or fallback hash)         |
| `OnContextSummarized` | After summarizer runs (`compressionRatio` = tokens before / tokens after)                                      |
| `OnPipelineCompiled`  | Successful `Compile()` / `CompileSnapshot()` with total payload cost and duration; failures skip callback only |

`CompileResult.Transformations` is updated even when no `Observer` is configured.

Use `contexty.NoopObserver` when telemetry is disabled.

**Observer semantics:**

- `WithObserver` on `Engine` receives `OnPipelineCompiled` only (not budget events).
- `WithBudgetObserver` on `BudgetPipeline` receives budget events (`OnTokensEstimated`, `OnNodeEvicted`, `OnContextSummarized`). If only `WithObserver` is set, budget callbacks are not emitted.
- The same `Observer` instance may be passed to both `WithObserver` and `WithBudgetObserver` to receive all events on one adapter.
- Direct `BudgetPipeline.Apply(...)` works with `WithBudgetObserver` without extra context wiring.
- Observer is passive: telemetry estimate failures do not fail `Compile()`; `OnPipelineCompiled` is simply skipped.

Architecture guardrails (AST tests in `architecture_test.go`):

- `TestArchitecture_NoStringHeuristicsForSemantics` — no `strings.HasPrefix`/`Contains`/`HasSuffix` heuristics in semantic core
- `TestArchitecture_NoForbiddenExternalImports` — core must not import kosmify, metry, langfuse, OpenTelemetry, or other third-party packages (stdlib + `github.com/skosovsky/contexty/*` only; testify allowed in tests)
- `TestArchitecture_NoBase64InCore` — no `encoding/base64` in production core sources

## Storage adapter contract tests

Postgres and Redis adapters share a minimum integration contract (testcontainers):

| Case                                        | Expected behavior                                                           |
| ------------------------------------------- | --------------------------------------------------------------------------- |
| Empty `Load`                                | `Version()==0`, empty segments                                              |
| Append / update / OCC                       | Monotonic version, stale write → `ErrConversationVersionConflict`           |
| `Clear` missing thread, `expectedVersion=0` | No-op                                                                       |
| `Clear` stale version                       | `ErrConversationVersionConflict`                                            |
| `Clear` existing thread                     | `Version()==0`, segments empty; other threads isolated                      |
| Semantic round-trip                         | `ToolCallPart`, `ToolResultPart`, `Annotations`, `UserProvenance`           |
| Expanded round-trip                         | `ImagePart`, `SystemProvenance`, multi-segment (`system`/`history`/`tools`) |
| Redis: version without payload              | `ErrUnavailable` (corrupt state)                                            |
| Postgres: concurrent first insert           | One success, one `ErrConversationVersionConflict`                           |

Run adapter suites locally when Docker is available:

```bash
go test -v ./adapters/store/postgres/...
go test -v ./adapters/store/redis/...
```

## Performance guardrails

Hot-path benchmarks live in `bench_test.go`. CI runs allocation guardrails via `TestBenchGuardrails_*` in `bench_guard_test.go`. Local check:

```bash
make test-dod
make bench-guardrails
make validate   # lint + test-dod + bench-guardrails + full test
```

## Wire JSON contract (v1)

Messages and segments serialize as JSON with explicit discriminators:

- Content parts: `kind` ∈ `text`, `image`, `tool_call`, `tool_result`
- Provenance: `type_id` resolved via `ProvenanceRegistry` (unknown types error at decode)

Adapters must use `contexty.ConversationCodec` / `MarshalMessageJSON` — no custom part parsing in storage layers.

## Policy

Do not use `strings.HasPrefix` / `strings.Contains` on message history for business logic. Use typed `ContentPart`, `Annotations`, and `Provenance`.
