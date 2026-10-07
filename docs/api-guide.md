# Detailed API guide

Run the [quickstart](../examples/quickstart/main.go) first.

## Compile API

```go
result, err := engine.Compile(ctx, contexty.CompileRequest{
    System:                 systemMsgs,
    History:                historyMsgs,
    Memory:                 memoryMsgs,
    Tools:                  toolMsgs,
    TurnID:                 "chat/turn-1",
    CurrentTurn:            &currentTurn,
    IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("chat"),
    RequireDurableIdentity: true,
    Targets: []contexty.CompileTarget{{
        Name:          "classifier_history",
        Segments: []contexty.SegmentName{contexty.SegmentHistory},
        Budget:        classifierBudgetPipe,
    }},
    Options: []contexty.CompileOption{contexty.WithResolveVar("locale", "ru-RU")},
})
if err != nil { return err }
payload := result.Payload
toSave, err := result.DerivePersistenceState(contexty.DefaultJSONSerializer(), contexty.Descriptor{})
if err != nil { return err }
classifier := result.Projections["classifier_history"]
```

`CompileResult.Source` is an owned mutable copy of normalized input messages (before pipeline mutations). `CompileResult.NormalizedSnapshot` and `CompileResult.Writeback` expose durable ID normalization and checkpoint writeback intent. `CompileResult.Introduced` captures pre-transform baselines for payload-born IDs (registered post-deferred, before hooks/patches). Use `DerivePersistenceState` for checkpoint persistence instead of parsing `Transformations`.

**Pipeline order:** normalize IDs/current turn → freeze Source → historical argument projection → authorized deferred resolution/merge → freeze shared prepared candidates → independently for main and each target: scope/selection/admission → pre-budget patches → hooks/role projection/segment formatters → history budgeting with protected current turn → post-budget patches → final OutputPolicy → accepted-content checks/recount → rendering. Compile options and shared resolvers run once; output selection runs once per output.

The current clear-break contract is summarized below and in the [migration guide](migration.md).

### Migrating origin, projection and persistence APIs

1. Removed prompt-origin aliases now map to `Origin` / `TemplateID`.
2. Replace `WithOverlay` with `CompileRequest.Options` for resolve vars and `CompileRequest.CurrentTurn` for prompt-only current-turn projection.
3. Use `CompileRequest.Targets` and `CompileResult.Projections` for classifier projections built from the same compile pass.
4. Set `DeferredBlock.MergePolicy` for origin/layer collision handling.
5. Persist with `DerivePersistenceState`.

### Migrating snapshot-only compilation

1. Use `CompileRequest` / `CompileResult` instead of snapshot-only compile and `AbstractPayload`.
2. Set `Message.ID` as the semantic node ID; use `SourceRefs` for external identity and typed `Extensions` for host metadata.
3. Put the active user input in `CurrentTurn`; reserve `Pending` for low-level protected pending messages.
4. Pass `Tools` explicitly when needed.
5. Inspect `result.Transformations[msgID]` instead of string diffs on payload.

## Stateless compilation

```go
engine := contexty.NewEngine(
    contexty.WithBudgetPipeline(pipe),
)
result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
    History:                msgs,
    TurnID:                 "chat/turn-1",
    CurrentTurn:            &currentTurn,
    IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("snapshot"),
    RequireDurableIdentity: true,
})
```

Observer telemetry (`WithObserver`, `WithBudgetObserver`) behaves the same on `Compile` and `CompileSnapshot`.

## Named Compile Targets

Use compile targets when a classifier, router, evaluator, or secondary provider needs its own context from shared preparation, before main loses messages to admission or budgeting:

```go
result, _ := engine.Compile(ctx, contexty.CompileRequest{
    CurrentTurn: &currentTurn,
    Targets: []contexty.CompileTarget{{
        Name:          "classifier_history",
        Segments: []contexty.SegmentName{contexty.SegmentHistory},
        Budget:        classifierBudgetPipe,
    }},
})
classifier := result.Projections["classifier_history"]
_ = classifier.Text
```

`CompileTarget.Segments` selects ordinary messages from explicit segments. Artifacts are separate candidates: choose exact `ArtifactRefs` or set `IncludeArtifacts` for all prepared visible artifacts. Set `IncludeCurrentTurn` to include the prompt-safe active turn and pending messages. Empty composition produces an empty output; nothing inherits from main.

`CompileProjection` owns final `Messages`, `Snapshot`, participating `Artifacts`/`ArtifactIDs`, transforms, selection and estimate evidence. `InputSnapshot` is shared prepared input; `Source` is normalized input before preparation. These are local diagnostics, not consumer transport. Use `ExportProjection(projection, selection, codec)` for an allowlisted envelope.

A target `View` renders prepared stored segments and is mutually exclusive with composition, selection, budget and formatter settings. It has no budget guarantee and does not append the active current turn. For independent budgeted messages, use explicit composition instead.

`WithSelectionPolicy` configures main selection; `CompileTarget.Selection` configures a target. A host policy selects exact `ContextCandidate.Ref` values with integer priorities. Core admits required units first, then higher priority, breaking ties by preparation ordinal and preserving conversation order. Complete tool rounds remain atomic. `SelectionDecision` describes admission; later transforms and budgeting may change final coverage. Main and targets may use different estimators/profiles, without sharing estimates.

Choose persistence explicitly: `result.DerivePersistenceState(codec, profile)` validates the complete restored state and derives main persistence with compile-only changes restored. A target `Snapshot` is an explicit prompt-state choice, not automatic durable writeback; apply the host's persistence policy and `ProjectCheckpoint(state, codec, profile)` before committing it. Summaries from different outputs are never merged automatically.

See [the output contract](context-projections.md) and runnable [two-consumer example](../examples/context_projections/main.go).

## Views (non-mutating render)

`Render` / `RenderView` remain available for read-only snapshot inspection. They do not run the compile pipeline, do not apply transform hooks, and do not see `CurrentTurn`.

Built-in views render all stored segments (system → history → tools → memory):

