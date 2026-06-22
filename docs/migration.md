# Consumer migration

This is a clear break. Update consumers, tests and checkpoint handling together;
there are no aliases for replaced selectors, budget fields or deferred callbacks.
The required `TokenEstimator` and whole-block `Summarizer` interfaces remain
valid abstractions. New evidence/strictness features are explicit opt-ins, not
compatibility modes. Host types, authorization, model selection and storage
ownership stay in the application.

## Test utilities

Replace `contexty.FailingEstimator` with `testutil.FailingEstimator`, importing
`github.com/skosovsky/contexty/testutil`. The root package no longer exports this
test double and provides no compatibility alias. Its error behavior is unchanged.
`contexty.FixedEstimator` remains a supported deterministic estimator; its
configuration continues to participate in recording identity.

This public API removal requires `make release-break`, not `make release-patch`.

## Replaced public contracts

| Previous usage | Current usage | Required consumer change |
| --- | --- | --- |
| `BudgetConfig.TokenLimit` | `BudgetConfig.Budget` | Choose an effective limit or a window with reservations, not both |
| `WithEphemeralPatch`, `MessageSelector`, `MessagePosition`, positional constants | `WithTextReplacement(TextReplacement)` | Resolve exact message IDs in the host; missing IDs are errors, not a positional fallback |
| `ReasonEphemeralPatch` | `ReasonTextReplacement` | Update reason handling; use persistence projection instead of recreating its rules |
| `DeferredBlock.Resolve` returning `([]Message, error)` | `Resolve` returning `(DeferredResult, error)` | Wrap ordinary messages explicitly; return structured resources separately |
| `map[string]TransformRecord` in compile/projection transformations | `map[string]TransformChain` | Inspect all steps or call `chain.Final()` for the effective status |
| Clear/recreate assuming token zero | Reload state and use `state.Version()` | Treat clear and expiry as identity-consuming transitions |

### Budgets and compile-only replacements

Before:

```go
cfg := contexty.BudgetConfig{TokenLimit: inputLimit}
option := contexty.WithEphemeralPatch(contexty.MessageSelector{
    Segment: contexty.SegmentHistory, Position: contexty.PositionLast,
}, safeText)
```

After:

```go
cfg := contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(inputLimit)}
option := contexty.WithTextReplacement(contexty.TextReplacement{
    Segment: contexty.SegmentHistory, MessageID: selectedMessageID, Text: safeText,
})
```

For a full model window use `WindowInputBudget(window, outputReserve,
wireReserve)` instead. Never pre-subtract reservations and then provide them
again. Zero effective capacity is meaningful; an unspecified mode is invalid.
Custom counters/strategies/summarizers need their explicit descriptor bindings
when recording is enabled. Built-in counters pin their actual configuration.

Final main, budgeted target and budgeted named-view representations are checked
after the last content-changing stage. Handle `ErrBudgetExceeded` rather than relying on a
formatter or history replacement bypassing the limit. Protected pending/current
input is not silently removed. Unknown/ambiguous reservations give
`ErrInvalidBudgetRequest`; missing replacement targets give
`ErrMissingReplacementTarget`. Counter errors wrap `ErrTokenCountFailed`;
cancellation dominates simultaneous callback errors and cannot return a partial
successful result. Estimators receive owned message containers and must not
depend on mutating pipeline input.

Role projection can change only its returned role: changes to callback message
parts or provenance are ignored. Summarizers receive owned inputs as well.
Hooks, role policies and segment/target/named-view formatters check cancellation
on return, including when a callback returns usable data or another error.
No later callback runs after cancellation; standalone `TransformPipeline`
obeys the same rule.

Standalone `BudgetPipeline.Apply` and `ApplyWithLimit` also check the final
eviction result against the invocation's effective allowance, even if a custom
strategy claims success. Strategy and summary results are copied before return;
mutating retained host slices cannot change the accepted output. Cancellation
from token/summary observer callbacks prevents success and subsequent stages.

### Deferred callbacks

Before:

```go
Resolve: func(ctx context.Context) ([]contexty.Message, error) {
    return loadSelectedMessages(ctx)
}
```

