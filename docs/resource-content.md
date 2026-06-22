# Selected resource contract

The host discovers resources and authorizes access. Core receives a selected
opaque `ResourceDescriptor`: reference ID/revision, display name, exact typed
artifact content reference and serialized byte length. Display names never act
as identity or permission. Descriptor and body are separate values.

`ResourceReader.ReadResource` receives a fresh scope, selected descriptor and
explicit maximum bytes. It returns a body with the actual storage reference and
a typed artifact. The adapter must enforce authorization and bounded loading;
core additionally checks revision, complete artifact digest and length before
projecting. Missing body is an error, never prompt content or an implicit fallback.
Only typed retrieval/memory artifacts are supported; host file formats and paths
are not interpreted by core.

`ResourceResolver.Resolve` composes pinned reader/projection ports, strict host
label transport and an explicit estimate reporter/budget. Projection returns a
typed artifact with an explicit identity; host owns lifecycle/persistence. Labels
and source ancestry follow LabelProjection, including explicit upgrade decisions.
Source artifact, projected artifact, materialized message, report and lineage are
independent immutable values. No projection/estimator runs on changed content.
No body grants permissions or installs capabilities, and no executable actions
are inferred from text. Cancellation dominates callback returns and stops later
stages; failure returns no partial resolved body/projection.

`DescribeResource` defines digest/length using the artifact's full typed JSON
contract, including host metadata. Digests use the same canonical JSON identity
as ArtifactContentRef; byte length is the serialized typed artifact length, not
provider tokens or the size of an external file. Unknown/lossy host label codecs
fail explicitly during resolution. Budget is checked against actual materialized
content, without truncation or implicit fallback.

Standalone resolution alone does not complete C4. Native deferred, manifest,
privacy-controlled checkpoint capture, exact accepted replay and the reference
adapter/example below provide the selected-resource workflow. Native replacement,
deduplication and append update the existing artifact set using the contracts below.

`DeferredBlock.Resolve` now returns `(DeferredResult, error)` rather than
`([]Message, error)`. Explicit message content lives in `DeferredResult.Messages`.
The old callback form is removed, without adapters/aliases or compatibility
overloads. Compile defensively owns this content and rechecks cancellation before
using it. Resource evidence is explicit in `DeferredResult.Resources`, independently
of `Messages`. Declare ordered selections in `DeferredBlock.Resources` and their
host codecs in `DeferredBlock.ResourceCodec` before compile. A selection pins
resolution ID, descriptor, complete configuration, budget and maximum byte bound;
it contains no reader handle or access scope. Missing/extra/reordered or changed
resource evidence fails instead of silently becoming arbitrary prompt content.
Message-only deferred results are not claimed to resolve resources.

Selections and codec registries freeze when installed in the engine. All
declarations validate before the first deferred callback, including duplicate
resolution IDs across blocks. `CompileResult.Source.DeferredResources` retains
selected metadata before resolution, with no raw body, callback or read scope.
Actual resource lineage is imported before deferred/merge/target transforms;
targets use the same resolved message without invoking readers again. Manifest
compile configuration includes selections and owns their nested containers.

Resolved artifacts pass the same lifecycle and local-budget admission as explicit
artifacts, using the main estimator. Inactive turn-bound or over-budget projections
are excluded, with actual cost and exclusion reason in manifest evidence. Only
admitted artifacts/messages reach main and targets. Default replacement and
`PolicyReplaceByOrigin` may replace an existing artifact with the same ID;
ephemeral artifacts follow the existing replacement semantics too. Resolution evidence and
admission state are owned by one compile operation; another compile starts fresh.
`CompileResult.Artifacts` contains admitted projections; normal artifact checkpoint
policy controls retention. Artifact-generated messages are not also saved through
`DerivePersistenceProjection`, which prevents an ephemeral projection becoming a
persistent ordinary message. Source retains only pre-resolution input metadata.

For distinct artifact IDs, an admitted `PolicyReplaceByOrigin` resource applies
the normal typed-origin replacement against both existing artifacts and earlier
resolutions. Removed artifact messages disappear from every shared segment before
later transforms/targets. Replaced projections in the same deferred callback are
filtered from its collected messages too. Unrelated origins remain unchanged;
inactive/over-budget replacements cannot evict active artifacts. Selected source
and actual resolution records remain immutable evidence, with `artifact_merge`
exclusions for replaced artifacts. Exact replay restores the final admitted set
without resolving again. Same-ID replacement retains only the last admitted
message, including when the two artifact revisions are identical. Different
revisions sharing an ID remain distinct manifest inputs and exclusions use full
content references, not bare IDs. Ordinary deferred messages may not reuse a
resource materialization ID in the same result: that conflict fails explicitly
instead of silently discarding host content. Same-ID deduplication follows the
contract below. Same-ID append produces a separate derived artifact as described
below, without rewriting the original resolution evidence.

