# Contracts and migration

This document describes the public contracts and host integration boundaries of contexty.

## Transform callback boundaries

Hooks, label/role projection, segment/target formatters and summarizers check cancellation
before and immediately after each host invocation. Cancellation dominates a
simultaneous host error or usable return value; no subsequent callback executes
and no partial compile result escapes. This applies without recording or tracing,
and also to standalone TransformPipeline. Role projection receives an owned
message and can change only the returned role. Summarizers receive owned input:
mutating callback arguments cannot rewrite the original inputs or their lineage.
Named read-only views obey the same role/formatter boundaries. Configured view
budgets are also checked after formatting; an oversized final representation
fails instead of escaping initial admission. Formatting is not rerun for recount.
Standalone budget eviction also validates the actual returned representation
against the invocation's effective limit (including a smaller ApplyWithLimit
allowance). Host strategy errors cannot mask cancellation, and returned content
is owned before final estimation; retaining a callback slice cannot mutate the
accepted result. This gate applies to built-in and custom strategies alike.
An observer does not authorize another stage to ignore cancellation: token and
summary notifications are followed by a context check before further callbacks
or success. Notifications do not change the authoritative content or budget.

## Manifest compaction references

Each captured compaction proposal has a manifest link scoped to the actual main
or named-target channel, identifying its summarize invocation and proposal digest.
Links are finalized only after all output privacy decisions: an omitted summary
remains a valid partial proposal, and the link pins that partial record, not a
pre-privacy draft. The saved compile record and returned manifest have identical
links. Host acceptance/supersession creates a different record digest and never
rewrites the historical compile manifest. Replay returns references without
loading or accepting compaction records. Unknown channels, duplicate proposal
identities, inherited/non-summary invocations and invalid refs fail validation.
Each budget also pins its optional compaction capture profile. Validation derives
the complete required invocation set from current channel-local summarize edges:
target graphs inherit shared preparation edges, but do not inherit main admission,
compaction or formatting edges. Shared edges are not claimed again. Missing
or extra links, a link reassigned to another channel, and inconsistent capture
model/encoding/privacy/estimator/summarizer identities fail before replay output.
Changing capture policy invalidates replay intent even when no summary was needed.

## Final output budgeting

Recorded budgets also pin estimator implementation and effective built-in
parameters. Built-in integer estimators have intrinsic descriptors; fixed weights
and character fallback ratio/non-text weight are copied and serialized. Custom
integer estimators and custom tool counters require WithEstimatorDescriptor.
EstimateReporter supplies its explicit estimator binding and profile; built-in
counter parameters remain pinned beneath that binding. Caller mutation of a
built-in counter after construction cannot change execution or recorded identity.
Changed estimator intent invalidates replay even when output cost stays equal.

Each recorded budget pins its configured summarizer, including idle compiles.
WithSummarizerDescriptor binds a caller implementation to one pipeline; an
explicit CompactionProfile.Summarizer can provide the same binding, and two
declarations must agree. Missing or surplus bindings fail before execution.
Main and targets can use different summarizers: actual summary lineage and
compaction profiles use the local binding, never an aggregate stage fallback.
Changing a binding invalidates exact replay intent; descriptors never serialize
the summarizer implementation or infer identity from a function/type name.

Manifest budgets pin the effective truncation implementation and configuration.
Built-in strict/drop/drop-tail/drop-head strategies have intrinsic identities;
drop-head parameters are normalized, copied and serialized (atomicity, minimum
messages, protected role set). Unused DropHead config is not included for another
strategy. Host strategies require WithTruncationDescriptor in recording mode;
missing identity fails before callbacks or store access. Exact replay rejects
changed truncation intent even when no eviction happened. Descriptors identify
behavior; they do not serialize host strategy objects or infer function identity.

Both the main payload and each budgeted named target are estimated after their
last content-changing operation. Compilation fails with `ErrBudgetExceeded` if
that final representation exceeds its own limit. The final check never runs a
formatter or summarizer again and never silently removes protected input.
Estimator failure wraps `ErrTokenCountFailed`. Negative estimates cannot be used
to bypass the limit.

Cancellation dominates a simultaneous estimator error, successful count or
overflow at the final main/target verification boundary. Check the context
before executing the estimator and immediately after it returns; a canceled
compile returns zero outputs and does not execute later target callbacks.
The same cancellation precedence applies to initial/summary estimates, reporter
per-message/total callbacks and truncation recounts. Estimators receive owned
message containers, including the passive observer count; a callback mutation
cannot change compiled output after its budget verification.
The intrinsic value-type `CharTokenEstimator` has no host callbacks or mutable
configuration and may borrow library-owned working content. Host estimators,
wrappers and counters with custom tool callbacks cannot select that fast path;
the allocation guardrail remains part of verification.

Compile-only transformation remains distinct from persistence. Consumers must
handle overflow errors instead of relying on post-budget expansion succeeding.

## State lifecycle and OCC

An OCC revision is a durable monotonic identity within a reusable conversation
ID. `ClearState` consumes the current revision and erases all messages/artifacts,
including when the ID was previously absent. `LoadState` returns an empty state
with the new revision. Stale append, replace, and clear operations conflict.

Memory retains an empty state; the relational adapter stores an empty snapshot
at the advanced revision. Redis uses an empty data value as a tombstone, a
permanent revision key, and atomic scripts for load/CAS/clear. Payload TTL is set
inside the mutation script. Missing expired payload advances the revision once
and creates a tombstone before returning a read or checking a write. Lua compares
tokens as strings to retain exact integer semantics above the float precision
range. A store not configured with TTL reports missing payload as unavailable.

Consumers must reload after clear or expiry and use the returned token, rather
than assuming zero. Storage owners may erase the revision marker only if the
conversation ID is permanently retired; otherwise old writers could become
valid again. These markers contain no deleted content. Exhausted tokens return
`ErrConversationVersionExhausted`. Old expiring revision-key configurations need
explicit host migration/ID retirement; transparent fallback is not provided.

