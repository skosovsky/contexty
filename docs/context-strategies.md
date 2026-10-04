# External context strategies

Contexty prepares, selects, transforms and validates semantic context. The host
decides what the agent needs, keeps original events outside the working window,
chooses models and authorizes access. A checkpoint is resumable working state;
it is not a transcript archive. `DerivePersistenceState` excludes evicted and
truncated messages, so persisting only that projection loses those originals.

## Choosing and combining strategies

| Strategy | Useful when | Loss or additional responsibility |
| --- | --- | --- |
| Sliding window | Recent exchanges contain the required evidence | Older facts disappear unless explicitly retained or recoverable elsewhere |
| Rolling summary with recent tail | A compact interpretation of older exchanges is sufficient | The host summarizer can omit or distort facts; source refs identify origins but do not recover bytes |
| Explicit offload preview | A large completed result should remain available without entering every prompt | Preview is incomplete; host must keep bytes and claims available and authorize bounded restore |
| Host-selected retrieval | The current task identifies a relevant archived episode or resource chunk | Search/ranking and access decisions remain host policy; a missed selection cannot be repaired by core |

These strategies compose. Archive original events first; explicitly offload large
completed evidence; summarize an older prefix while preserving the recent tail;
retrieve the original detail when the next task needs it. Core does not infer
this sequence from text and `Compile` does not automatically offload content.
Active tool calls and incomplete rounds retain their actual typed protocol.

Protect indispensable context using `RetentionPolicy` or exact required selection
refs. If mandatory content exceeds capacity, handle the explicit error. A
summary is a new local semantic message with compaction lineage, never an original
event or an external `OpaqueState`. An opaque envelope has host-defined bytes,
placement, codec/profile and exact dependencies; changes can invalidate it even
when a summary looks equivalent. See [retention budgeting](retention-budget.md)
and [opaque state](opaque-state.md).

## Archive, checkpoint and resume

The [archive/resume recipe](../examples/archive_resume) keeps a host-owned journal
and its query interface next to the example. The sequence is:

1. Append original typed events with stable host `SourceRef` identities to the
   journal before compiling a working window.
2. Compile a summary and recent tail, apply `ProjectCheckpoint` explicitly, and
   commit the working state under its OCC revision.
3. Load that checkpoint to resume working context. Query the separate journal
   for an old source identity; do not treat summary text as the archived event.
4. Select an exact `ResourceDescriptor` for that original episode. A fresh host
   scope authorizes the bounded `ResourceResolver` read and the host explicitly
   materializes its typed artifact for the next compile.

The example writes original event/body/descriptor files, recovers the old source
identity from the reopened summary's refs, and selects descriptor metadata before
loading its body. It reopens a serialized checkpoint through `ConversationCodec`.
Reopening those files demonstrates resume
semantics. The temporary journal supplies no fsync, transactional publication,
concurrent-writer or crash-recovery guarantee; its files are removed after the run.
A production host must supply durable transcript and checkpoint backends with
their own access, deletion and reconciliation policies.
No transcript store/query API is added to contexty.

## Resource discovery and JIT reads

The [progressive disclosure recipe](../examples/progressive_disclosure) uses host
catalog metadata and deterministic search to select chunk descriptors, then
performs bounded authorized reads. It selects another chunk on a later step and
records reader activity. Descriptor discovery is separate from body loading:
only selected bodies should be read. The local fixture stores source strings in
process and generates typed bodies during selected reads; it does not establish
production storage latency or streaming behavior.

Revision, typed artifact digest and serialized body length bind the chosen
descriptor to the actual body. Fresh scopes and host permission checks still
apply; possessing a descriptor grants no permission. Stale and denied resources
return typed errors with no fallback body. Retrieved instructions remain data:
provider roles and display names do not grant trust or install capabilities.
Use the existing [selected resource contract](resource-content.md), including its
native deferred declarations, codec bindings, evidence capture and replay rules.

## Offload, memory and retention

The [composition recipe](../examples/context_composition) offloads completed
tool-derived evidence explicitly, compiles a preview with rolling summary and a
recent tail, then restores original detail with an authorized byte bound. It
coordinates provisional claims, checkpoint publication, claim commit, retirement,
release and collection through the existing [blob retention protocol](blob-retention.md).
Unknown checkpoint outcomes require authoritative reconciliation; a timeout is
not permission to abort a claim. Claims must remain active through consumption.

The example also keeps host-defined typed facts with sources and versions, and
makes an explicit replacement decision for a conflicting fact. `MemoryBlock`
provides lifecycle, ownership and persistence metadata. It does not extract truth,
resolve contradictions, consolidate memories or implement forgetting. The memory
blob/resource adapters are process-local fixtures, not durable storage.

Offload reduces issued prompt content. Exact replay capture can still retain the
original private body, including resource revisions excluded from final admission.
That capture is a separate host retention decision and is not retention
minimization. Deleting required captured bytes can make exact replay unavailable.
Prompt-only `OutputPolicy` acceptance also does not sanitize Source, archives,
checkpoints or canonical artifact exports. See [output policy](output-policy.md).

