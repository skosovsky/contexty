# Consumer migration

This is a clear break. Update consumers, tests and checkpoint handling together;
there are no aliases for replaced selectors, budget fields or deferred callbacks.
`TokenEstimator` remains the estimator port. `Summarizer` now accepts the typed
`SummaryRequest` with owned messages, actual MaxTokens and soft TargetTokens. New evidence/strictness features are explicit opt-ins, not
compatibility modes. Host types, authorization, model selection and storage
ownership stay in the application.

The [external context cookbook](context-strategies.md) and
[offline evaluation runner](../examples/context_evaluation) are host recipes over
these contracts. They add no transcript/search/model port to core. Preserve
original events in a separate host archive before lossy compilation; saving only
`DerivePersistenceState` does not preserve evicted/truncated originals. Use the
existing resource and blob retention protocols for recovery, not summary text or
private replay capture as an implicit archive policy. Provider quality/usage/cost
remain not measured until an explicitly connected host runner records them.

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

Persist through `DerivePersistenceState(codec, profile)` and explicit artifact/checkpoint
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
err = store.CommitState(ctx, conversationID, state.Version(), delta)
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
isolated consumer. Call `ExportProjection(projection, selection, codec)` and serialize the resulting `ExportEnvelope`. Allowlist actual public
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

## Retention and compaction budget

BudgetPipeline.Apply and ApplyWithLimit return BudgetResult: read Messages for
content and Decision for hard/trigger/target capacities and soft-target status.
Move role selectors from DropHeadConfig to BudgetConfig.Retention.Roles; explicit
MessageIDs and ContentRefs share the same mandatory-content contract. Required
messages and complete rounds cannot be removed by summary or custom eviction.
Impossible retention returns ErrRetentionExceedsBudget rather than empty success.

Optional CompactionPolicy uses TriggerPercent and TargetPercent of the effective
pipeline capacity after reservations. NewCompactionRecord requires the concrete
CompactionExecution; old records lack required request evidence and must be
explicitly retired or migrated by the host. No legacy callback adapter or record
reader is provided. See [the complete contract](retention-budget.md).

## Atomic checkpoints and semantic wire schema

Replace store `ApplyDelta` with `CommitState(ctx, id, loadedVersion, deltas...)`.
One nonempty batch consumes one revision; empty batches return an error. Do not
hard-code successive revisions or blindly retry on a reloaded version. Compile
still does not persist its output. An unavailable response (or conflict after a
retry) does not prove whether this host committed; reconciliation belongs to host.

ConversationCodec now retains all artifacts. Use `ProjectCheckpoint(state, codec, profile)` explicitly
before directly encoding a durable checkpoint. Memory stores use the same projection
and configured semantic codec as durable adapters; host extensions require
`WithMemoryStateCodec`. The wire envelope requires `schema: contexty/conversation/1`;
missing/unknown schema fails. There is no legacy decoder or automatic migration.

Redis keys are now `contexty:checkpoint:<hex namespace>:{c<hex ID>}:ver/data`.
`WithKeyPrefix` supplies the logical namespace, encoded before key construction.
The default logical namespace is `default`. There is no dual read or legacy write.
Hosts must explicitly migrate old checkpoint data and OCC markers together, or
choose fresh conversation identities. Never independently delete active revision
markers. Clear/TTL remain monotonic, without cross-backend transaction promises.

## Independent output composition and admission

Replace `CompileTarget.SourceSegment` with explicit `Segments`. Set
`IncludeCurrentTurn` when a consumer needs active prompt-safe input, and choose
exact `ArtifactRefs` or `IncludeArtifacts` separately. No segment or artifact
inherits from main. Targets now branch before main admission/budgeting; a wider
target can retain input that main excludes. View-only targets render prepared
stored segments without budget guarantees.

Remove `ArtifactBudgetPolicy.Group`: it had no allocator semantics. Host retrieval
and priorities belong in `SelectionPolicy`; core validates exact candidate refs,
mandatory units, complete rounds, deterministic admission and final capacity.
`SelectionDecision` records admission, while final output coverage accounts for
later transforms. Pin policy identity and required refs for recorded replay.