After:

```go
Resolve: func(ctx context.Context) (contexty.DeferredResult, error) {
    messages, err := loadSelectedMessages(ctx)
    return contexty.DeferredResult{Messages: messages}, err
}
```

A callback error is not partial content. For resources declare exact
`ResourceSelection` entries and `ResourceCodec`, and return corresponding
`ResolvedResource` values in `DeferredResult.Resources`. Discovery, selection and
fresh authorization belong to the host. Reader/projection identities and bounded
bytes must agree with the declaration; display names are not resource keys.
See [resource contracts](resource-content.md) and the
[progressive disclosure example](../examples/progressive_disclosure/main.go).

### Transform status and persistence

Before: `result.Transformations[id].Action` observed only the overwritten record.
After: `result.Transformations[id].Final().Action` derives the effective outcome;
iterate the chain when every transition matters. Use `Lineage` for actual
input/output content refs and transform descriptors, not the action list as a
substitute for provenance.

Persist through `DerivePersistenceProjection` and explicit artifact/checkpoint
operations. A prompt preview or compile-only replacement is not the authoritative
raw input. `CurrentTurn` keeps raw/prompt-safe/persistence forms distinct.
Artifact lifecycle, persistence policy and turn binding remain explicit; artifact
prompt messages do not become ordinary stored messages automatically.

## Durable OCC and existing state

`ClearState(ctx, id, expected)` erases payload and advances identity. Load the
empty state again before any fresh write:

```go
err := store.ClearState(ctx, conversationID, expected)
// Handle err before proceeding.
state, err := store.LoadState(ctx, conversationID)
// Handle err before proceeding.
err = store.ApplyDelta(ctx, conversationID, state.Version(), delta)
```

Stale append/replace/clear now fail with `ErrConversationVersionConflict`. Payload
expiry advances identity before the next read/CAS. Revision markers and empty
tombstones contain no deleted content and must not expire while an ID can be
reused; exhausted tokens return `ErrConversationVersionExhausted`.

Pause old writers during deployment. Old storage that erased its revision on
clear/expiry does not contain enough evidence to infer all previous tokens.
Retire such conversation IDs and migrate approved content to a fresh namespace,
or perform an explicit host-controlled migration with sufficient identity
evidence. Do not guess a safe token or erase markers on reusable IDs. Historical
checkpoint migration/retirement is a host operation; the core does not promise
transparent old-format replay or recover deleted payload.

## Lineage, labels, recording and replay

Enable `WithTraceProfile` with pinned `Encoding`, serializer/codec bindings,
stage descriptors and host `LabelProjection`. Provide `CompileRequest.Origins`
and `Lineage` for strict existing roots; missing origins are not invented.
`Descriptor{ID, Revision}` is an opaque behavior identity, not executable code.
Revise it when host behavior changes; register each configured component slot,
including unused registered codecs. Missing/unknown/lossy codecs and ambiguous
origins are typed errors. Role changes do not grant trust. A declared label
upgrade requires a host `DecisionRef`; conflicts wrap `ErrLabelConflict`.

Enable `WithCompileRecording(recordProfile)` to obtain a `CompileManifest`,
including main/target identities, source revision, resolved dependencies,
coverage, budgets, policies and actual estimates. Descriptor-only recording does
not retain bytes. Enable `WithCompileContentCapture(privacyDescriptor, policy)`
only for content the host permits keeping. Denied raw content cannot be retained
under another capture purpose; approved derived output needs a separate decision.

Accept a complete `SavedCompileRecord` explicitly with `record.Accept(decisionRef)`;
persist it using `EncodeSavedRecord` and restore using `DecodeSavedRecord`.
Partial records cannot be accepted. Exact replay is:

```go
replayed, err := contexty.Replay(ctx, acceptedRecord, currentExpectation, codec,
    contexty.WithReplayResourceCodecs(resourceCodecs),
    contexty.WithReplayBlobAvailability(freshScope, availability),
)
```