## Canonical content and lineage

`Descriptor{ID, Revision}` identifies pinned behavior/encoding. Both fields are
opaque host-owned strings and required for reproducible behavior.
`MessageContentRef` encodes the full polymorphic message and canonicalizes JSON
object key ordering/whitespace while preserving array order and numeric
literals. Extensions, provenance, source refs, actor and cache hints participate
in the SHA-256 digest. Codec identity is pinned separately; a digest does not
grant access to content. A changed digest distinguishes transformed revisions of
the same message ID.

`LineageRecord` describes a unique transform invocation, its descriptor and
ordered input/output references, plus an optional host decision reference.
`Lineage` holds those records and known unresolved references. `WithRecord`
returns a defensive copy and checks duplicate invocation IDs, malformed refs,
multiple producers and cycles. Pass-through input/output identity creates no
self-edge. `EncodeLineage` and `DecodeLineage` validate the graph and do not
fetch content or fabricate missing source records. `Clone` copies every slice.

`ContentRef.Occurrence` distinguishes transformation occurrences even when bytes
return to an earlier revision. `WithTraceProfile` pins stage descriptors and the
message codec. Traced requests provide `CompilationID`, existing `Lineage` and
explicit `Origins`. Strict `RequireOrigins` fails with `ErrMissingLineage` rather
than inventing roots; permissive tracing lists unknown roots as unresolved.
New output IDs require an explicit `TraceMapping` to available stage inputs,
except a summarizer whose covered inputs are known to the pipeline. Callback
inputs and returned mapping containers are defensively copied.

A mapping may omit Occurrence to select the current available revision by its
ID/digest. When Occurrence is supplied, it must match the exact available stage
input; a same-content reference to another invocation is not an implicit alias.
Cancellation after mapping stops the stage even when it has no output messages.

Content introduced from an empty stage is still a source revision: strict tracing
requires its declared origin; permissive tracing records an unresolved source
reference and connects it to the generated output. Empty inputs do not fabricate
a resolved root or bypass origin requirements.
An empty rendered envelope is a representation of zero inputs, identified by its
pinned renderer descriptor; it does not introduce or fabricate a source origin.

Shared and target-local passes collect separate graphs. Source, deferred,
merge, hooks, role projection, formatting, summarization, budgeting, current-turn
prompt projection and patch stages record immutable input/output references.
Message-based targets append their own projection records. Text-view rendering
creates a `RenderedOutput` containing the typed text counterpart, projected
labels/source refs, output occurrence and pinned renderer descriptor. Its input
references follow actual wire order (system, history, tools, memory), not generic
snapshot enumeration. Empty rendering is a known transform with no input roots;
it does not fabricate unresolved sources. Shared graphs remain unchanged.

`CompileResult.Transformations` and `CompileProjection.Transformations` now hold
`map[string]TransformChain`, not one record per message. Consumers iterate a
chain for history or call `chain.Final()` for the persistence outcome. Terminal
removals and structural replacements cannot be overridden by later nonterminal
observations. Each hook records its own event; targets cannot mutate shared
records. Summary persistence retains projected labels and source refs.

## Host label projection

Standalone projection snapshots required label types and decoder registrations
before invoking host code. Callback-side configuration changes apply only to a
later operation and cannot relax the current projection's requirements.

`LabelProjectionPolicy.ProjectLabels` receives defensive copies of inputs and
output plus the transform descriptor. It returns host Extensions and optionally
declares a trust upgrade with `DecisionRef`. Conflict should wrap
`ErrLabelConflict`; a declared upgrade without a decision fails with
`ErrInvalidTrustUpgrade`. Contexty does not define trust classes or grant tool
permissions. Output role is not an authorization decision.

`LabelProjection.Project` checks input/output label codecs and required output
types through the provided `ExtensionRegistry`, clones the result and unions
input source refs. Missing codecs fail with `ErrMissingLabelCodec`; labeled
input/output without a host policy fails with `ErrMissingLabelPolicy`.
Metadata-free projection needs no policy. Host policies own the meaning and
correct classification of an upgrade; the library validates the explicit
decision contract rather than interpreting domain-specific label fields.

Label projection requires lossless canonical codec round-trips, not merely a
matching extension type. A declared trust upgrade requires a nonblank host
decision reference; Role changes confer no trust. Cancellation during encoding,
decoding or policy evaluation stops subsequent callbacks and returns no partial
projection. Host policy still defines label meanings and conflicts; core does
not inspect trust classes or grant permissions from content/roles.

## Isolated export

Use `ExportProjection` with an explicit `ExportSelection`, not serialization of
the whole local `CompileProjection`. `MessageIDs` selects accepted revisions
present in `projection.Messages`; `ArtifactPayloadRefs` selects exact canonical
artifact revisions belonging to that projection. The export API accepts no external
artifact collection. Source/InputSnapshot and rendered Text are never copied.
Missing, empty, stale or duplicate selected refs/IDs fail with
`ErrInvalidExportSelection`.

Metadata defaults to no disclosure. `ExportMetadata` separately approves actor,
annotations, source refs, origin, cache, provenance, extension payload types,
artifact type, lineage and transform descriptors. Artifact ownership, budgets,
merge, turn binding, lifecycle and persistence configuration are never copied.
Selecting content approves its complete typed payload; hosts must sanitize any
handles embedded inside a payload before approving that content or extension.

Selecting a message ID approves only the actual public output revision, not
historical raw revisions with the same ID. `LineageRefs` authorize references,
never source payloads. Excluded links report `OmittedInputs`; their digests appear
only with `AllowOpaqueDigests`, without source IDs. Private invocation namespaces
are replaced with local export occurrences. Metadata filtering creates a new
public digest and a separate record; it never falsely attributes sanitized bytes
to the original transform. Ancestors are followed by identity even in imported
graphs with unordered records. Export never fetches content or modifies inputs.