## Reproducible evaluation and measured limits

Run the separate [offline evaluation runner](../examples/context_evaluation):

```bash
go run ./examples/context_evaluation
```

It compares sliding window, rolling summary, offload and host-selected retrieval
using the same fixed corpus, effective budget, estimator and evaluator profile.
Machine-readable reports pin fixture/policy/model/estimator identities and record
selected semantic content/refs, exclusions, actual estimates, callback counts and
timing. Structural results are reproducible; wall-clock durations are diagnostic
and must not become byte-equality expectations or flaky CI thresholds.
Strategy-specific observations live in `rows`; independently executed mechanical
preflight checks live once per fixture in `behaviors`. A preflight result is not
four separate strategy observations. `host_wall_nanoseconds` includes host
orchestration elapsed time and is not CPU time or provider latency.

Offline checks use deterministic host callbacks and source-defined expected
facts/constraints. They exercise long distracting results, delayed facts,
conflicting versions, retrieved instructions, protected history, partial rounds,
stale/denied dependencies, independent consumers and opaque-state invalidation.
Passing these fixtures proves the specified mechanical behavior. It does not
prove real-model summary fidelity, answer quality or prompt-injection resistance.
The fixture summarizer has explicit fact markers, and retrieval receives host
query source IDs from the authored tasks. Those deliberately favorable policies
measure recovery mechanics, not the quality of an unknown search/ranking system.
Source attribution checks compare message-level `SourceRefs`; a summary can carry
the union of its input sources. That establishes reference transport, not semantic
proof that each individual fact was derived from its claimed source.

Real provider usage, money and model quality remain `not measured` without a
provider run. Character estimates are semantic estimates, never billed provider
tokens. CPU benchmarks measure implementation cost separately. An optional host
runner accepts typed model/summarizer/evaluator ports with pinned identities and
an explicit run count. Only an explicit host invocation connects credentials and
SDKs; ordinary tests and `make validate` do not call a model. Missing usage/cost
measurements remain absent or `not measured`, rather than inferred from text.

No strategy is universally best. A production evaluation must fix its task
distribution, selection policy, provider estimator, model/summarizer and evaluator,
record repeated runs and report their limits. Authorization, external transcripts,
durable blob/claim backends and model quality remain application responsibilities.

The example-local `evaluation.RunLive(ctx, cfg, adapters, runs, explicitOptIn)`
requires explicit opt-in, nonnil typed adapters and pinned identities; its estimator
model must match the supplied model identity. Per-row quality is the evaluator's
observation. Usage/cost cover only final answer generation returned by `Model`.
Summarizer and evaluator provider usage/cost remain `not measured`, because their
ports return no such observations. The report is not a total workflow bill.

## Recorded offline baseline

The offline command's measured baseline contains 40 strategy rows over 10 fixtures,
one run each, with effective capacity 640 for every strategy. It pins
`context-evaluation-report/fixture-v1`, model `offline-no-model/fixture-v1`,
summarizer `fixture-marker-summary/fixture-v1`, evaluator
`fixture-facts-sources/fixture-v1` and estimator `rune-counter/fixture-v1`.
All issued estimates have quality `estimated` and are within capacity.

| Strategy | Source-defined fact checks | Issued semantic estimate range | Summary callbacks | Resource reads | Blob writes |
| --- | --- | --- | --- | --- | --- |
| Sliding window | 6/11 | 26–520 | 0 | 0 | 0 |
| Rolling summary | 11/11 | 26–505 | 25 | 0 | 0 |
| Explicit offload | 6/11 | 26–536 | 0 | 0 | 1 |
| Host-selected retrieval | 11/11 | 26–172 | 0 | 9 | 0 |

These are totals/ranges across each strategy's 10 rows, not provider tokens or
quality scores. Sliding and offload each lose five authored facts after their
history admission. Offload preserves availability of the large completed tool
result but does not recover unrelated lost history automatically. The deterministic
rolling summarizer and explicitly selected retrieval recover all 11 fact checks
under their fixture policies. This is a measured comparison of those policies,
not a ranking of real model-driven approaches.

All non-fact row checks pass, including capacity, required constraints and actual
delivery of retrieved instruction bytes as `RoleUser` data. Separate `behaviors`
contain 10 fixture records with seven executed checks, all passing: partial-round
preservation/fail-closed/pending handling, resource access, blob access, independent
consumers and opaque invalidation. Empty preflight lists mean no additional check
for that fixture; these seven checks are not multiplied by the four strategies.

Wall-clock values are intentionally omitted from this table. Real LLM quality,
provider usage/cost and new durable transcript/blob/claims integrations are
`not measured`. No provider SDK or durable backend is added by these recipes.
Re-run the command for current JSON evidence when fixtures or policies change.
