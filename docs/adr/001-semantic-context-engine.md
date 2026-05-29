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
- Compile order: load → deferred → hooks → budget → payload
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

String heuristics (`strings.HasPrefix`, `strings.Contains`) on history for business logic are **forbidden** in application code integrating with `contexty`.
