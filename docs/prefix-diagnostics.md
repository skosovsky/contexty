# Prefix diagnostics contract

`BuildPrefixManifest`, `PrefixManifest.Validate`, `DiagnosePrefix` and
`WithPrefixWireConfirmation` implement this contract. The local reference adapter
is runnable with `go run ./examples/prefix_diagnostics`.

This optional recipe diagnoses an explicitly ordered, already compiled prompt.
It is not a cache, a compilation shortcut, or permission to reuse old content.
Run final budgeting, freshness checks and host privacy/trust projection first.
The recipe never changes roles, order, persistence or content to obtain reuse.

`DiagnosePrefix` accepts messages in their actual adapter-facing order and a
`PrefixRecipe`. Boundaries have caller-owned IDs and terminate **after** an exact
message ID. Boundary IDs and message IDs must be unique; boundaries must occur
in increasing message order. The last boundary separates the static prefix from
the variable tail. Missing, repeated or reversed boundaries fail explicitly.
Tail messages do not participate in prefix hashing. `BuildPrefixManifest` admits
only the prefix. `DiagnosePrefix` additionally returns a separate `TailDigest`
over ordered full typed tail content and pinned rendering/encoding/policy/hints.
It therefore freshly admits **all** messages before their respective encoding;
private tail data is never hashed merely to diagnose reuse. Tail changes do not
invalidate the prefix or change its manifest. An empty tail has a deterministic
digest, not an absent/unknown identity.

Renderer, semantic encoding and admission policy have required opaque
descriptors. The host pins the serializer implementation/registry to the encoding
descriptor and revises it whenever its behavior changes. Core snapshots registry
containers but cannot establish identity of arbitrary host code.
`Authorize` is mandatory and is called on an owned copy of each hashed message
on **every** invocation, even when the previous digest matches. The host must
reject stale, over-budget or private/untrusted content according to its policy.
Failure or cancellation returns no partial report. No serializer runs on a
message until the host has authorized it. This does not claim that contexty can
infer host authorization, remote freshness or a provider's wire budget.

Each boundary digest includes the ordered full typed message references, cache
hints and all other serialized metadata, pinned renderer/encoding/policy and
canonical hint capabilities. Optional per-message compaction/offload references
are host evidence of the final projection and participate in identity. They are
not storage handles or permissions. Changed content remains changed content
without such evidence; evidence permits the more specific invalidation reason.

The metadata-only `PrefixManifest` is self-digested and validated before previous
state is used. It contains no message payload. Comparison returns all affected
boundaries in current order, the first affected ID, and deterministic reasons:
content, order, policy, codec, compaction, offload. Initial construction is not
an invalidation or confirmation of stability. Removing a previous boundary is
reported explicitly as an order change; a missing current boundary has no digest.
An incompatible previous renderer fails with `ErrPrefixRendererMismatch` and
does not claim stability. Encoding changes are reported as codec invalidation.
Required hints not supported by the adapter's declared capabilities fail with
`ErrPrefixRequiredHint`. Optional unsupported hints remain hashed metadata.

Wire confirmation is a separate adapter-owned fact, bound to the exact semantic
boundary digest and renderer. Attaching one never changes semantic identity.
Core validates the binding and canonical SHA-256 syntax, not the provider's
actual request. A new diagnostic run never carries old wire confirmation forward,
even for an unchanged semantic prefix. Confirmations cannot assert remote cache
hits, TTL, availability, billing or savings. The adapter must render/count/check
the final wire request itself.

Acceptance tests cover deterministic multiple boundaries, variable-tail
independence, full wire metadata, all invalidation categories, removed boundaries,
malformed previous manifests, invalid boundary order/IDs, unsupported required
hints, renderer mismatch, fresh authorization, denial before encoding,
cancellation dominance, ownership and separate wire confirmation. A local example
shows host admission, diagnostics and explicit wire confirmation with no remote
cache operation.

## Comparison and confirmation API

`DiagnosePrefix(ctx, messages, recipe, previous)` takes an optional previous
`*PrefixManifest` and returns `PrefixDiagnosticReport`. It validates and owns the
previous manifest before admission callbacks; renderer mismatch fails before
encoding or host execution. `Compared` distinguishes an initial run from an
actual comparison. An empty invalidation list is only semantic equality under
the pinned contracts, not a remote cache claim. All host admission checks run
again even on equality, including variable tail admission. The complete input
and serializer containers are snapshotted before any admission callback, so a
host mutation during prefix admission cannot change the later tail digest.

Invalidations for surviving/new boundaries follow current order. Removed
boundaries follow in previous order and carry `Removed: true`. Their current
digest is absent. `FirstAffectedBoundary` selects the earliest affected prefix
termination position across current and removed boundaries; on equal positions
a removed boundary takes precedence. Removing an earlier diagnostic boundary
does not invalidate an otherwise identical surviving prefix. Changing content,
ordered IDs, hint/policy/capabilities, encoding or projection facts produces the
corresponding reasons in the fixed order listed above; reasons may coexist.

`WithPrefixWireConfirmation(report, confirmation)` returns a deeply owned report
with a separate adapter fact. It requires a valid canonical SHA-256 wire digest,
an existing boundary, its exact semantic digest and the same renderer. Duplicate
confirmation for one boundary or stale/mismatched bindings fail explicitly with
`ErrPrefixWireMismatch`. A new `DiagnosePrefix` report has no confirmations.