Selected resource dependencies now use native deferred admission, privacy-approved
saved records and explicit resource replay codecs; see [the resource contract](resource-content.md).
Prefix diagnostics use the opt-in [prefix recipe](prefix-diagnostics.md): explicit
ordered boundaries, full semantic identity, freshly required host admission,
previous-manifest invalidation and separate adapter wire confirmation. The core
never performs remote cache operations or changes roles/order to obtain reuse.
Positional selection was removed in favor of exact-ID text replacements. The
[consumer migration guide](migration.md) lists actual replaced contracts,
before/after calls, checkpoint handling and host deployment obligations.

## Host blob storage

The ephemeral reference `adapters/blob/memory` implements provisional/checkpoint
claims and authorized metadata-only availability checks. The [host retention
protocol](blob-retention.md) defines commit ambiguity, abort/release, exact-source
retirement, shared objects, atomic cleanup and durable backend obligations.
The core does not decide permissions or retention. Blob-bearing exact replay
requires WithReplayBlobAvailability with a fresh host scope. Checks are live
metadata-only; no Get/refetch occurs. Missing, expired, denied or mismatched
dependencies return ErrMissingReplayDependency plus the original typed host
error, with zero replay output. Exact duplicate descriptors are checked once;
distinct retention bindings need distinct checks. Host owns claim lifetime beyond
the point-in-time check and must keep claims active during consumption.

`BlobStore` is a host-owned Put/Get port, not a filesystem or network client.
`BlobContent` carries immutable raw bytes and an explicit MIME type. A confirmed
`BlobDescriptor` pins object ID/revision, raw SHA-256 digest, byte length, MIME,
opaque write scope/retention references and ordered source content refs. These
references are metadata, never permissions. Each Get requires a fresh caller
scope reference and a byte bound; the adapter must enforce authorization and the
bound during retrieval. Core also verifies returned length, MIME and digest.

`PutBlob` clones requests before invoking storage and publishes a descriptor only
after successful Put, exact receipt validation and cancellation checking. If a
valid object identity is returned with an error, invalid receipt or cancellation,
the outcome carries a cleanup intent, not a published descriptor. Host decides
whether/when cleanup is legal under claims/checkpoint state. Put failures without
an object identity remain the adapter's cleanup responsibility. No implicit Get,
retry, inline fallback, deletion or access upgrade occurs.

`ResolveBlob` is explicitly byte-bounded. Missing/expired/denied storage errors,
size/media limits and digest mismatch remain typed errors with no partial content.
`BlobResolver.Resolve` adds an explicit host decoder and EstimateReporter to the
bounded read. The request pins a resolution ID, decoder descriptor and input
budget; missing/invalid configuration fails before Get. Verified bytes are passed
as a defensive copy to the decoder. Output IDs must be unique; the complete
decoded message list is checked using the existing report/profile, including
unknown-cost policy and reservations. Overflow returns `ErrBudgetExceeded` with
zero projection and no silent trimming. Quality is reported honestly; a semantic
estimate is not a provider-exact token guarantee. Cancellation prevents subsequent
decode/count steps. A returned lineage edge links the full stored descriptor
identity (including revision, MIME, retention and sources) to decoded outputs;
the raw digest is verified separately. No execution or permission is inferred
from decoded body/roles, descriptor strings or storage refs.

Inline/offload policy, bounded preview, artifact/persistence/replay integration
and the host retention/checkpoint adapter are subsequent required CTX-003 work;
bounded resolution alone does not complete CTX-003.

## Explicit blob selection before budgeting

`BlobOffloader.Project` asks a pinned host BlobOffloadPolicy to choose inline,
offload or reject for immutable typed bytes/source refs. Only offload invokes Put;
reject returns `ErrBlobRejected` and never writes. Inline preserves exact bytes.
Offload validates the host-produced preview byte bound/MIME allowlist before Put;
failed preview never publishes a ref or falls back inline. A confirmed write
returns descriptor plus bounded preview; partial/failed writes preserve the
explicit cleanup intent. No Get, cleanup, retry or authorization inference occurs.
MIME preflight requires a complete type/subtype rather than a bare token or a
wildcard type/subtype. Policy, preview and Put failures return no usable prompt
projection; a partial write exposes only a cleanup intent, not a durable ref.
Plain-text previews must be valid UTF-8 before Put, so JSON encoding cannot
silently replace invalid bytes and expand a supposedly bounded preview.

The threshold recipe chooses inline at/below MaxInlineBytes, reject above
MaxBlobBytes, otherwise requests a host preview. Threshold values are copied and
included in the selection evidence. Preview rendering/semantics stay host-owned;
core does not truncate arbitrary typed payloads or clear trust metadata.

`ProjectArtifact` is explicit host preparation before `CompileSnapshot`, not an
engine-side hidden storage callback. It serializes the complete original
ToolPayload contract as immutable application/json, binds the original
ArtifactContentRef in the Put sources, and returns inline unchanged or a preview
artifact. Offloaded artifacts carry an `ArtifactBlob` descriptor, original ref,
pinned policy/thresholds and a digest of the actual typed preview. The returned
lineage links original artifact to projected artifact; host combines it with
existing ancestry in CompileRequest.Lineage. All existing identity, source,
lifecycle, ownership, budget and persistence fields remain unchanged.

Artifact admission/final prompt budgets count the materialized preview. Snapshot
codecs reject invalid descriptors or altered previews (including append merges)
without fetching storage. Checkpoint/resume and accepted exact replay retain the
descriptor/preview; isolated artifact export deliberately omits blob backend,
scope and retention metadata. Ref presence never authorizes reading. Failed Put
returns no prepared artifact; known uncommitted objects return cleanup intent.
Repeated offload of an already projected artifact is rejected rather than
storing preview as the original. Full CTX-003 still requires historical argument
protections and retention/checkpoint reconciliation adapter.