```go
snap, err := store.LoadState(ctx, "chat-1")
if err != nil { return err }
xml, _ := contexty.Render(ctx, snap, contexty.ViewLLMXML)
flat, _ := contexty.Render(ctx, snap, contexty.ViewFlatClassifier)
```

For classifier/router/evaluator projections, prefer named compile targets.
`RenderView` is for already-materialized snapshot inspection, not compilation.

## Messages, Actors, and Source Refs

`Role` is only the provider-facing role (`system`, `user`, `assistant`, `tool`). Use `Actor` for participant identity and `SourceRefs` for host-owned IDs:

```go
msg := contexty.TextMessage(contexty.RoleUser, "I need help")
msg.Actor = &contexty.Actor{Kind: "customer", ID: "actor-1", DisplayName: "Customer"}
msg.SourceRefs = []contexty.SourceRef{{
    Namespace:    "messages",
    Kind:         "external",
    ID:           "msg-1",
    CheckpointID: "stable-1",
}}
```

Attach generation metadata as first-class fields:

```go
msg := contexty.TextMessage(contexty.RoleSystem, "persona rules")
msg.Origin = &contexty.MessageOrigin{TemplateID: "agents/sales", LayerID: "persona-v1"}
msg.LLMCache = &contexty.CachePolicyRef{Type: "ephemeral"}
```

Configure role projection when actor-aware messages must be rendered with provider roles:

```go
engine := contexty.NewEngine(
    contexty.WithRoleProjectionPolicy(contexty.RoleProjectionFunc(func(msg contexty.Message) (contexty.Role, error) {
        if msg.Actor != nil && msg.Actor.Kind == "system_alert" {
            return contexty.RoleSystem, nil
        }
        return msg.Role, nil
    })),
)
```

Naked attributes are not part of the semantic contract.

## Tool Payloads and Tool Rounds

Tool calls and results carry typed payloads, not text prefixes:

```go
args, err := contexty.StructuredPayload(struct {
    Query string `json:"query"`
}{Query: "typed"})
_ = err

assistant := contexty.Message{
    Role: contexty.RoleAssistant,
    Parts: []contexty.ContentPart{contexty.ToolCallPart{
        ID:        "call-1",
        Name:      "lookup",
        Arguments: args,
    }},
}
tool := contexty.Message{
    Role: contexty.RoleTool,
    Parts: []contexty.ContentPart{contexty.ToolResultPart{
        ToolCallID: "call-1",
        Name:       "lookup",
        Payload:    contexty.TextPayload("result"),
    }},
}
round, err := contexty.ToolRoundFromMessages([]contexty.Message{assistant, tool}, 0)
_ = round
_ = err
```

## Deferred blocks and merge policies

```go
engine := contexty.NewEngine(
    contexty.WithStateStore(store),
    contexty.WithConversationID("chat-1"),
    contexty.WithDeferredBlocks(contexty.DeferredBlock{
        Name:        "persona",
        Segment:     contexty.SegmentSystem,
        MergePolicy: contexty.PolicyReplaceByOrigin,
        Resolve: func(ctx context.Context) (contexty.DeferredResult, error) {
            return contexty.DeferredResult{Messages: []contexty.Message{contexty.TextMessage(contexty.RoleSystem, "dynamic")}}, nil
        },
    }),
)
result, _ := engine.Compile(ctx, contexty.CompileRequest{
    Options: []contexty.CompileOption{
        contexty.WithResolveVar("tenant", "acme"),
    },
})
```

Deferred content resolves at compile time and is not persisted unless written to the store separately. Use `contexty.CompileResolveVarFromContext(ctx)` inside `Resolve`.

`MergePolicy` values: `PolicyAppend` (default), `PolicyReplaceByOrigin`, `PolicyDeduplicateByLayer`.

## Persistence projection

After compile, persist checkpoint segments without parsing `Transformations`. Payload-born messages (deferred, summarize) use `result.Introduced` baselines when hooks or patches redact payload text:

```go
toSave, err := result.DerivePersistenceState(contexty.DefaultJSONSerializer(), contexty.Descriptor{})
if err != nil { return err }
_ = result.Introduced // pre-transform baselines for payload-born IDs
```

Patches and `Pending` are compile-only. `DerivePersistenceState(codec, profile)` excludes evicted/truncated messages and returns Source originals for formatted messages. It validates cross-segment opaque dependencies and returns an error rather than an invalid durable state. Use the same registered codec and receiving profile as the store; an empty profile is sufficient only for state without opaque envelopes. If `Pending` alone exceeds the effective input limit, compile returns `ErrPendingExceedsBudget`.

## Current Turn and Identity

Use `CurrentTurn` for active input that needs different provider-facing and checkpoint-facing representations:

```go
turn := contexty.NewCurrentTurn(contexty.TextMessage(contexty.RoleUser, "raw input")).
    WithPromptSafe(contexty.TextMessage(contexty.RoleUser, "redacted input")).
    WithPersistence(contexty.CurrentTurnPersistRaw)

result, err := engine.Compile(ctx, contexty.CompileRequest{
    TurnID:                 "chat/turn-1",
    CurrentTurn:            &turn,
    IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("chat"),
    RequireDurableIdentity: true,
})
if err != nil { return err }
_ = result.Writeback.Snapshot
_ = result.Writeback.Messages
_ = err
```

`CurrentTurnPersistRaw` stores the original input, `CurrentTurnPersistPromptSafe` stores the prompt-safe representation, and `CurrentTurnPersistNone` skips current-turn checkpoint persistence. When `RequireDurableIdentity` is true, missing message IDs require an explicit `IdentityPolicy`; otherwise compile fails with `ErrMissingIdentityPolicy`.

## Host blob storage

`BlobStore` is an application-provided Put/Get port. `PutBlob` validates immutable object metadata (revision, SHA-256, byte length, MIME, sources and opaque scope/retention refs) before publishing a ref. Failed/unvalidated writes with a known object ID return a cleanup intent; the application reconciles it under retention claims and checkpoint state, rather than deleting automatically.

`ResolveBlob` requires a fresh read scope, explicit byte bound and MIME allowlist. The adapter enforces authorization and bounded retrieval; core verifies the returned bytes. A descriptor is not permission. Descriptor codecs contain metadata only and never fetch bytes.