`ResourceArtifactMerge.Prepared` replaces `Admitted`: a shared merge is prepared
before output budgets. An over-cap merged artifact is excluded locally, without
silently restoring its old revision. Each output uses its own artifact estimator.

`ExportProjection` no longer accepts an external artifact collection. Its artifact
allowlist addresses only the projection's participating artifacts. Do not send
`Source`, `InputSnapshot` or `PreparedSnapshot` to an isolated consumer.
Choose one output for persistence and apply host persistence policy explicitly;
there is no automatic union of summaries or checkpoint projections. See
[the complete contract](context-projections.md).

## Explicit materialization and final output acceptance

Configure `WithArtifactMaterialization(ArtifactMaterializationPolicy{...})` for
artifact-bearing compile. Return an explicit provider role and typed parts; the
old implicit `RoleSystem` materialization is removed. `ArtifactContentParts`
provides role-free typed payloads. Message-only compile requires no artifact policy.
Configure `ResourceResolver.Materialization` with the same pinned identity as the
engine; materialization identity participates in resource/replay configuration.

Replace `NewRedactionHook`/`RedactionHook` with a host-owned `OutputPolicy` when a
complete final prompt projection or validation is required. Generic hooks remain
ordinary stages. `WithOutputPolicy` applies once per main/named/view output after
all ordinary mutations and pending/post-budget patches, before final accepted-ref
checks, recount and rendering. Preserve IDs, segment order and tool topology;
argument/result bytes may change. No opt-in means no sanitization guarantee.

Exact required revisions are validated before the boundary; the explicitly
accepted projection supplies their final ref mapping. Prompt changes do not change
Source, raw current-turn persistence or private capture policies. Message export
returns accepted revisions. Canonical artifact payload disclosure is independent
of prompt sanitization; do not assume its bytes were redacted by OutputPolicy.
See [the full output-policy contract](output-policy.md).

`ExportSelection.ArtifactPayloadRefs` explicitly approves exact canonical artifact
revisions from `ArtifactContentRef`, independently of `MessageIDs` selecting accepted
prompt messages. This replaces export selection `ArtifactIDs`; projection
`ArtifactIDs` remains participation evidence. Stale, malformed or duplicate payload
refs fail export. A payload ref grants disclosure of the original canonical typed
body, not the output-policy representation; select only messages when handing off
the accepted prompt. Neither sanitization nor metadata allowlisting rewrites that
canonical body.

## Opaque state and fallible persistence

Store external model state in the single `OpaqueState` extension envelope, with a
host-owned typed `Payload`, explicit `Codec`, `Placement` and `Binding`. Remove
text, tool-result or artificial media wrappers. Register payload decoders with
`RegisterOpaquePayload(typeID, descriptor, decoder)`; ordinary extension registration
does not establish the required pinned opaque codec identity. Configure
`WithOpaqueStatePolicy` for state-bearing compile and `ConversationCodec.OpaqueProfile`
for storage. Core does not infer a protocol from a vendor or payload name.

Replace per-segment `DerivePersistenceProjection` with fallible
`DerivePersistenceState(codec, profile)`, then select segments from the validated
returned state. `ProjectCheckpoint` also requires `(state, codec, profile)`.
Cross-segment dependency validation must happen over the whole chosen state;
handle failures before writing. No compatibility wrapper preserves the old API.
For state-free data, use a normal semantic codec and empty profile descriptor.

State dependency changes fail closed. `OpaqueDropInvalid` is an explicit host
choice with recorded drops, rather than automatic byte repair. Isolated export
needs `OpaqueStateIDs`, a matching `OpaqueProfile` and separately allowed dependency
messages. Default rendering and export omit opaque payloads. External opaque
compaction items and local text summaries have separate lifecycles; see the
[offline fixture recipe](../examples/opaque_state/main.go).

## Task 24 contract break

The [remediation contract baseline](remediation-contracts.md) fixes the selected
semantics. Its execution journal separates implemented API changes from behavioral
gates still being repaired; do not treat the baseline as a passing test report.

