# Developer Guide: Semantic Context Engine

## Views

```go
snap, _ := store.Load(ctx, "chat-1")
xml, _ := contexty.Render(ctx, snap, contexty.ViewLLMXML)
flat, _ := contexty.Render(ctx, snap, contexty.ViewFlatClassifier)
```

`Render` never mutates the input snapshot. It does **not** apply transform hooks or budgeting — use `Engine.Compile()` when you need redaction or truncation before sending to an LLM.

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
payload, _ := engine.Compile(ctx)
```

Deferred content resolves only at `Compile()` and is not persisted unless written to the store separately.

`Compile()` order: load snapshot → resolve deferred blocks → transform hooks (redaction) → budget → payload. Deferred content is therefore redacted by hooks. Use `contexty.CompileOverlayFromContext(ctx)` inside `Resolve` to read overlay vars from `Engine.WithOverlay`.

## Overlay

```go
engine := contexty.NewEngine(
    contexty.WithStore(store),
    contexty.WithConversationID("chat-1"),
).WithOverlay(contexty.Overlay{"reason": "scheduled-wake"})
payload, _ := engine.Compile(ctx) // payload.Overlay is ephemeral, not stored
```

## Redaction hooks

```go
engine := contexty.NewEngine(
    contexty.WithStore(store),
    contexty.WithConversationID("chat-1"),
    contexty.WithTransformHooks(contexty.NewRedactionHook()),
)
payload, _ := engine.Compile(ctx)
```

Hooks run after deferred resolution and before budgeting inside `Compile()`. For ad-hoc transforms on a snapshot, use `TransformPipeline`.

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
