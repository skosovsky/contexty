# Independent context outputs

Compile has one shared preparation boundary: normalized identities, prompt-safe
current turn, historical argument projections, and explicitly declared deferred
resolution/merge. Deferred authorization and projection remain host-owned.
Preparation is trusted local work, not transport to a consumer. Artifact materialization requires an explicit host role/typed-parts policy.
Output hooks, role/segment formatters, summarizers and budget admission run after this boundary.
Compile options and shared resolvers run once, without hidden reads for targets.

Main uses all prepared segments, visible artifacts and prompt-safe active input.
Every named output branches independently from the same owned prepared candidates
and preparation lineage; narrowing main cannot remove another output's input.

## Explicit composition

`CompileTarget.Segments` selects ordinary messages from the listed segments.
Artifacts materialized in those segments remain separate candidates: choose exact
`ArtifactRefs` from `ArtifactContentRef`, or use `IncludeArtifacts` for all prepared
visible artifacts. These settings are mutually exclusive. `IncludeCurrentTurn`
includes prompt-safe active input and protected pending messages. Raw current-turn
revisions cannot be selected through a policy. Lifecycle/TurnID and resource
permission checks still apply before candidates become available.

Nothing inherits implicitly from main. Empty composition gives empty messages.
`View` is a separate rendering mode, mutually exclusive with all composition,
selection, budget and formatter settings. It renders prepared stored segments,
without adding the current turn or guaranteeing budget capacity.

## Exact selection and deterministic admission

`WithSelectionPolicy` configures main, and `CompileTarget.Selection` configures a
named output. `SelectionPolicy.Select` receives owned `ContextCandidate` units
once for that output. `Ref` addresses the exact prepared unit; `Members` describes
its exact message revisions. A tool round is one unit, not separately selectable
call/result messages. An artifact unit uses its artifact content ref.

Return `SelectionChoice{Ref, Priority}` values. `SelectionPolicy.Required` adds
exact required refs. Current-turn/pending content and configured retention rules
also create mandatory units. Missing/stale refs, duplicate choices, partial rounds
and omitted mandatory units return typed errors: `ErrUnavailableCandidate`,
`ErrDuplicateSelection`, `ErrSelectionRound`, `ErrMandatorySelection`. Malformed
policy/plan configuration returns `ErrInvalidSelection`.

Core admits mandatory units first, then higher integer priorities, breaking ties
by original preparation ordinal. Priorities decide admission, never dialogue
chronology. With an explicit selection policy, each optional unit is tried against
the whole admitted representation; an over-cap unit is excluded. Required overflow
fails. Without a selection policy, normal history budgeting remains responsible
for shortening history. A configured final `OutputPolicy` runs after transforms, pending/post-budget
patches and formatting; the accepted representation is recounted afterward,
regardless of admission estimates. View outputs run the same semantic policy
before rendering.

Artifact-local caps use the output's own estimator and admit whole artifacts.
A prepared resource merge is shared before those caps; an excluded merged revision
does not silently restore an older revision. `ResourceArtifactMerge.Prepared`
records preparation, not participation in every output. The unused artifact
`Group` field is removed; no group allocator, retrieval engine or reranker is
implied. The host owns candidate retrieval and priority meaning.

## Evidence, export and persistence

`SelectionDecision` records admission-stage candidates, choices, policy identity
and exclusion reasons. `Selected` means admitted at that stage: later compaction,
eviction or formatting may change final content. Final manifest coverage, lineage
and output estimates describe that later representation. Do not treat admission
as final coverage or a transport allowlist. Output configuration pins composition,
policy identity and exact required refs; accepted replay restores recorded results
without running selection, resolvers or other callbacks again.

`CompileResult.PreparedSnapshot` and `CompileProjection.InputSnapshot` expose
owned shared preparation for local diagnostics. Final projections own their
messages/snapshot, participating artifact IDs/revisions and local artifact
estimates/exclusions. Main exclusion does not change visibility or persistence
for another output. Any required output error returns a zero `CompileResult`;
partial-success execution requires separate host calls.

Diagnostic projections can contain source/prepared input. External handoff uses
`ExportProjection(projection, selection, codec)` and the resulting allowlisted
`ExportEnvelope` only. Export selects final messages and participating artifacts
owned by that projection; an external artifact set cannot inject payloads.
Message export selects accepted output-policy revisions. Canonical artifact
payload export is a separate host disclosure decision: final prompt projection
does not modify artifact bodies. Metadata disclosure remains explicit. Do not serialize the diagnostic projection.

Persistence requires an explicit output choice. For main, use
`DerivePersistenceState(codec, profile)` to restore compile-only changes and apply the
current-turn persistence policy. A target `Snapshot` is an explicit prompt-state
choice; the host must decide which prompt transforms belong in durable state.
`ProjectCheckpoint(state, codec, profile)` filters artifact persistence/lifecycle and validates opaque bindings across the selected state; it does not restore prompt edits.
Commit under a loaded OCC revision. No API combines summaries from multiple
outputs automatically. See [the runnable example](../examples/context_projections/main.go).

`ExportSelection.ArtifactPayloadRefs` explicitly approves exact canonical artifact
revisions from `ArtifactContentRef`, independently of `MessageIDs` selecting accepted
prompt messages. This replaces export selection `ArtifactIDs`; projection
`ArtifactIDs` remains participation evidence. Stale, malformed or duplicate payload
refs fail export. A payload ref grants disclosure of the original canonical typed
body, not the output-policy representation; select only messages when handing off
the accepted prompt. Neither sanitization nor metadata allowlisting rewrites that
canonical body.