`adapters/blob/memory` provides an ephemeral reference host backend with provisional and checkpoint claims, atomic commit/release, exact-source retirement and claim-safe cleanup. It requires host authorization for every action; `CheckBlob` checks availability without fetching bytes. Follow the [retention handoff protocol](blob-retention.md) and executable example. Durable deployments must persist this state in their own backend.

For blob-bearing exact replay pass `WithReplayBlobAvailability(freshScope, backend)` explicitly. Replay validates live dependency metadata before returning any output, without Get/refetch. Missing/expired/denied/mismatched dependencies wrap `ErrMissingReplayDependency` while retaining the original host error; absent availability capability fails closed. Keep retention claims active through consumption of returned refs. Records without blob references need no backend.

`BlobResolver.Resolve` additionally uses an explicit host decoder, pinned decoder identity and `EstimateReporter` to enforce the requested input budget/reservations on all decoded messages. It returns cost and lineage evidence, not silent trimming or fallback. Estimated counts remain estimates, not provider-exact guarantees.

`BlobOffloader.ProjectArtifact` explicitly prepares an artifact before compile. A host policy selects inline/offload/reject; `NewBlobThresholdPolicy` supplies a byte-threshold recipe with a host preview renderer. Offload stores the complete immutable ToolPayload JSON contract, returns a bounded preview with `ArtifactBlob` metadata, and preserves identity/source/lifecycle/budget/persistence fields. Pass the prepared artifact and its lineage into `CompileRequest`; merge that graph with existing lineage when present. Artifact admission and final prompt budgets count the preview, not the original bytes. Checkpoint/resume and exact replay preserve metadata without fetching storage. Altered previews fail digest validation; isolated export does not disclose storage handles. The host still owns authorization, checkpoint commit and retention reconciliation; no automatic offload, Get or deletion occurs.

Artifacts can carry host-owned `Extensions`. Supply matching extension decoders in `BlobArtifactRequest.Extensions` before offload and in conversation/replay codecs. Labels are retained in preview messages and checkpoint artifacts; unknown or lossy codecs fail explicitly. For standalone restoration use `UnmarshalArtifactJSON(data, registry)`; plain JSON decoding cannot restore host-owned labeled artifacts. Export discloses artifact labels only for explicitly allowed `ExtensionTypes`.

## Selected resources

Deferred callbacks now return `DeferredResult`, not a message slice. Migrate `return messages, err` to `return DeferredResult{Messages: messages}, err`; error outcomes must not be used as partial content. No legacy callback overload is retained. Compile freezes returned messages and checks cancellation before consuming them. Structured resource evidence belongs in `DeferredResult.Resources`, matched against declared selections and codecs before admission.

Resource resolution pins an independent `LabelPolicyIdentity` and explicit custom `Codecs` bindings alongside reader/projection identities. `ResourceResolver.Configuration()` and each result's `Configuration` capture the exact estimator/encoding/codec/label intent; configuration refs change even when text does not. Lineage separates host projection from label reconciliation and its upgrade decision. Configured but unused codecs also require identity bindings.

`DescribeResource` pins an opaque host reference/revision, display name, full typed artifact digest and serialized byte length. `ResourceResolver.Resolve` requires an explicit `ResourceReader`, fresh scope, pinned projection, byte bound and estimate budget. It validates the actual body before projection and retains host labels/source ancestry. Declare selections/codecs in `DeferredBlock.Resources`/`ResourceCodec`, then return actual evidence in `DeferredResult.Resources`. Source keeps pre-resolution metadata; manifest and privacy-controlled saved records retain actual resolution dependencies. Resource-bearing accepted replay requires explicit `WithReplayResourceCodecs`; it never invokes a reader or refetches missing bodies. Missing/changed/oversized/unsupported content and cancellation fail without partial output or implicit fallback. Core does not discover resources, parse paths, install capabilities or execute body text. See the [selected resource contract](resource-content.md), [host-owned reference reader](../adapters/resource/memory/reader.go) and runnable [progressive disclosure example](../examples/progressive_disclosure/main.go).

Resolved artifacts pass lifecycle checks and merges during shared preparation, before output admission. Same-ID replacement and source-layer deduplication select one intact prepared revision; append creates separate `ResourceResolution.Merge` evidence with `Prepared` recording the shared derivation. Incoming and merged evidence stay immutable. Main and targets independently apply artifact-local caps using their own estimators, without additional reads. An excluded replacement or merged revision does not silently restore the prior artifact. Append labels require the compile host label policy; media and blob-bound previews fail explicitly rather than being flattened. Choose an output artifact set for persistence and apply its checkpoint policy, not additional ordinary messages from `DerivePersistenceState`. Replay requires saved old/incoming/derived content for every append, including output-excluded derivations, and never re-executes the host label policy or estimator.

## Prefix diagnostics

After final compilation and host freshness/budget/privacy checks, optionally call
`DiagnosePrefix(ctx, result.Payload.FlattenMessages(), recipe, previousManifest)`.
Use the actual adapter-facing order if it differs from the payload order.
`PrefixRecipe` pins renderer, semantic encoding and host admission policy; its
mandatory `Authorize` callback rechecks every hashed message on each invocation.
Caller-owned boundaries terminate after exact message IDs. Variable tail content
does not participate in the prefix digest; the report returns its separate
`TailDigest`. Both identities use owned input captured before callbacks; private
or stale tail content is not encoded if host admission rejects it.

The metadata-only report identifies the first affected boundary and content,
order, policy, codec, compaction or offload changes. Invalid boundaries,
incompatible previous rendering and unsupported required hints fail explicitly.
The adapter checks actual wire prefix bytes and hint support, then may attach a
separate `PrefixWireConfirmation`; semantic equality never means authorization,
cache availability, TTL or savings. No content, role or order is changed for reuse.
See [the contract](prefix-diagnostics.md) and the runnable
[local adapter example](../examples/prefix_diagnostics/main.go).

## Compile-only text replacements

