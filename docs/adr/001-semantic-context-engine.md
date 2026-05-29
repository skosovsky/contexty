# ADR-001: Semantic Context Engine (Task10)

## Status

Accepted — Clean Break migration from string-builder model.

## Context

`contexty` previously assembled LLM context via `Builder`, flat `[]Message`, and `HistoryStore`.
Applications encoded tool cycles in text/Base64 and embedded transport metadata in message bodies.

## Decision

The library is the **final semantic container** for dialogue:

- Typed AST (`Message`, `ContentPart`, `Annotations`, `Provenance`)
- Single `ConversationStore` with named segments
- Non-mutating `Render(ViewType)` projections
- Unified budgeting pipeline (summarize + truncate, tool-pair atomicity)
- `Compile()` → `AbstractPayload` with deferred blocks and overlay
- `CompileSnapshot()` — stateless compile from in-memory `ConversationSnapshot` (no `Store` / `conversationID`)
- Compile order: load → deferred → hooks → budget → payload (budget last for **token accuracy**: hooks and deferred blocks change text length)
- Polymorphic JSON codec with `ProvenanceRegistry` for provenance (wire contract v1: `kind` / `type_id`)
- Structural sharing (copy-on-write) for snapshots/hooks/render
- `ConversationStore` methods use `conversationID` parameter name (DB column may remain `thread_id`)

## API replacement map

| Legacy                                   | Replacement                       |
| ---------------------------------------- | --------------------------------- |
| `Builder` / `Build`                      | `Engine` / `Compile`              |
| `HistoryStore`                           | `ConversationStore`               |
| `Thread`                                 | `Engine` + `ConversationStore`    |
| `TokenCounter`                           | `TokenEstimator`                  |
| `Formatter`                              | `ViewFormatter` + `Render`        |
| `Metadata map[string]any`                | `Annotations` + `Provenance`      |
| `Message.ToolCalls` / text tool encoding | `ToolCallPart` / `ToolResultPart` |

## Invariants preserved from Task9

- `ErrConversationVersionConflict`, `ErrUnavailable`, OCC on mutating store ops
- Caller-controlled `context.Context` deadlines

## Deprecation policy

String heuristics (`strings.HasPrefix`, `strings.Contains`) on history for business logic are **forbidden** in application code integrating with `contexty`. Core package enforces this via `TestArchitecture_NoStringHeuristicsForSemantics` (AST inspection, allowlist for PII redaction in `transform.go`).

## Task12: Architecture guardrails and stateless compile

- `CompileSnapshot(ctx, snap)` compiles without `Store` / `conversationID`; pipeline steps match `Compile()` after load; `OnPipelineCompiled` fires on both entry points
- `TestArchitecture_NoForbiddenExternalImports` — AST import scan; no kosmify/metry/langfuse/OpenTelemetry in core
- `TestArchitecture_NoBase64InCore` — no Base64 encoding heuristics in semantic core
- Shared `Observer` may be wired via both `WithObserver` and `WithBudgetObserver` on the same instance

## Task11: Observe API

- `Observer` interface with `context.Context` as first argument on all callbacks
- `WithObserver` on `Engine` (`OnPipelineCompiled` only)
- `WithBudgetObserver` on `BudgetPipeline` (budget telemetry)
- When both are configured, compile and budget events route to their respective observers
- `OnNodeEvicted` emitted for truncation, budget drop, and orphan repair paths
- Deterministic `nodeID`: `Annotations.RefID` or `{blockID}:index_{n}:{fingerprint}`
- Observer is passive: telemetry estimate failures do not fail `Compile()`
- No external telemetry dependencies in core