Artifacts carry host-owned typed Extensions, encoded with explicit type IDs.
ProjectArtifact requires matching request.Extensions decoders before any policy
or storage callback, checks lossless labels with cancellation, and preserves them
unchanged on inline/offload. Materialization copies labels to prompt messages;
normal trace label policy still handles all later transforms/conflicts/upgrades.
Preview and the host-selected provider role never erase or upgrade labels automatically.
Artifact append preserves both sides' opaque labels and the union of SourceRefs;
it does not pick a trust winner. Later host label policy reconciles conflicts.

Conversation snapshot/delta codecs require the registry for labeled artifacts.
UnmarshalArtifactJSON provides standalone registry-bound restoration; plain
json.Unmarshal has no host registry and rejects labeled artifacts. Unknown,
nil, wrong-type or lossy decoders fail explicitly without partial output.
ArtifactContentRef includes extension type/value identity. Exact replay uses
the caller's pinned registry and preserves labels/source ancestry/blob metadata
without fetching storage. Isolated artifact export excludes labels by default;
ExtensionTypes explicitly approves entire selected host payloads, while blob
backend/scope/retention metadata remains excluded. Synchronous JSON/export codecs
do not provide a cancellation port; offload/compile/replay do.

## Historical argument projections and compile manifests

Historical arguments preparation and compile integration:
`BlobOffloader.ProjectHistoricalArguments` selects an exact assistant message ID
and call ID from caller-provided history. Round layout and unique IDs are checked
before policy/Put; an incomplete round is rejected even when the selected call
already has its result. The complete original ToolPayload JSON is stored, with
a whole-message source digest including host approval/operation metadata.
Prompt-only arguments receive the bounded preview and explicit ArgumentsBlob;
ID/name, other calls, results, pending rounds and all host metadata remain exact.
Source and Prompt are independently cloned. Codec round-trip must be lossless;
cancellation stops later callbacks. Failed Put returns cleanup evidence only,
never source/prompt/durable ref or inline fallback. Inline preserves everything.
Presence of ArgumentsBlob is not execution permission or approval; core never
reads a workspace path from arguments and never resolves storage implicitly.
Pass a confirmed offload to `WithHistoricalArgumentProjection` while keeping
CompileRequest.History authoritative and raw. The option defensively freezes
prepared Source/Prompt/ref/selection/lineage; inline needs no option. Compile
validates the current full source message digest, complete round, exact original
argument bytes/descriptor receipt, preview-only shape and declared edge before
content capture or transforms. Missing/stale, altered approval/arguments/preview,
duplicate target or already-projected source fails explicitly with zero result.
Multiple calls in one message can be projected independently without retargeting.
Options are evaluated once and their ordered projection configuration is hashed
in the manifest, without publishing raw option values.

The projection runs before deferred/hooks and budgeting. Its lineage uses the
actual pinned offload policy, preserving labels without a trust change. Later
host transforms still apply normal label policy. TransformChain records an
in-place historical argument change; DerivePersistenceState(codec, profile) returns the
original arguments and opaque approval/operation metadata, never preview bytes.
Main/targets and accepted replay contain the exact projected arguments/ref
without Put/Get; isolated export strips ArgumentsBlob handles/scopes. Do not
substitute Prompt as authoritative persisted history or execute its shortened
arguments. Resume from original persistence and apply the explicit prepared
projection again, or use accepted replay for the already compiled prompt.

Compile-only text replacement is addressed by `TextReplacement{Segment,
MessageID, Text}` via `WithTextReplacement`. IDs and segments are explicit; there
is no first/last/all or role-based selector and no implicit retargeting after
compaction/truncation. Non-history replacements run before hooks/budget; history
replacements run after the history budget and still undergo final output checks.
Unknown/empty segment or ID returns `ErrInvalidTextReplacement`; a target absent
at its execution phase returns `ErrMissingReplacementTarget`, with zero compile
result. Replacements are ordered (later replacement of the same ID wins), retain
non-text parts and metadata, never change source/persistence bytes, and participate
in the private compile-option digest. Use CurrentTurn for active-turn prompt-safe
content rather than positional selection. Resolve variables remain independent.

CompileConfiguration pins the evaluated compile-option digest and ordered active
deferred block placement (slot, name, effective segment/merge policy). Option
values and replacement text are not stored in the manifest. Options are evaluated
once and the same values drive execution. Unknown active deferred segments/merge
policies fail recording configuration before callbacks/store access. Resolver
slots and deferred configurations must agree. Changes invalidate replay even
under identical resolver output. Replacement IDs, segments, ordered text values
and resolve variables all participate in the private digest.

TraceConfiguration records require-origins, durable-identity enforcement and the
canonical required-label type set. Each configured custom decoder is explicitly
bound through TraceProfile.Codecs by registry kind/type/descriptor; label and
message registries are distinct scopes. Built-in default provenance decoders have
intrinsic identities. Missing, duplicate, surplus or invalid decoder bindings fail
before callbacks/store access. Unused configured decoders still participate in
identity. Registry decoder tables and bindings are snapshotted at construction;
later registration cannot change a configured engine or reporter. Manifest and
replay expectations own canonical copies. Changing strictness, label requirements
or codec intent invalidates replay, regardless of unchanged output bytes.

Recording component bindings identify individual host callbacks by a structured
key (kind, optional target/segment, configured index) and pinned descriptor.
RecordProfile.Components is validated against configured hook slots, deferred
resolvers, segment/target formatters, role policy, label policy, trace mapping,
request identity policy and requested view renderers. Missing, duplicate, extra or
incorrectly scoped bindings fail before callbacks/store loading; bindings are not
inferred from function addresses, type names or an aggregate pipeline descriptor.
Nil/unconfigured callbacks have no binding. Canonical binding order is independent
of caller ordering. Profile copies own their binding slices; replay checks them
alongside other current intent. Hook/formatter/resolver lineage uses the actual
component descriptor while retaining the intrinsic stage name. Other descriptors
(budget subcomponents, codecs and compaction links) remain to be completed; this
step alone does not complete the full recording contract.