`BlobOffloader.ProjectHistoricalArguments` validates an exact completed message/call and prepares independent Source/Prompt projections with immutable argument storage references. For a confirmed offload, pass `WithHistoricalArgumentProjection(prepared)` in compile options and keep `CompileRequest.History` original. Compile verifies the source/approval digest, complete round and preview-only change before applying it to prompt history before budget. Persistence retains original arguments; accepted replay preserves the projected prompt without fetching bytes, after explicit live availability checks. Pending rounds, other calls/results and host operation/approval metadata are untouched. `ArgumentsBlob` is not execution permission and is stripped from isolated export. Never replace authoritative persisted history or executable arguments with the shortened prompt projection.

Prefer `CurrentTurn` for prompt-only active-turn redaction. To replace text on an existing message, use `WithTextReplacement(TextReplacement{Segment: SegmentHistory, MessageID: "message-id", Text: "safe text"})`. Assign the ID explicitly; role or position never chooses a target. Non-text parts and metadata are preserved, and source/persistence bytes remain unchanged.

Non-history replacements run before hooks/budget; history replacements run after history budget and still undergo final budget validation. Unknown segments or empty IDs return `ErrInvalidTextReplacement`; a missing target (including one removed by compaction/truncation) returns `ErrMissingReplacementTarget`. Multiple replacements run in request order. The positional selector API was removed without aliases or compatibility fallback; migrate multiple selections to explicit IDs or use a host-owned transform hook.

## Segment formatters

Register host-side projection before budgeting (e.g. wrap memory in XML):

```go
engine := contexty.NewEngine(
    contexty.WithSegmentFormatter(contexty.SegmentMemory, func(ctx context.Context, msgs []contexty.Message) ([]contexty.Message, error) {
        // return formatted messages; preserve IDs when updating content in place
        return msgs, ctx.Err()
    }),
)
```

## Final output policy

Configure one `OutputPolicy` to validate or project every final semantic output.
It receives owned typed payload segments after ordinary hooks, formatting, current
turn insertion and post-budget patches. Main, named targets and view targets each
invoke it once; text views render the accepted content. Host code owns any redaction
of text, tool JSON/results, media or extension values. No configured policy implies
no sanitization.

```go
engine := contexty.NewEngine(contexty.WithOutputPolicy(contexty.OutputPolicy{
    Identity: contexty.Descriptor{ID: "host/output-validation", Revision: "1"},
    Project: func(ctx context.Context, input contexty.OutputPolicyInput) (contexty.AbstractPayload, error) {
        // Validate/project every typed channel here using the host's domain rules.
        return input.Payload, ctx.Err()
    },
}))
```

The callback may reject or transform content; it cannot insert, remove or reorder
messages or change tool topology/IDs. Tool argument/result bytes may be transformed.
Exact retention is checked before the boundary, then accepted refs and final budget
are checked afterward. Prompt-only changes preserve Source and configured raw
current-turn persistence. Saved raw capture requires its own host decision.
`ExportSelection.MessageIDs` exports accepted prompt messages;
`ArtifactPayloadRefs` explicitly discloses exact canonical artifact bodies, which
may contain bytes absent from the accepted prompt.
`NewRedactionHook` and `RedactionHook` are removed. Generic transform hooks remain
ordinary pipeline stages; they do not replace this final boundary.

See [output and materialization contracts](output-policy.md).

## Budget pipeline and truncation

When using `WithCompileRecording`, `RecordProfile.Components` must explicitly bind
each configured host callback to its `RecordingComponentKey` and descriptor.
Kinds cover hook/resolver indexes, segment and target formatters, role/label
policies, trace mapping, request identity policy and requested view renderers.
Indexes address the configured slots, including skipped nil slots; nil callbacks
do not need bindings. Missing, extra, duplicate or mis-scoped bindings fail before
execution. Aggregate pipeline/target descriptors no longer substitute for these
callback identities. Binding order is canonical, and changing an individual
descriptor invalidates replay even if the aggregate descriptor is unchanged.
Actual hook/formatter/resolver/role/render edges retain their intrinsic stage name
and the bound component descriptor. See [recording contracts](contracts.md)
for scope and remaining recording-component work.

Recorded budget pipelines with a summarizer require
`WithSummarizerDescriptor(Descriptor{ID: "host/summary", Revision: "pinned"})`.
An explicit `WithCompactionCapture` profile supplies that binding as well; when
both are specified they must agree. Bind each main/target pipeline independently.
Saved budgets and actual summary lineage retain the local identity, and replay
rejects changed bindings even when no summary was needed. Custom truncation
strategies likewise require `WithTruncationDescriptor`; built-in strategies pin
their own identities and effective parameters.
Custom integer estimators and custom tool-cost callbacks require
`WithEstimatorDescriptor`; an `EstimateReporter` already supplies an explicit
binding. Recorded budgets include effective built-in fixed/character parameters.
Pipelines and reporters copy built-in counters at construction, so subsequent
caller mutation cannot change the saved configuration or its execution.
For recording with custom decoder registries, explicitly bind each configured
decoder in `TraceProfile.Codecs` by kind (`CodecExtension`, `CodecLabel`, or
`CodecProvenance`), type ID and descriptor. Message and label registries are
separate scopes. Default provenance codecs have intrinsic identities. Engine and
reporter construction snapshots registries; register needed decoders first.
Manifest trace configuration also pins strict origins, durable identity and
required label types; replay rejects changed configuration.
`CompileConfiguration` pins evaluated compile options by digest, without storing
resolve-variable values or replacement text, plus active deferred block slots,
effective segments and merge policies. Unknown active deferred placement/policy
is rejected before resolver execution in recording mode. Replay checks this
configuration even when different resolver inputs produce identical bytes.

```go
pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
    Budget: contexty.EffectiveInputBudget(4000),
    DropHead:   contexty.DropHeadConfig{MinMessages: 2},
}, &contexty.CharFallbackEstimator{CharsPerToken: 4})

engine := contexty.NewEngine(
    contexty.WithBudgetPipeline(pipe),
)
```

`TokenEstimator` is passed to `NewBudgetPipeline`, not to `Engine`. Estimator failures surface as `ErrTokenCountFailed`.

