# ADR-002: Clear Break — CompileRequest, Transformations, Segment Formatters (Task13)

## Status

Accepted — breaking change to compile API and message model.

## Context

Task12 added stateless `CompileSnapshot`, but compile input remained implicit (store + overlay), eviction traceability relied on text/index matching, and host apps patched payloads after compile (wrapping memory in XML, appending pending turns). `Annotations.RefID` doubled as logical message identity.

## Decision

### Message model

- `Message.ID` — stable logical identity for reconciliation and transformations.
- `Message.Attributes map[string]any` — host metadata (not serialized into message text).
- `Annotations.RefID` — transport-only; `MessageNodeID` uses `Message.ID` or deterministic hash fallback (no RefID).
- `EnsureMessageIDs` / `CompileRequest.Normalize()` assign UUIDs when IDs are empty.

### Compile input and output

```go
type CompileRequest struct {
    System, History, Memory, Tools, Pending []Message
}

type CompileResult struct {
    Payload         AbstractPayload
    Transformations map[string]TransformRecord // key: Message.ID
}
```

- `Compile(ctx, req)` and `CompileSnapshot(ctx, req)` return `CompileResult` (not bare `AbstractPayload`).
- `CompileRequest` is the **single exhaustive** input for `CompileSnapshot`. `Tools` and `Pending` are never loaded from store.
- Store-based `Compile`: non-empty slices in `req` override store segments; `Tools` / `Pending` only from `req`.

### Pipeline order

`deferred → hooks → segment formatters → budget preflight (reserved) → budget(history, availableLimit) → merge Pending → payload`

`AbstractPayload` no longer includes `Overlay`. `Engine.WithOverlay` applies only to deferred `Resolve`.

### Pending turn

- `Pending` is excluded from `BudgetPipeline.Apply` (never evicted).
- After history budget, `Pending` is appended to `History` in the payload.
- Preflight reserves tokens for `System + Memory + Tools + Pending` before history budgeting.
- If reserved exceeds `TokenLimit` and `Pending` alone exceeds the limit → `ErrPendingExceedsBudget`.
- Otherwise reserved overflow → `ErrBudgetExceeded` (strict non-history segments are not silently truncated in MVP).
- Duplicate `Message.ID` anywhere in `CompileRequest` → `ErrDuplicateMessageID` at `CompileRequest.Validate()`.

### Segment formatters

```go
type SegmentFormatter func(messages []Message) []Message
// EngineOption: WithSegmentFormatter(seg, fn)
```

Run after hooks, before budget. Formatters apply in fixed segment order: System → History → Memory → Tools. Formatter token impact is included in preflight reserved.

**Transformation MVP rules:**

| Scenario                               | Record                                              |
| -------------------------------------- | --------------------------------------------------- |
| Same `ID`, content changed             | `ActionFormatted`, `segment_formatter`              |
| ID removed, new IDs added              | old → `replaced_by_formatter`; new → `ActionPassed` |
| Aggregating N→1 without preserving IDs | Out of scope — host owns ID strategy                |

### Transform hooks and Transformations

- Transform hooks record `ActionFormatted` with `transform_hook` / `replaced_by_hook` (same rules as segment formatters).
- `markProtectedPending` does not overwrite `evicted` / `truncated` / `formatted` records.

### Transformations vs Observer

- `CompileResult.Transformations` is the source of truth for host reconciliation (index-free DoD).
- `Observer` remains optional telemetry; eviction records are written to the transformation map even without an observer.

## API replacement map

| Task12 / earlier                              | Task13                                                                  |
| --------------------------------------------- | ----------------------------------------------------------------------- |
| `Compile(ctx)` / `CompileSnapshot(ctx, snap)` | `Compile(ctx, CompileRequest)` / `CompileSnapshot(ctx, CompileRequest)` |
| `AbstractPayload` return                      | `CompileResult` with `Payload` + `Transformations`                      |
| `payload.Overlay`                             | `Engine.WithOverlay` only (deferred resolve)                            |
| Overlay / post-compile pending injection      | `CompileRequest.Pending`                                                |
| Tools from store only                         | `CompileRequest.Tools` required when tools are needed                   |
| `Annotations.RefID` as logical ID             | `Message.ID`                                                            |

## Consequences

- **Breaking** for all compile callers (e.g. kosmify): migrate to `CompileRequest`, read `CompileResult`, assign `Message.ID` at ingest.
- Host must register `SegmentFormatter` for domain-specific projection (XML tags, etc.) before budgeting.
- Migration guide: map old overlay/pending flows to `CompileRequest.Pending`; use `Transformations` instead of diffing payload text.

### Kosmify / host migration checklist

1. Replace `Compile(ctx)` / `CompileSnapshot(ctx, snap)` with `Compile(ctx, CompileRequest)` / `CompileSnapshot(ctx, CompileRequest)`.
2. Assign stable `Message.ID` at ingest (or call `req.Normalize()` once before compile).
3. Move post-compile pending injection to `CompileRequest.Pending` (protected after history budget).
4. Pass `Tools` in `CompileRequest` when tools are needed (no longer loaded implicitly from store).
5. Read `CompileResult.Transformations` by `Message.ID` instead of diffing payload text or indices.
6. Put host metadata in `Message.Attributes`; register `WithSegmentFormatter` for domain projection (XML, tags).
7. Use `Engine.WithOverlay` only inside deferred `Resolve` — not on `AbstractPayload`.

## Related tests

`task13_dod_test.go`, `TestDoD_*` in CI via `make test-dod`.