`WithCompileRecording` opts into a local `CompileManifest` on `CompileResult`.
Recording requires tracing with pinned encoding/stage descriptors. The host pins
pipeline, model, prompt, estimator and rendering identities plus each target's
policy. These descriptors identify configuration, not executable implementations.
The manifest contains ordered normalized input refs by segment, raw/prompt-safe
current-turn refs, the loaded source revision, actual main/target limits and the
final estimator results (captured without an extra estimator invocation), ordered
output refs, rendered text identities, artifact identities, transform chains and
per-output lineage. Intrinsic `LineageRecord.Stage` records actual stage meaning;
dependency collection does not infer it from a host descriptor's name.
It contains no source payloads, callbacks, registries or storage handles.

`CompileRequest.SourceRevision` preserves revision in stateless compiles and
snapshot conversion. Store-backed compiles replace it with the observed revision.
The canonical manifest digest covers every semantic field except its own digest.
Its ID is the caller's compilation identity. Changing content, order, policy,
budget or rendering changes identity. JSON round-trip validates descriptors,
refs, duplicate typed output identities and the self-digest. Main and named
targets have distinct `ManifestOutputKind`; a target named main is unambiguous.
An output's message/rendered refs must be produced by its lineage. Unknown fields
and trailing JSON are rejected. A missing policy descriptor or tracing profile
fails before resolver execution; untraced output cannot claim a pinned record.
Actual captured dependencies and accepted content are separate host-owned
records. `InheritedTransforms` distinguishes ancestry from executions in this
compile; `TransformResults` records the exact current non-source results needed
for replay. Removing or adding an unsupported result fails manifest validation.
Merely building a descriptor-only manifest does not store replay content or
imply acceptance. Included/excluded coverage and explicit reservations are now
recorded. Main and each named target pin their component descriptors and final
budget evidence; configuration is validated before executable callbacks.

## Saved results and exact replay

Content capture is a separate opt-in from descriptor-only manifests. The host
uses `WithCompileContentCapture` with a pinned privacy descriptor and a
`RecordContentPolicy` deciding whether
each immutable typed wire value may be kept. The policy receives defensive copies
and context cancellation. Denial for a content digest dominates all purposes:
raw input forbidden by policy cannot be smuggled into storage as a source-stage
result or an unchanged output. Omitted content remains a reference only.
The host policy must independently classify changed/derived content; the core
does not perform secret detection or treat references as read capabilities.

`SavedCompileRecord` starts proposed. Host `Accept` requires a nonempty decision
reference and complete, digest-valid saved dependencies/results; partial records
cannot become accepted. Storage, expiry, deletion and retention belong to host.
No record format serializes functions, registries or backend handles.
`Supersede` requires an explicit host decision and prevents further exact replay.
Compaction records have their own explicit lifecycle and acceptance decisions;
accepting a saved compile record does not implicitly accept a compaction record.

Required saved content includes actual resolved dependencies, current transform
results and final messages/text/artifacts. Raw/source pass-through content is not
required solely to restore an approved saved transformed result. Thus host policy
can forbid raw retention while permitting replay of saved safe output. A hash or
path alone cannot replace missing required bytes. An omitted dependency still
fails even if some final output remains available.

`Replay` accepts an accepted record, explicit expected input/profile/encoding/
stage/budget identities and the pinned message codec. It validates the complete
manifest and content before returning any output. It restores recorded main and
target messages, text and artifacts without executing hooks, summarizers,
renderers, privacy policies or resolvers. Missing/deleted dependency, mismatched
content/config, unsupported state and inconsistent transform-result coverage
fail explicitly. It never refetches. Required message codecs are verified even
for dependencies not returned as visible output; metadata cannot disappear silently.
Saved text is returned verbatim; semantic digests do not themselves promise a
provider wire request or an identical model answer. `WireSegments` and
`WireArtifacts` return cloned original AST-wire bytes, alongside reconstructed
typed content. Input snapshots and callbacks are not fabricated on replay.

`ReplayExpectationFor` makes a defensive copy for callers deliberately retaining
the saved intent. Supply current desired identities instead if configuration
changed; copying stale intent is not proof that policies are still current.
Errors include `ErrReplayMismatch`, `ErrReplayContentMismatch`,
`ErrMissingReplayDependency`, `ErrReplayCodec`, `ErrUnsupportedReplay`,
`ErrInvalidRecordState` and recorded `ErrBudgetExceeded`.

`Engine.Recompute` requires explicit content and a new compilation ID; it does
not fetch raw inputs from the previous record. A new manifest records
`PreviousRecord` and new transform occurrences. The old graph can be retained as
ancestry, not executed results. Reusing the previous ID fails with
`ErrRecomputeIdentity` before execution. Consumer storage owns accepted records,
deletion and availability; no automatic migration from old checkpoints exists.

## Verification

## Input budget contract

`BudgetRequest` selects exactly one mode: a full window with output/wire
reservations, or an already effective input limit. All values are nonnegative;
reservations cannot exceed the window. Fields belonging to the other mode are
rejected, including unknown modes. Effective mode never subtracts reservations.
The resolved limit applies to preflight, history compaction and final main/target
verification. `ApplyWithLimit` only accepts a nonnegative sub-limit no larger than
the configured effective limit. Invalid configuration fails before callbacks.
Manifest budgets preserve the original request plus the resolved effective limit.
`BudgetConfig.TokenLimit` is replaced by `BudgetConfig.Budget` (clear break).

Manifest validation rejects a resolved limit that disagrees with the original
request. Replay compares the entire request: different reservations remain a
mismatch even if their effective limits happen to coincide. Configuration is
checked before loading the conversation store and before executing callbacks.
Tests cover independent target capacities, one-time system/pending preflight,
zero capacity, maximal integers, invalid/ambiguous requests and bounded history
sub-limits. Optional estimate quality/coverage reports use the reporter contract
below without changing the required integer estimator interface.

## Estimate report contract