Budget configuration is explicit: use `EffectiveInputBudget(4000)` for an already
reduced input capacity, or `WindowInputBudget(8000, 3000, 1000)` for a full window
with output and wire reservations. Both examples provide 4000 input tokens.
Reservations are subtracted only in window mode; contradictory, negative or
overflowing reservations return `ErrInvalidBudgetRequest` before transforms run.
`BudgetConfig.TokenLimit` has been removed. Manifest budget records retain the
original request as well as the resolved input limit.

For explicit estimate evidence, create an `EstimateReporter` with your pinned
model/estimator/method/encoding descriptors and capabilities, then pass it as the
estimator to `NewBudgetPipeline`. Unsupported kinds fail strictly unless you
configure an explicit positive fallback. `CompileResult.Estimates` contains final
reports for main and named targets, including post-formatter costs. Recorded
manifests retain these reports; `manifest.EstimateFor(kind, name)` returns a
defensive report bound to the manifest digest. Replay restores saved reports
without calling the estimator. Wire counts and actual usage remain separate
observations; they do not overwrite semantic estimates.

`CompactionRecord` is a separate host-owned summary lifecycle, not acceptance of
an entire compilation. `NewCompactionRecord` pins covered revisions, the actual
summarize edge, output, model/codec/policy identities, saved result and estimate.
Proposals may omit bytes or estimates; `Accept(decisionRef)` requires both and a
within-budget result. `Supersede(decisionRef)` retires an accepted record.
`ReplayCompaction` requires the current accepted digest, profile, coverage and
budget, and returns saved summary content without fetching or summarizing.
Storage/privacy decisions remain with the host. Add `WithCompactionCapture(profile)`
to a reporter-backed BudgetPipeline to return actual summary proposals in
`CompileResult.Compactions`. This requires trace, recording and content capture;
summarizer/reporter/privacy descriptors must match. Records capture projected
labels and the actual sub-budget. A later privacy denial leaves a partial proposal
without bytes, not an acceptance bypass. Accept/store records explicitly in host
code. Add `WithRollingSummary(RollingSummaryPolicy{Descriptor: policyID,
RecentMessages: 2})` to compress only the aged prefix while preserving at least
two recent messages. Pin the policy descriptor; when capturing compactions it
must match `CompactionProfile.Policy`. The boundary expands backward for a tool
round or an earlier pending call. Under-budget input is not summarized; oversized
tail/summary returns a typed error, without silently deleting either. Whole-block
compression remains available without this option.

Manifest budgets pin the actual recipe and replay rejects changes to it. Resume
with an explicitly accepted summary, preserved tail and prior lineage; the next
proposal covers that summary revision plus newly aged messages and retains old
ancestry. The core does not load or accept records automatically. Give each new
summary a fresh identity, not an ID of any covered/preserved message. Main and
named targets can use independent recent-tail counts and budgets.

Persistence now keeps surviving/introduced messages in compiled chronological
order: a prefix summary precedes its tail, rather than being appended after it.
Compile-only formatting still restores original bytes at the retained position;
current-turn raw/prompt-safe persistence follows its explicit policy. Consumers
should persist this ordered projection, not manually append summaries on resume.

`InspectToolRoundStates(messages, declarations)` distinguishes locally complete
rounds from pending ones and explicitly host-declared interruptions. It validates
call/result identities without inferring remote outcomes from payload metadata.
`RepairInterruptedToolRounds(ctx, messages, declarations, policy, codec)` is an
explicit projection-only operation: pin behavior/encoding descriptors and a host
decision reference for each interrupted assistant. Only missing results receive
deterministic synthetic markers stating that the external outcome is unknown.
`Messages`, `Repairs` and `Lineage` are returned together; markers remain in the
serialized payloads. Pending rounds and source history are not changed. Host
extensions require round-tripping codecs, and assistant metadata is preserved
without assigning higher trust. Supply the returned lineage to CompileRequest
when compiling a repaired projection; recording/replay retain synthetic evidence.
This operation does not guarantee provider wire validity.

For host extensions, register their decoders in the reporter's serializer and
declare `EstimateProfile.Extensions[typeID]` with pinned codec/policy descriptors.
Set `MetadataOnly` only when that value does not participate in the request wire;
it remains part of request identity, but is removed from counter inputs. Content
extensions use `EstimateExtension` capabilities. Missing classification or unknown
cost fails strictly, or adds one explicit positive fallback per value. A generic
extension capability alone does not classify an unknown type. Reports preserve
ordered `ExtensionTypes` and participation coverage. Missing decoders return
`ErrMissingEstimateExtensionCodec`; fallback never removes actual output values.

Tool-call turns are truncated atomically by default (`KeepTurnAtomicity` defaults
to `true`). `BudgetPipeline` validates round layout before callbacks and protects
the chronological suffix starting at the earliest pending round. Only preceding
context can be compressed or evicted. An oversized protected suffix returns
`ErrPendingExceedsBudget`; combined cost is verified without assuming additivity.
Setting `KeepTurnAtomicity` to `false` still enables standalone index truncation,
but a pipeline now rejects split rounds with `ErrInvalidToolRound` instead of
silently deleting orphan nodes. `EvictionReasonOrphanRepair` and implicit orphan
repair are removed. Handle typed errors or keep turn atomicity enabled; do not
depend on automatic deletion to sanitize malformed history.

When using a custom `Summarizer`, do not reuse a truncated message ID for the summary. In durable compile flows, leave the summary ID empty and let `IdentityPolicy` assign it.

**Canonical tool-turn layout** for atomic truncation: `RoleAssistant` with `ToolCallPart`(s), then `RoleTool` message(s) with matching `ToolResultPart.ToolCallID`. Use `ToolRoundFromMessages` / `ToolRound.Validate` for first-class validation. `ToolTurnUsesCanonicalLayout` remains a lightweight layout predicate.

## Context Artifacts and Deltas

Artifact-bearing compilation requires `WithArtifactMaterialization`. There is no
default provider role, including no implicit system role for retrieved text.
The host returns an explicit role and typed parts; sources and extensions remain
bound to the artifact. Message-only compilation needs no artifact policy.