Only include options needed by that record. Resource codecs are mapped by actual
resolution IDs and pin the independent message/label topologies. Blob outputs
require fresh authorized metadata-only availability checks. Replay never calls
hooks, summarizers, projectors, readers or blob `Get` to reconstruct missing
bytes. Handle `ErrReplayMismatch`, `ErrMissingReplayDependency` and
`ErrUnsupportedReplay` explicitly. `ReplayExpectationFor` is safe only after
confirming the saved identities still match current intent; blindly copying an
old expectation is not a freshness/authorization check. Recompute is a separate
operation with a new compilation ID, parent ref and lineage.

Compaction acceptance is independent of whole-compile acceptance. Use actual
`CompactionRecord.Covered`, output digest and profile; its `Accept`/`Supersede`
transitions require host decisions. `WithRollingSummary` preserves a recent tail,
current turn and complete tool-round boundaries. Incomplete rounds remain live;
interrupted repair is explicit and synthetic, never a statement of remote outcome.
Orphan/duplicate results are errors, not silently repaired history.

## Isolated consumers, offload and estimate evidence

Do not send an entire `CompileProjection`, its `Source` or `InputSnapshot` to an
isolated consumer. Call `ExportProjection(projection, artifacts, selection,
codec)` and serialize the resulting `ExportEnvelope`. Allowlist actual public
message/artifact revisions and each metadata category; references never authorize
raw payload. Sanitize host extensions before approving them. Blob handles/scopes
are not automatic export metadata.

Use host `BlobOffloader`/`BlobResolver` with explicitly approved scope, immutable
bytes, pinned policy/decoder/reporter and size/token limits. A durable ref exists
only after confirmed `Put`; failed/partial writes return cleanup intent, not a
valid ref. Missing/expired/denied objects, mismatch and limits do not silently
fall back. Retention claims/checkpoint commit, source retirement and collection
are host responsibilities; an active claim prevents collection. The reference
backend is ephemeral, not a production durable store. See
[the retention protocol](blob-retention.md). Historical completed-call arguments
may use `WithHistoricalArgumentProjection` for prompt-only previews; keep raw
history/approval-bound metadata authoritative and never project pending arguments.

For evidence wrap the unchanged estimator interface with `NewEstimateReporter`.
Pin model/method/encoding/capabilities and explicit strict/permissive fallback.
Binary media uses a typed representation, not text/URL length/zero cost. Actual
final reports live in the compile manifest; artifact admission uses the same
estimator with its local budget. External wire observations must match request,
profile and wire identities. Provider counts without a precision guarantee remain
estimated; later usage is separate immutable history, not a rewritten estimate.

## Prefix diagnostics

After final compile and host freshness/budget/trust checks, call
`DiagnosePrefix(ctx, orderedMessages, recipe, previousPrefixManifest)`.
Use exact caller-defined `PrefixBoundary{ID, AfterMessageID}` entries in actual
wire order and pinned renderer/encoding/policy. `Authorize` freshly admits every
hashed message, including variable tail. A previous digest never permits stale or
private reuse. The report separates stable prefix identities, variable
`TailDigest`, first affected boundary and six invalidation categories.

The adapter renders the actual request, verifies hint support and may attach
`WithPrefixWireConfirmation(report, confirmation)` bound to the exact boundary
digest/renderer. Wire digest stays separate; no cache hit, TTL or savings is
asserted. New runs do not inherit old wire confirmation. Handle invalid boundary,
renderer mismatch, unsupported required hint and stale wire confirmation errors.
See [the prefix contract](prefix-diagnostics.md) and
[local adapter](../examples/prefix_diagnostics/main.go).

## Deployment checklist

- Replace the removed APIs and reason/status accesses in all consumers.
- Pin current host configuration, codecs and label policies before recording.
- Migrate/retire unsafe old checkpoint and OCC namespaces explicitly.
- Store only host-approved bytes; accept complete records separately from proposals.
- Wire fresh resource/blob authorization and retention/cleanup protocols.
- Test overflow, denial, cancellation, missing/stale dependencies and private export.
- Run `make validate` and actual adapter integration checks before release approval.