An optional reporter wraps TokenEstimator without extending its required methods.
It pins model, estimator, method, encoding, supported content kinds and fallback
policy. Reports bind ordered canonical message refs, segment membership, budget
request and optional manifest/wire refs to a request digest and profile digest.
They contain per-message/per-segment totals and counted/estimated/unknown coverage.
Strict unknown kinds fail before estimation; permissive mode requires a pinned,
positive fallback and marks unknown coverage rather than claiming measured cost.
Unsupported parts are removed from the estimator input and replaced by explicit
fallback cost, so URL length or raw binary bytes cannot disguise unknown media.
The full request is estimated once, not once per segment: shared request overhead
is distributed using per-message weights, never repeatedly charged per segment.
The original request and callback arguments remain isolated. Negative, overflowing
or inconsistent estimator totals fail with typed errors. Built-in legacy counters
cannot be relabelled counted by caller configuration. Final wire evidence and
immutable usage observations remain separate from semantic estimates.

The reporter covers typed text/image/tool parts, explicit binary media and host
extensions through pinned codec/classification policies. Final main and target
checks capture actual reports into compile manifests. Artifact admission uses
the same estimator with explicit local requests and records accepted/excluded
estimates. Replay validates these saved reports without recounting content.

Report codec and observation contract: reports have canonical self-digests,
validated identities, coherent totals/coverage and strict decoding. Forged totals,
duplicate segments and upgraded quality fail with zero results. External wire
counts pin exact wire/request/profile identities and a counter descriptor;
counted requires an explicit accuracy guarantee. Usage observations are separate,
uniquely identified, immutable records and never replace estimates. Wire overflow
uses window minus output reservation (wire overhead is not deducted twice).
Appending observations validates identity and defensively clones the history.

`PartKinds` retains per-message/per-part kind groups without payloads. Validation
checks exact coverage counts and the pinned fallback cost once per unsupported
part, even when a tool part contains both tool and media kinds. The shape is
included in request identity. Report and history self-digests detect corrupted
evidence; they are integrity metadata, not authentication or access capabilities.
`NewEstimateHistory`, `WithWireCount` and `WithUsage` return independent snapshots.
`EncodeEstimateReport`/`DecodeEstimateReport` and history codecs reject invalid
evidence with zero results on decode failure.

## Manifest coverage

Compile estimate integration contract: EstimateReporter implements
the existing TokenEstimator port. Its strict/fallback behavior applies to preflight
and truncation as well as final verification. Final reports retain actual ordered
output segments, original reservations and the same pinned profile. CompileResult
returns independent reports even without recording; recording embeds them in the
manifest and validates output refs, totals, model/encoding/profile and budget.
Missing required reports fail explicitly. Replay restores recorded reports without
estimator execution. To avoid a circular manifest/report digest, an embedded report
has no self-manifest ref; CompileManifest.EstimateFor returns a separate immutable
binding to the final manifest digest. Legacy integer-only estimators remain valid
without claiming report coverage or accuracy.

Contract tests prove independent main/target profiles, actual post-formatter refs
and costs, recorded report codec round-trip, no estimator execution on replay,
missing report rejection, pre-callback model/encoding/estimator mismatch failures,
strict media checks in preflight and reports without recording. Callback-returned
weight slices are frozen before another callback can mutate their backing data.
Artifact admission uses the same main estimator, with actual local requests,
estimates and exclusion evidence. Host extension/media classification is explicit
in estimate profiles; unknown coverage is never silently claimed as zero cost.

Manifest coverage is a deterministic ledger for every input/empty input segment,
resolved dependency and output channel. Entries distinguish included, transformed,
summarized and excluded content, with actual output refs and reasons. Summary
coverage follows graph edges to selected output; an unrelated historical summary
does not count as compaction in this compile. Ledger validation recomputes evidence
from pinned lineage and transformation records, including non-topological graphs.

Artifacts acquire an explicit artifact-ref → materialized-message lineage edge.
Inactive/oversized artifacts remain in normalized input coverage with an explicit
exclusion reason. Current-turn host prompt templates get a separate materialization
node before label projection; raw, prompt-template and persistence-only inputs are
not conflated. Persistence-only projection is excluded from prompt coverage by
definition, not inferred from coincidentally identical bytes.

Coverage describes transport/inclusion, not permission, trust or model execution.
Unknown provenance is never converted to a fabricated source. Decode/replay must
reject missing, duplicate or contradictory coverage even if metadata is otherwise
well-formed. Rolling compaction and explicit round repair use the separate
contracts below; coverage itself never executes those transformations.

`CompileManifest.Coverage` is verified before the manifest self-digest. Invalid
entries return `ErrInvalidCoverage`; failed decode/replay returns a zero result.
Materialization requires pinned `prompt-template` and `artifact` stage descriptors
when those stages execute. Strict tracing accepts the original raw/artifact refs
as origins; it does not require a fabricated independent origin for their derived
prompt messages. Host label validation still applies to materialized outputs.
`ExcludedArtifacts` only accepts known input refs, allowed exclusion reasons and
unique entries that do not contradict selected artifacts.

Contract tests include final target removal of a shared summary, independent
target coverage, budget eviction, empty segments, resolved dependencies, retained
historical summaries, raw/prompt/persistence separation, inactive/oversized
artifacts, missing stage descriptors and decode/replay rejection of ledger holes.