```go
materialization := contexty.ArtifactMaterializationPolicy{
    Identity: contexty.Descriptor{ID: "host/retrieval-data", Revision: "1"},
    Materialize: func(ctx context.Context, artifact contexty.ContextArtifact) (contexty.ArtifactRepresentation, error) {
        parts, err := contexty.ArtifactContentParts(artifact)
        if err != nil { return contexty.ArtifactRepresentation{}, err }
        return contexty.ArtifactRepresentation{Role: contexty.RoleUser, Parts: parts}, ctx.Err()
    },
}
engine := contexty.NewEngine(contexty.WithArtifactMaterialization(materialization))
```

This is an explicit host representation decision, not a trust classification or
tool authorization. A `ResourceResolver.Materialization` must use the same pinned
policy identity as the engine; resource append, blob previews and artifact counts
use that agreed typed representation.


Use artifacts for retrieval and memory lifecycle instead of host-side run metadata:

```go
owner := contexty.SourceRef{Namespace: "tenant", Kind: "workspace", ID: "workspace-1"}
doc := contexty.NewRetrievalDocument(
    "doc-1",
    contexty.TextPayload("retrieved context"),
).ContextArtifact.WithTurn("turn-1").WithOwner(owner)
doc = doc.WithBudget(contexty.ArtifactBudgetPolicy{TokenLimit: 2000})
memory := contexty.NewMemoryBlock("memory-1", contexty.TextPayload("durable context")).ContextArtifact
memory = memory.WithPersistence(contexty.ArtifactPersistenceStore)

result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
    TurnID:    "turn-1",
    Artifacts: []contexty.ContextArtifact{doc, memory},
    History:   historyMsgs,
})
_ = result
_ = err
```

Artifact-local limits use each output's budget pipeline estimator on its materialized
message, or `CharTokenEstimator` when no pipeline is configured. A nil `Budget`
means no local limit. An explicit `TokenLimit: 0` means zero capacity; callers
previously using zero as unlimited must omit the policy instead. Negative active
limits and estimator failures fail compilation. Manifest exclusions preserve the
actual selection decision without counting again. Final output budgeting still
applies independently; local admission is not a guarantee of final inclusion.
`CompileResult.ArtifactEstimates` records each active artifact-local admission:
input/message refs, limit, tokens, quality, and the full report when configured.
Manifest `ArtifactBudgets` pins requests; `ArtifactEstimates` preserves evidence
even for excluded artifacts. Missing/duplicate evidence, mismatched reports and
contradictory budget exclusions are rejected. Exact replay restores this evidence
without counting again. Use `ReplayExpectationFor` to pin artifact budgets too.
Inactive and unbounded artifacts have no local estimate; legacy integer admission
is explicitly `estimated`, not proof of an exact provider cost.
Binary/media artifacts materialize as `MediaPart{MIMEType, Data}`; an optional
text preview is a separate part and does not replace the body. MIME is required
for binary content. Message cloning/codecs preserve the bytes. Character
estimators return `ErrUnknownEstimateCost`; configure an `EstimateReporter` with
a media-capable host estimator or an explicit positive fallback (quality remains
`unknown`). Text-only built-in views return `ErrUnsupportedMediaRendering`;
use typed message projections or a host formatter that supports your media.
Migration: handle `MediaPart` explicitly instead of assuming every artifact is a
single `TextPart`. Typed target `Text` is diagnostic, not a media transport.

Turn-bound retrieval artifacts are visible only when `CompileRequest.TurnID` matches `BoundTurnID`. Ownership is `OwnerRef`, a typed `SourceRef` owned by the host application. Ephemeral artifacts and `ArtifactPersistenceSkip` are omitted from checkpoints; `ArtifactPersistenceStore` forces checkpoint persistence. For artifacts, `PolicyReplaceByOrigin` replaces stale artifacts with the same kind/type plus owner/source refs even when the new artifact uses a different `ID`.

Use typed artifact codecs when the host needs structured values to round-trip without manually packing domain data into a raw payload container:

```go
type Fact struct {
    Title string `json:"title"`
    Body  string `json:"body"`
}

desc := contexty.ArtifactCodecDescriptor[Fact]{
    TypeID:      "example.fact",
    Kind:        contexty.ArtifactKindRetrievalDocument,
    Lifecycle:   contexty.ArtifactLifecyclePersistent,
    SourceRefs:  []contexty.SourceRef{{Namespace: "kb", Kind: "document", ID: "doc-1"}},
    MergePolicy: contexty.PolicyReplaceByOrigin,
    Budget:      &contexty.ArtifactBudgetPolicy{TokenLimit: 2000},
    Persistence: contexty.ArtifactPersistenceStore,
    Render:      func(v Fact) string { return v.Title + ": " + v.Body },
}
artifact, _ := contexty.NewTypedArtifact("fact-1", desc, Fact{
    Title: "Boundary",
    Body:  "Artifacts carry lifecycle and typed source data.",
})
decoded, _ := contexty.DecodeTypedArtifact[Fact](artifact, desc)
_ = decoded
```

Use deltas for immutable state transitions:

```go
state, err := contexty.ApplyDelta(contexty.EmptyState(), contexty.ConversationDelta{
    Operation: contexty.DeltaAppendMessages,
    Segment:   contexty.SegmentHistory,
    Messages:  []contexty.Message{contexty.TextMessage(contexty.RoleUser, "hello")},
})
_ = state
_ = err

err = store.CommitState(ctx, "chat-1", expectedVersion, contexty.ConversationDelta{
    Operation: contexty.DeltaReplaceSegment,
    Segment:   contexty.SegmentHistory,
    Messages:  state.Segment(contexty.SegmentHistory),
})
```

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
    contexty.WithBudgetPipeline(pipe),
)

