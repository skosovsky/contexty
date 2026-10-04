# Explicit strategy composition

Run `go run ./examples/context_composition` from the repository root. The offline
fixture keeps a completed tool result in host-owned original evidence, creates a
separate retrieval artifact, and calls `BlobOffloader.ProjectArtifact` before
`Compile`. The active tool payload is never rewritten. Host materialization
explicitly chooses `RoleUser` for preview and memory evidence.

The library rolling-summary pipeline compacts the old prefix and preserves two
recent messages. The deterministic summarizer copies a protected constraint from
its actual inputs; this demonstrates mechanics, not LLM summary quality. The
preview and summary omit the original access code. `BlobResolver` later reads and
verifies the original typed payload under a fresh scope, MIME/byte bounds and an
explicit semantic estimate budget. Denied and stale reads fail through the real
library/backend contracts.

The host stores an ephemeral checkpoint before `CommitCheckpoint`, retires its
source, demonstrates that `Collect` fails while the checkpoint claim is active,
then authoritatively retires that checkpoint, releases its claims and collects.
Unknown checkpoint outcomes must retain provisional claims for reconciliation.
This follows [the existing retention protocol](../../docs/blob-retention.md).
Neither the local checkpoint map nor `adapters/blob/memory` survives a process
restart. A production host must provide durable bytes, claims, source retirement
markers, identity tombstones and authoritative checkpoint storage; swapping a
memory adapter does not establish that durability.

Memory is a typed host fact (`key`, `value`, `version`, `sources`) encoded with
`StructuredPayload` and carried in a `MemoryBlock` with matching `SourceRefs`.
The host explicitly accepts version 2 over conflicting version 1 and records its
policy decision. Core handles lifecycle/materialization; it never extracts truth
or chooses a winner. The report states provider usage is `not measured`.

Offload reduces issued prompt content. Exact replay capture may still store
private original bytes: enabling replay capture is a separate retention decision,
not a guarantee of minimization. This recipe does not enable exact replay capture.