Budget regression tests and common OCC conformance tests cover final expansion,
clear/recreate, stale replace/append/clear and concurrent writers. Real store
integration tests cover the same cases; Redis also covers deterministic payload
expiration without sleeps and exact large-integer revision comparisons.
Lineage tests cover mixed-source summary, same-ID transformed content, defensive
copying, canonical JSON numeric precision, codec round-trip and invalid graphs.
Label tests cover source preservation, codec absence, conflict, role changes and
missing trust-upgrade decision. Compile tests cover mixed-source summary labels,
host decision references, target isolation and message/graph codec round-trips.
Export tests serialize the entire envelope to check private snapshots/handles,
same-ID raw revision exclusion, opaque-digest opt-in and unordered graph ancestry.
Replay tests reconstruct saved records after restart with unchanged execution
counters, byte-identical message wire and text, artifact/label preservation,
deleted dependency failures, codec absence, config mismatch, cancellation and
privacy denial. Recompute tests prove new identity/lineage and no hidden raw load.
Artifact-local budgets use the engine's configured semantic estimator on the
actual materialized message (the common character estimator when unconfigured).
A nil artifact budget imposes no local limit; an explicit zero limit is zero,
and a negative limit is invalid. Estimator failures, negative costs and cancellation
fail compilation. Local exclusion decisions are captured during compilation;
manifest construction must not re-run the estimator or infer exclusion with a
separate byte/rune counter. Local estimates do not replace final output validation.
Inline binary/media is represented by `MediaPart` with explicit MIME and copied
bytes, not by a byte-count text placeholder or a tool control contract. Its codec
uses a hex field and rejects malformed MIME, malformed hex and unknown fields.
`ArtifactContentParts` exposes binary content and an optional separate text
preview without a role; the explicit host materializer selects issued typed parts. Non-text/non-JSON MIME content is media even when supplied in a text or
JSON field. Textual/JSON artifacts retain their explicit render preview.
Common character estimators reject media rather than silently count zero.
EstimateReporter classifies MediaPart as media_payload; strict unsupported cost
fails before counting, while opt-in positive fallback reports unknown quality.
Text-only built-in views reject media; host formatters own supported rendering.
Typed message projections retain MediaPart; their Text is diagnostic only.
Extension classification is host-defined, not inferred from type names. Profile
`Extensions` maps exact type IDs to pinned codec and policy descriptors plus an
explicit MetadataOnly decision. Metadata-only extensions are excluded from token
counting but remain in request identity. All other extensions participate as
`extension` coverage units; unsupported ones fail strict preflight or receive one
positive unknown fallback each, and are removed only from counter inputs, not the
actual output. Known extension costs use the same whole-request estimator.
Every supplied extension requires an available decoder and a valid type ID.
Report records exact ordered extension types per message and participating units;
codec validation checks these against profile classification. Classification or
codec revision changes invalidate profile/request evidence. This does not assign
trust, permissions or domain semantics to host values.
Artifact admission evidence is recorded once in CompileResult.ArtifactEstimates
and the manifest. ArtifactBudgets declares every active local budget request;
each requires exactly one matching estimate with artifact input ref, materialized
message ref, limit, tokens and quality. Reporter-backed admission saves the full
report for that actual message/local limit and uses the same main profile.
Legacy integer admission remains explicitly estimated, never counted. Over-limit
evidence requires a matching exclusion; within-limit evidence cannot claim a
budget exclusion. These are admission observations, not final output totals.
Manifest codecs/replay validate and restore this evidence without counting again.


## Compaction record lifecycle

A CompactionRecord is separate from a saved compile record. It pins ordered
covered message revisions, summary output identity, actual summarization lineage,
model/summarizer/estimator/policy/encoding/privacy descriptors and the budget.
Covered IDs are unique and cannot reuse the summary ID. The summary edge must
match all covered inputs exactly and use the pinned summarizer descriptor.
Raw inputs are not required to be stored; provenance references do not grant read
permission. Result bytes are caller-owned saved content under the declared host
privacy policy. A proposed record can omit result bytes or cost evidence;
acceptance requires both. Summary estimate pins exact output/model/encoding/
estimator/budget, and accepted summary cannot exceed its effective limit.
Acceptance and supersession are explicit immutable host decisions and change the
record digest while retaining its ID. Only accepted records can be replayed.
Replay validates current expected identity/policies/coverage/budget and pinned
codecs, returns saved summary bytes without a summarizer/resolver, and never
refetches deleted content. Record storage and atomic host acceptance remain with
the application. WithCompactionCapture opts a BudgetPipeline into actual summary
proposals and requires tracing, EstimateReporter and compile content capture.
Profile identities match the executing summarize stage, reporter and privacy
policy. Proposals record post-label-projection content and actual sub-budget.
CompileResult.Compactions is populated after all output privacy decisions, so a
later denial removes result bytes here too. Acceptance/storage are never automatic.
Summary cost is counted once for capture; final verification remains
separate. Rolling-tail capture records only the summarized prefix under its
actual remaining capacity; accepted-summary resume is explicit host input.

## Tool round state inspection

InspectToolRoundStates validates canonical assistant-call/contiguous-result blocks
without altering history. All call IDs satisfied means complete; missing results
mean pending unless the host explicitly declares that assistant interrupted.
Complete/pending declarations must agree with local facts. Unknown declarations,
noncanonical call/result roles, duplicate calls/results and orphan results fail
with typed errors. Payload error/progress/control fields do not infer lifecycle
or the outcome of a remote action. Missing IDs retain call order; indexes refer to
the original input. Inspection never creates synthetic results.

RepairInterruptedToolRounds is an explicit, immutable projection operation. Its
policy pins behavior and encoding descriptors and requires a host decision reference for each declared
interrupted assistant. Only missing results of those rounds receive synthetic
ToolResultParts, in call order. Pending and complete blocks are copied unchanged.
The marker explicitly says that no result was recorded and the external outcome
is unknown; it does not claim failure, cancellation, success or approval. Synthetic
messages inherit the assistant's metadata, never an elevated trust label. Their
IDs derive from ordered input references, policy, encoding and decision, not time/randomness.
Returned repair evidence and lineage identify every synthetic output and its
input revisions. The marker is also serialized in each synthetic payload, so it
cannot disappear merely by dropping an auxiliary report. A generated ID collision,
missing identity/decision/codec, non-round-tripping codec, malformed round or
cancellation fails atomically. Caller-owned policy maps are copied before codecs.
Provider-specific validity is not guaranteed; source history is never changed.
BudgetPipeline validates round layout before estimator/summarizer/strategy calls.
The earliest pending round anchors a protected chronological suffix: neither that
round nor later context may be summarized, shortened or evicted. Only the prefix
is eligible for compression/truncation. Its capacity reserves the suffix's cost;
a suffix exceeding capacity returns ErrPendingExceedsBudget without invoking a
summarizer/strategy. Final combined cost is checked because estimator costs need
not be additive. A strategy splitting a complete round returns ErrInvalidToolRound
instead of implicit orphan deletion. Standalone strategies do not provide this
pipeline-level guarantee. Interrupted repair remains an explicit host operation
before budgeting; no missing result is manufactured by the budget pipeline.