// Or attach observer only to budget events:
pipe := contexty.NewBudgetPipeline(cfg, estimator, contexty.WithBudgetObserver(metricsObserver{}))
```

| Callback              | When                                                                                                           |
| --------------------- | -------------------------------------------------------------------------------------------------------------- |
| `OnTokensEstimated`   | After initial token estimate for a budget block (`blockID` = segment name)                                     |
| `OnNodeEvicted`       | Strategy truncation or block drop (`nodeID` = `Message.ID` or fallback hash); events describe attempted work, not an accepted compilation |
| `OnContextSummarized` | After summarizer runs (`compressionRatio` = tokens before / tokens after)                                      |
| `OnPipelineCompiled`  | Successful `Compile()` / `CompileSnapshot()` with total payload cost and duration; failures skip callback only |

`CompileResult.Transformations` is updated even when no `Observer` is configured. Use `contexty.NoopObserver` when telemetry is disabled.

**Observer semantics:**

- `WithObserver` on `Engine` receives `OnPipelineCompiled` only (not budget events).
- `WithBudgetObserver` on `BudgetPipeline` receives budget events. If only `WithObserver` is set, budget callbacks are not emitted.
- The same `Observer` instance may be passed to both `WithObserver` and `WithBudgetObserver`.
- Observer is passive: telemetry estimate failures do not fail `Compile()`.

## Storage adapters (Postgres / Redis)

```go
import postgresstore "github.com/skosovsky/contexty/adapters/store/postgres"