### Same-ID deduplication contract

An admitted non-ephemeral `PolicyDeduplicateByLayer` projection follows the
existing artifact source-layer contract: when its non-empty source reference
intersects the active artifact with the same ID, retain that existing artifact
unchanged and emit no incoming materialization. Otherwise replace it with the
incoming artifact. Ephemeral lifecycle retains its replacement precedence.
Resource resolution evidence and its approved replay dependencies remain present
even when deduplication excludes the incoming projection. Inactive or locally
over-budget incoming projections cannot replace the active artifact. Main and
targets must agree with the final admitted artifact set; no target reads again.
Deduplication selects one intact artifact; it does not merge labels, broaden trust
or treat an empty source identity as a common origin. A repeated identical typed
artifact revision has one manifest budget request/estimate, not duplicate entries.

### Same-ID append contract

Append creates a separate derived artifact, never rewrites the incoming resolved
record. It preserves the incoming lifecycle/persistence/local-budget contract and
unions both source sets. Host labels are reconciled through the compile label
policy; missing policy, conflicts and unauthorized upgrades fail atomically.
Lineage binds both complete typed artifact inputs to the derived artifact, then
binds that artifact to its actual materialized message. Byte content is joined
using the existing newline text-append semantics. Media and blob-bound previews
are unsupported for append and fail explicitly instead of flattening or reusing
an invalid immutable-object binding.

Incoming admission precedes append. The resulting derived artifact undergoes a
second local-budget admission using the main estimator. If excluded, the prior
active artifact remains unchanged; final main/target budgets still apply. Metadata
and actual dependencies for the derivation are separate from the original
resolution. Host-approved capture includes both inputs, the derived artifact and
message, including an excluded derivation. Accepted replay verifies these saved
dependencies without executing reader, label policy, projector or estimator.

`ResourceResolution.Merge` stores the two full input refs, derived artifact and
materialized message refs, initial admission decision and the two derivation
edges. Original resolution refs/reports/four edges stay unchanged. Derived local
budget evidence belongs to the actual derived artifact, not the incoming report.
Derived refs are not declared as external manifest inputs. A later merge may
replace an initially admitted derivation; its admission flag is not a promise
that it remains in the final prompt. Replay restores the final admitted set from
saved bytes and validates the text/source/lifecycle/persistence/budget binding
of each derivation without running the host label policy again.

Contract tests cover sequential same/separate-callback append with strict origins,
independent-codec labeled accepted replay, missing old/incoming/derived dependencies,
decoder and estimator cancellation, main/target hard budgets, checkpoint lifecycle
and independently owned typed outputs, metadata and saved proposals. Replay also
binds each derived local-budget request to the decoded typed artifact's actual
budget: missing, invented or changed requests cannot replace that contract.

Artifact checkpoints alone do not establish an accepted resolved record. Explicit
host capture/acceptance and the replay dependencies below remain mandatory.

## Compile capture and exact replay

`CompileManifest.Resources` contains metadata-only `ResourceResolution` records:
the selected descriptor/configuration/bounds, actual projected/final artifact refs,
actual estimate and the four resolution edges. Records must match the ordered
declarations, profile/budget and every main/target graph. Manifest self-digests and
clones include these records; they contain no body bytes or access scope.

With explicit compile content capture, the host sees original body, pre-label
projection and final artifact as separate `SavedArtifact` candidates at
`resource-read`, `resource-project` and `resource-labels`; the materialized message
is a `SavedMessage` candidate at `resource-materialize`. Denial for a content
revision dominates later capture purposes. These actual saved bytes are mandatory
resolution dependencies, including for a projection excluded from prompt admission.
If required bytes are denied/deleted, the proposal cannot be accepted; hashes do
not replace the missing body. A prompt-safe compile may still succeed.

Exact accepted `Replay` first checks current caller intent, then reconstructs and
validates every resource's source/projection/final/message bindings, estimate and
lineage from saved typed content before issuing any output. It never calls a
reader, projector, label policy or estimator, and never refetches missing content.
Missing dependencies, incorrect content kinds, missing/lossy host codecs or changed
current configuration fail with a zero result. Replay exposes final admitted
artifacts/outputs, not an executable or raw Source. The host owns saved-record
authorization, retention/deletion and the explicit acceptance decision.

Resource-bearing replay requires `WithReplayResourceCodecs`, keyed by the declared
resolution ID. The map and registry containers freeze when the option is created.
It must contain exactly the resolutions in the accepted manifest: missing/extra
IDs, duplicate capability options and missing/surplus codec topology fail before
any decoder callback. Blob-free/resource-free replay needs no additional capability.