## Rolling summary and recent tail

WithRollingSummary(RollingSummaryPolicy) selects an opt-in budget recipe with a
pinned policy descriptor and a positive minimum RecentMessages count. It requires
a summarizer and validates before callbacks/store loading. Whole-block compression
remains the default without this option. Without a CompactionPolicy, fitting input generates no summary. With an explicit
threshold policy, fitting input may be compacted to a soft target. When triggered, the chronological tail is preserved unchanged; a boundary inside a
tool round expands backward to include the entire round. An earlier pending round
expands the protected suffix further. Only the preceding prefix is summarized.
An oversized tail returns ErrRecentTailExceedsBudget without summarization; an
oversized summary or nonadditive combined request returns ErrBudgetExceeded.
This recipe does not silently evict either summary or tail to fit. Current-turn
raw/prompt-safe/persisted semantics remain those of CompileRequest/CurrentTurn.

Compaction capture is optional but, when enabled, its policy descriptor must match
the recipe. Proposals cover only the actual summarized prefix, not the preserved
tail or current turn. Summary identity must not reuse a covered or tail identity.
Manifest budgets save the actual recipe (descriptor and count), and replay rejects
a changed recipe even with an unchanged effective capacity. Host resume supplies
the accepted summary plus prior lineage and new recent messages explicitly. A new
compaction covers that summary revision and newly compacted messages, retains its
ancestors, and does not re-run earlier summarization during exact replay. Accepting
or superseding compactions remains an explicit host decision.

Persistence preserves compiled chronology of surviving source/introduced messages:
a prefix summary precedes its recent tail. In-place compile-only changes still
restore original source/introduced bytes at that position. Source-only retained
messages with no compiled position follow in source order; explicit current-turn
persistence follows its existing policy. This fixes append-at-end replacement
ordering, not a switch to persisting prompt-safe content implicitly.

## Retention and soft compaction

The [retention and compaction budget contract](retention-budget.md) defines
SummaryRequest, BudgetResult/Decision, RetentionPolicy and CompactionPolicy.
Retention applies before any compression or eviction callback, independently of
the chosen strategy. Compaction records carry concrete request capacities and
configuration, and compile manifests bind actual budget-stage decisions.

## Atomic checkpoint commit

ConversationStateStore exposes LoadState, CommitState with an ordered nonempty batch,
and ClearState. Stores apply one explicit ProjectCheckpoint and semantic codec before
atomic publication, consume one OCC revision, and publish no partial batch on failure.
ConversationCodec is lossless and schema-tagged; OCC is independent of schema.
See [checkpoint-store.md](checkpoint-store.md) for policy matrix, Cluster key layout,
unknown network outcomes and host reconciliation boundaries.

## Independent output preparation and selection

Main and targets branch from one owned prepared candidate set before lossy
admission. Composition, policy identity, exact required refs, selected/excluded
units, artifact revisions and estimate profiles are recorded per output.
Accepted replay restores these outcomes without executing policies or resolvers.
Compile is atomic across outputs. See [context projections](context-projections.md)
for composition, admission, final coverage and persistence boundaries.

## Explicit materialization and final output policy

Artifact-bearing compile requires a pinned `ArtifactMaterializationPolicy`;
there is no default role. Materialization decisions bind exact artifact/message
refs. `ResourceResolver.Materialization` must match the engine's pinned identity.
Resource append, blob preview and local estimates use the same chosen typed
representation. Sources/extensions remain artifact-owned; provider role does not
establish trust or execution authority.

One optional `OutputPolicy` accepts/rejects the complete final semantic payload
for each main/named/view output after all ordinary mutations, pending insertion
and patches. Views render only accepted bytes. It preserves IDs/order/tool
structure while permitting typed argument/result byte projection. Exact retention
is checked before this boundary; accepted-ref mapping, round/identity checks and
final recount follow it. No policy means no sanitization. Built-in redaction hooks
are removed; generic transform hooks do not replace final output acceptance.

Prompt-only projection preserves Source/raw persistence. Raw capture and canonical
artifact payload disclosure remain separate host decisions; message export uses
accepted revisions. Records pin policy identity and decisions, and replay does
not execute the callback. See [output-policy contract](output-policy.md).

`ExportSelection.ArtifactPayloadRefs` explicitly approves exact canonical artifact
revisions from `ArtifactContentRef`, independently of `MessageIDs` selecting accepted
prompt messages. This replaces export selection `ArtifactIDs`; projection
`ArtifactIDs` remains participation evidence. Stale, malformed or duplicate payload
refs fail export. A payload ref grants disclosure of the original canonical typed
body, not the output-policy representation; select only messages when handing off
the accepted prompt. Neither sanitization nor metadata allowlisting rewrites that
canonical body.

## Непрозрачное внешнее состояние

Единственная representation — `OpaqueState` envelope над `Message.Extensions`;
контракт, placement, codecs, точные зависимости и явное invalidation описаны в
[opaque-state.md](opaque-state.md). Core не интерпретирует host payload. Проверка
каждого выдаваемого semantic output идёт после host output policy и до финальной
оценки/identity. Fail-closed — default; разрешённый drop фиксируется в evidence.
BYOT payload и pinned codec/profile сохраняются losslessly через store и replay.
Plain render и isolated export автоматически state не раскрывают.