- Replace `WithBudgetPipeline(SegmentHistory, pipe)` with `WithBudgetPipeline(pipe)`.
  The removed argument never selected a segment. Compilation budgets the complete
  request and evicts optional history.
- Host provenance implements `ProvenanceType() string` and
  `CloneProvenance() Provenance`; clone owns mutable fields and preserves type.
  Register its decoder and pin custom codec identity in strict profiles. Built-in
  user/system discriminators and payloads retain their wire format.
- DeltaRemoveArtifact uses `ArtifactIDs` and wire `artifact_ids`. Convert old
  artifact-removal `message_ids` explicitly before decoding; applying or decoding
  them now returns `ErrInvalidDeltaIDs`. DeltaRemoveMessages retains MessageIDs.
  State/message formats are not changed by this field migration.

Identity/configuration remediation now requires these consumer updates:

- Supply `TurnID` for missing-ID Pending/CurrentTurn events using
  `NewStableMessageIdentityPolicy`. IDs use turn identity, event kind and ordinal
  within the turn; retry keeps the same ID after history trimming. Historical,
  static and generated messages need explicit IDs or a host identity policy.
  Preserve logical IDs across content revisions; use MessageContentRef for content.
- Use nil System/History/Memory to inherit stored data, and nonnil empty slices
  to clear a segment for this compilation. Tools comes only from the request.
- Provide a conversation ID with a configured StateStore for Compile;
  CompileSnapshot remains stateless. PromptSafe without Raw and unknown
  CurrentTurn persistence policies are errors. An entirely zero turn is absent.
- Role projection covers Pending/CurrentTurn as well as source segments, while
  hooks and segment formatters retain their source-segment scope. Raw writeback
  preserves original roles. Layer deduplication replaces the entire incoming
  (TemplateID, LayerID) group and keeps its order.
- Declare current custom `ResourceCodec.Codecs` independently from saved
  configuration, using the actual decoder bindings and revisions. Mismatches
  fail before decoding. Update bindings whenever decoder behavior changes.
- Fix invalid deferred enums and duplicate/reserved named views at configuration
  time. Their errors are enforced even when recording is disabled. Built-in
  CompileTarget views and registered RenderView names have different scopes.
- Materialized pointer parts are canonicalized to owned values. Typed nil,
  invalid media and tool call/result parts are rejected. Custom provenance
  decoders must return a nonnil value with the registered discriminator.

Budget remediation requires an explicit nonnil estimator in NewBudgetPipeline;
no default character counter is inserted. Invalid built-in weights, typed nil,
negative MinMessages, overflowing or inconsistent estimates return errors.
Compile BudgetDecision capacities and soft percentages now describe the complete
request, including fixed sections and active input. Already fitting history survives
once-per-request overhead accounting. Update decisions/soft-target assertions that
previously used a separately reduced history limit. MinMessages remains an optional
block-size threshold; use RetentionPolicy for protection.

Store remediation changes Redis positive TTL precision to ceiling milliseconds;
zero alone means persistent. Memory cancellation after lock wait returns the context
error without changing the OCC token. Postgres ClearState can remove undecodable
old payload with a known matching token; it writes a standard empty checkpoint
without host codec callbacks. Load/commit retain their lossless codec requirement.
The memory reference store keeps codec execution under its global mutex; callbacks
must not reenter it. See checkpoint-store.md for ownership and concurrency limits.


Event TurnID and policy prefix must be valid UTF-8; malformed byte strings now
return ErrMissingEventIdentity instead of colliding after JSON replacement. Public
result DTOs are owned mutable copies; retain Source intact when deriving
persistence. Quickstart is `go run ./examples/quickstart`; API reference moved
from README into `docs/api-guide.md`. Private session/clone optimizations do not
change checkpoint/message serialization.

Segment formatter options now reject unknown/empty SegmentName and nil callback
at compile preflight with ErrInvalidCompileConfiguration, before store/resolver
effects, in both recording modes and both entry points. To omit a formatter, omit
the option; this API does not define nil as a removal command.