```go
option := contexty.WithReplayResourceCodecs(map[string]contexty.ResourceCodec{
    resolutionID: {Messages: resourceMessages, Labels: resourceLabels},
})
result, err := contexty.Replay(ctx, accepted, currentIntent, outputMessages, option)
```

`resourceMessages`, `resourceLabels` and `outputMessages` may have independent
configured topologies. Resource artifacts decode only through the selected label
registry; the materialized resource message uses its own message registry. Returned
outputs/artifacts still require their current output codecs. Unused configured
decoders participate in topology identity but never execute just because they are
registered. Resource-local dependency messages are not decoded using the root
output codec; each returned output is independently checked before publication.
Resource decoder cancellation stops later callbacks and dominates callback errors.

Migration: supplying only the root message codec is no longer sufficient for
resource-bearing replay. Declare the exact resource codec map explicitly. No
registry substitution, reader invocation or compatibility fallback is provided.

## Reference host reader and progressive disclosure

`adapters/resource/memory.New` accepts host-owned typed body snapshots, an explicit
maximum serialized body size and a mandatory authorization callback. It is an
immutable in-process reference adapter, not durable storage or an ACL system.
Discovery, catalog construction, resource selection and permission interpretation
remain with the application. No file paths or instruction formats are parsed.

Each read checks fresh scope, declared/actual byte bounds and authorization before
body/existence disclosure, then selects by opaque reference ID and verifies exact
revision/content/length. Names do not affect lookup. Missing, denied, changed,
oversized and canceled reads return no body. Constructor and returned containers
are owned independently; concurrent reads cannot mutate stored snapshots. Publish
a new host reader snapshot for changed storage content, rather than updating a
revision in place. The host pins the reader identity for that interpretation.

Run `go run ./examples/progressive_disclosure`. The application discovers two
descriptors with the same name, explicitly selects one, authorizes and reads only
that body, and passes frozen evidence through native compile and a target. Source
contains the descriptor/configuration, not body messages. The projected ephemeral
artifact is not duplicated as ordinary persistent history. The example neither
interprets resource text as instructions nor installs executable capabilities.

## Interpretation identity

`ResourceResolver.Configuration` pins reader, projection, label policy, complete
estimate profile and configured codec topology. `LabelPolicyIdentity` is required
for a host label callback, independent of `ProjectionIdentity`. With no callback,
only the intrinsic source-reference transport identity applies; callers cannot
impersonate that identity. Required labels without a policy fail before reading.

Provide `Codecs` bindings for every configured custom label/extension decoder,
including unused decoders. Default provenance codecs have intrinsic bindings;
unknown functions/types never receive inferred identities. Missing, extra,
duplicate or malformed bindings fail before host I/O. Registry/type/binding
containers freeze before the reader runs. `ResourceConfiguration.Clone` owns
all containers; `Ref` hashes the complete validated configuration. Configuration
identity changes when the label policy, codec, encoding, model or estimate intent
changes, even if the text output remains identical.

Lineage separates `resource-read`, `resource-project`, `resource-labels` and
`resource-materialize`. The projection edge describes the host's actual initial
artifact. The labels edge references both original body and projected artifact,
uses the actual label policy identity and records its upgrade decision. The
materialization edge links the final artifact to the actual counted message.
Source labels remain unchanged. Returned `ResolvedResource.Configuration` is
the frozen interpretation used by that invocation, not reconstructed caller state.

## Local evidence restoration

`ResolvedResource.ID` identifies the actual resolution invocation. `Source`
contains the original body and actual reference; `Projected` is the independently
owned artifact before label transport; `Artifact` and `Message` are the final
materialization. `Validate(ctx, ResourceCodec)` binds all of these to the selected
descriptor, frozen configuration, estimate profile/message refs and four exact
lineage edges. It rejects changed revisions, bodies, projections, roles, costs,
configuration or lineage. Intrinsic label transport cannot claim an upgrade
decision. Validation proves internal consistency, not authorization or authenticity.

`Clone` owns all containers. `EncodeResolvedResource` and `DecodeResolvedResource`
round-trip typed evidence with explicit message and label registries, exact codec
topology, a canonical envelope digest and lossless label checks. Missing required
content, unknown codecs, inconsistent bindings, unknown envelope fields and
trailing JSON fail with no partial result. Cancellation stops subsequent extension
decoder callbacks, including round-trip checks, and dominates callback errors.
Readers, projectors, label policies and estimators never run during restoration.

This encoding deliberately includes the **original private body**. It is neither
a safe export nor an accepted checkpoint: the host must explicitly approve raw
retention before calling it. Fresh read scope is not serialized. A self-digest is
not a signature, a host permission or evidence that current interpretation intent
still matches. Native compile capture must apply the host privacy/deletion policy;
exact accepted replay must independently check the caller's current configuration
and required saved dependencies, as described above. The standalone evidence codec
does not perform those native capture/acceptance/replay steps itself.