store := postgresstore.New(pool)
state, err := store.LoadState(ctx, conversationID)
if err != nil { return err }
err = store.CommitState(ctx, conversationID, state.Version(), contexty.ConversationDelta{
    Operation: contexty.DeltaAppendMessages,
    Segment:   contexty.SegmentHistory,
    Messages:  []contexty.Message{msg},
})
```

Schema (Postgres):

```sql
CREATE TABLE contexty_conversations (
    thread_id VARCHAR(255) PRIMARY KEY,
    version BIGINT NOT NULL DEFAULT 0,
    segments JSONB NOT NULL DEFAULT '{}'
);
```

### Adapter contract tests

Postgres and Redis adapters share a minimum integration contract (testcontainers):

| Case                                        | Expected behavior                                                    |
| ------------------------------------------- | -------------------------------------------------------------------- |
| Empty `LoadState`                           | `Version()==0`, empty segments                                       |
| Delta append / replace / OCC                | Monotonic version, stale write → `ErrConversationVersionConflict`    |
| `ClearState` missing thread, expected zero  | Empty tombstone with revision 1                                     |
| `ClearState` stale version                  | `ErrConversationVersionConflict`                                     |
| `ClearState` existing thread                | Revision advances, payload erased; reload token before recreate     |
| Semantic round-trip                         | `ToolCallPart`, `ToolResultPart`, `SourceRefs`, `UserProvenance`     |
| Expanded round-trip                         | `ImagePart`, `SystemProvenance`, `Origin`, `LLMCache`, `SourceRefs`  |
| Redis: version without payload              | `ErrUnavailable` (corrupt state)                                     |
| Postgres: concurrent first insert           | One success, one `ErrConversationVersionConflict`                    |

Clear and payload expiry must never reuse a previous OCC revision. Postgres
retains an empty row; memory retains an empty state; Redis retains the revision
key and a payload-free tombstone. These markers contain no deleted messages or
artifacts. `ClearState` consumes a revision even for a previously absent ID.
`WithTTL` expires only Redis payload, atomically advancing revision when expiry
is observed by a read or CAS. Revision markers must not be expired or deleted
independently while the same conversation ID can be reused. Host cleanup that
removes the marker must also retire the ID permanently. Integer exhaustion
returns `ErrConversationVersionExhausted`, never wraps to an earlier token.

Migration: after clear/expiry call `LoadState` and use its returned version for
recreation; do not assume `expectedVersion=0`. Stale writes and stale clears
conflict. Old Redis data whose revision key was configured to expire must be
migrated explicitly before reusing its IDs. There is no transparent compatibility
path for reset revisions.

Compile validates final main and target output budgets after patches/formatters.
An expansion beyond the configured limit returns `ErrBudgetExceeded`; protected
current-turn/pending messages are never silently dropped. Compile-only redaction
continues to use the original content for persistence when selected by policy.

Run adapter suites locally when Docker is available (also covered by CI `integration` job):

```bash
go test -v ./adapters/store/postgres/...
go test -v ./adapters/store/redis/...
```

Adapters must use `contexty.ConversationCodec`, `contexty.JSONSerializer`, or registry-aware `contexty.MessageCodec` helpers — no custom part parsing in storage layers.

## Storage resilience

| Error                            | Meaning                                        |
| -------------------------------- | ---------------------------------------------- |
| `ErrConversationVersionConflict` | OCC mismatch — reload and merge                |
| `ErrUnavailable`                 | Transient storage failure — retry with backoff |

Wrap `ConversationStateStore` with retry logic on `ErrUnavailable`. Respect `context.Context` deadlines in storage calls.

## Wire JSON contract

### Host-owned opaque state

`OpaqueState` is a reserved typed extension envelope for external model state.
The host supplies a typed payload, pinned payload codec, ID, `Placement.AfterPart`
and `OpaqueBinding` with a receiving profile and exact content refs. An optional
ordered prefix binds the context beginning through `Boundary`, including insertion
and order; that boundary must precede the owner. Changes after it remain outside
the prefix scope. Register each independent host payload
with `ExtensionRegistry.RegisterOpaquePayload` and configure
`WithOpaqueStatePolicy`. Compilation without opaque envelopes requires no state policy.

Dependency changes fail with `ErrOpaqueStateInvalidated`. The host may explicitly
choose `OpaqueDropInvalid`; drops are recorded, without repairing payload bytes.
Final output projection and branch selection also pass this check. Plain-text
rendering excludes opaque bytes. Export requires separate `OpaqueStateIDs`, a
matching `OpaqueProfile` and explicitly selected dependency messages; selecting a
state never grants hidden access to raw source content. Cost comes from the host
adapter estimator or remains unknown under `EstimateReporter`; payload byte length
does not establish token cost.

Conversation stores and codecs require the same `ConversationCodec.OpaqueProfile`
and registered payload codecs. `DerivePersistenceState(codec, profile)` and
`ProjectCheckpoint(state, codec, profile)` validate the entire selected state;
handle their errors before committing. Accepted replay restores recorded bytes
and bindings through registered codecs without rerunning selection, materialization or
output policies, and pins policy/profile/encoding identity.
The executable [offline host recipe](../examples/opaque_state/main.go) uses distinct
signature and opaque compaction fixtures. A local `CompactionRecord` represents a
summary transformation; it is not external opaque compaction state. Core neither
interprets signatures nor promises portability between models.

Messages and segments serialize as JSON with explicit discriminators:

- Content parts: `kind` ∈ `text`, `image`, `media`, `tool_call`, `tool_result`
- Tool payloads: `text`, `data`, explicit `binary_hex`, MIME type, error, progress, control
- Provenance: `type_id` resolved via `ProvenanceRegistry` (unknown types error at decode)
- Extensions: `type_id` resolved via `ExtensionRegistry` (unknown types error at decode)
- Message origin: `origin` object with `template_id`, `layer_id` (optional)
- LLM cache hint: `llm_cache` object (provider-specific fields)
- Source refs: `source_refs` with namespace, kind, ID, checkpoint ID, URI

## Architecture guardrails

AST tests in `architecture_test.go` (run via `make test-acceptance`):

- `TestArchitecture_NoStringHeuristicsForSemantics` — no string-prefix heuristics in semantic core
- `TestArchitecture_NoForbiddenExternalImports` — stdlib + `github.com/skosovsky/contexty/*` only in core
- `TestArchitecture_NoJSONMetadataInTextParts` — no JSON tunneling in `TextPart`
- `TestArchitecture_NoContractMetadataInAttributes` — no naked attributes escape hatch
- `TestArchitecture_NoBase64InCore`
- `TestArchitecture_NoRemovedOverlayAPIInCore` — removed overlay and prompt-origin aliases must not reappear
- `TestArchitecture_FormattersUseExplicitContext` — formatter context flows through explicit parameters, not globals

## Development

```bash
make test              # all modules, race
make test-acceptance   # contract + atomicity acceptance subset
make lint
make bench-guardrails  # allocation guardrails (CI gate)
make validate          # lint + test-acceptance + bench-guardrails + full test
```

Hot-path benchmarks live in `bench_test.go`. Full acceptance gate: `make validate` plus adapter integration tests when Docker is available.

## Policy

Do **not** encode transport metadata in message text or use string heuristics (`strings.HasPrefix`, `strings.Contains`) on message history for business logic. Use typed `ContentPart`, `Actor`, `SourceRef`, `Extension`, `Provenance`, and registries.

## Architecture Notes

The shipped contract is documented in this README, the package docs and [contract reference](contracts.md).

## External context strategies

The [context strategy cookbook](context-strategies.md) combines host archives,
checkpoint resume, rolling summaries, explicit offload, chunk selection and typed
memory replacement using existing contracts. Original transcripts, search,
authorization, durable blob/claim storage and model quality remain host concerns.
Working projections and summaries are not archives; exact replay capture may
retain original private bytes even when offload reduces the issued prompt.

Run `go run ./examples/context_evaluation` for a machine-readable offline comparison
of sliding window, rolling summary, offload and host-selected retrieval. Fixed
fixtures check mechanical guarantees with the same budget and estimator/evaluator
profile. Timing is diagnostic; real-model quality, provider usage and money are
not measured by this offline run. The optional typed live runner requires an
explicit host invocation and is excluded from ordinary validation.

## Required context and early compaction

`BudgetConfig.Retention` protects selected message IDs, exact content refs or
roles through summarization and eviction, including all participants of a
selected tool round. Insufficient mandatory capacity is an explicit error.
`CompactionPolicy` optionally starts compression before hard overflow and sets
a soft target as percentages of the effective input capacity.

A host `Summarizer` receives `SummaryRequest` with owned `Messages`, actual
remaining `MaxTokens`, desired `TargetTokens` and the policy `Purpose`. It runs
at most once. A summary missing the soft target may succeed within the hard
limit; `BudgetResult.Decision.TargetReached` reports this explicitly.
`CompileResult.BudgetDecisions` exposes stage evidence for main and target
outputs. Final output estimates remain separate and authoritative after later
patches and formatting.

See [the complete contract](retention-budget.md) and the executable
[tool-heavy budgeting example](../examples/context_budget/main.go).

## Atomic checkpoint storage

`CommitState` publishes an ordered nonempty batch under one loaded revision.
Every supplied store applies `ProjectCheckpoint` explicitly. Conversation codecs
are lossless and retain transient artifacts; they use `contexty/conversation/1`
independently of OCC revisions. Redis checkpoint keys use a new encoded namespace
with conversation-specific Cluster hash tags. See [checkpoint contract](checkpoint-store.md)
and [host reconciliation example](../examples/resilient_store) for unknown network outcomes.

`ExportSelection.ArtifactPayloadRefs` explicitly approves exact canonical artifact
revisions from `ArtifactContentRef`, independently of `MessageIDs` selecting accepted
prompt messages. This replaces export selection `ArtifactIDs`; projection
`ArtifactIDs` remains participation evidence. Stale, malformed or duplicate payload
refs fail export. A payload ref grants disclosure of the original canonical typed
body, not the output-policy representation; select only messages when handing off
the accepted prompt. Neither sanitization nor metadata allowlisting rewrites that
canonical body.

Release platform, validation gates and failure recovery: [release tooling](release.md).

### Closed roles and native mapping

Use `RoleSystem`, `RoleDeveloper`, `RoleUser`, `RoleAssistant`, or `RoleTool`.
`Role.Validate()` returns `ErrInvalidRole` for empty/unknown roles. Inputs and
transformed outputs are checked even without OutputPolicy. Developer remains a
separate role. Role does not grant trust or instruction priority; host-defined
retention can include both instruction roles through `Retention.Roles`.

See [SupportedMapping](supported-mapping.md) and the independent
[offline consumer](../integration/chat/README.md) for exact JSON, native media,
opaque state, final execution reports and terminal history CAS ownership.
