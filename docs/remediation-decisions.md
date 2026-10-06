# Task 24 decisions

Every D entry has an explicit decision below. Accepted retention means the current
behavior is part of the contract, not silently postponed work. The numbered stages
and their independent acceptance evidence are in `remediation-evidence/stage1` …
`stage6`. Full final gates exercise the referenced tests; measurements are local
observations, not production guarantees.

| ID | Decision | Reason and implementation/evidence |
| --- | --- | --- |
| D01 | Accept change | WithBudgetPipeline takes only pipeline; whole-request count/history-only eviction. compile.go, stage1/4, retention-budget.md. |
| D02 | Accept BYOT | Exported provenance type/clone port, discriminators checked; closed ContentPart remains exhaustive. provenance.go, remediation validation tests, stage1/3. |
| D03 | Accept change | Explicit estimator required; nil/typed nil fails before effects. pipeline.go, remediation budget tests, stage4. |
| D04 | Accept change | Nil inherits, nonnil empty clears compile input only; Tools request-only. compile.go, normalize tests, stage3. |
| D05 | Accept clarification/change | Role projection includes active/pending; historical hooks/formatters keep narrow scope, raw unaffected. current_turn/compile tests, stage3. Final audit also rejects unknown/empty formatter segment or nil callback before effects (remediation formatter preflight tests). |
| D06 | Accept grouped replacement | Whole incoming group replaces (TemplateID, LayerID), missing layers append. merge_policy tests, stage3. |
| D07 | Accept validation | Zero turn absent; prompt-only and unknown persistence rejected, no fallback. current_turn tests, stage3. |
| D08 | Accept change | Stateful Compile rejects missing conversation ID/store; Snapshot explicitly stateless. compile tests, stage3. |
| D09 | Accept retention | Interrupted typed marker is unknown outcome, not successful execution; IsError false is provider presentation. round_repair_test.go, api-guide.md; host owns recovery. |
| D10 | Accept stronger contract | Raw text estimators reject binary/media; FixedEstimator is configured structural approximation. estimator tests and stage4; no provider count claim. |
| D11 | Accept retention | Positive host unknown fallback records Unknown coverage rather than measured count/zero. estimate_report tests, retention-budget.md. |
| D12 | Accept clarification; reject rename | MinMessages is minimum optional retained block size; retention policy protects required data. Keep familiar field with accurate docs/negative validation. strategies tests, stage4. |
| D13 | Accept validation | Owned nonnegative weights/total checked; complete candidates re-estimated without cross-request additivity; final gate retained. stage4 adversarial tests. |
| D14 | Accept measurement; reject callback cache | History×targets/profile measure clone amplification; targeted digest/estimate/Reporter costs measured independently (stage6 evidence-components.log). No safe host callback memoization assumption. Selection aggregation optimized, final digest/report/estimate gates retained. stage6 measurements. |
| D15 | Accept retention | Hard admission/soft compaction separate; host supplies/accepts summarizer, no model client/retry. compaction/retention tests. |
| D16 | Accept change | ResourceCodec declares current bindings; saved topology/revisions compared before decode. resource record tests, stage3. No authenticity claim. |
| D17 | Accept retention | RetireSource exact global ref including Occurrence; globally unique tenant IDs or separate stores required. blob memory tests, glossary/blob-retention.md. |
| D18 | Accept simplification | Early cancellation/known byte size before clone/hash/auth, repeated prepublication checks. memory Put, TestRemediation_EarlyPutRejection. |
| D19 | Accept reference tradeoff | Tombstones/claims prevent identity reuse; no TTL cleanup. Reference/ephemeral scale documented in blob-retention/glossary; identity/cleanup tests retained. |
| D20 | Accept retention | CheckBlob availability/binding distinct from active claim; host retains claims during use. memory retention tests, glossary. |
| D21 | Accept boundary | ResourceResolver composes explicit ports/policies; discovery/auth/model/scheduling external. resource tests/import guardrails. |
| D22 | Accept documentation/measurement; reject speculative CAS | Single memory mutex includes callbacks; nonreentrant, serial across IDs. Deterministic lock test and parallel benchmark stage5. No production throughput requirement. |
| D23 | Accept measured private transfer | Append clones old/new messages once and transfers private slice; public getters/deltas remain isolated. Appendix benchmark and TestRemediation_AppendOwnership, stage6. |
| D24 | Accept rename | ArtifactIDs/artifact_ids distinct; wrong namespace rejected application/encode/decode including empty/null wire fields. stage1 regression/migration. |
| D25 | Accept clarification; reject accidental semantic restriction | Semantic codec preserves negative version/unknown lifecycle symmetrically; persistence projection/OCC admission validates separately. serializer.go, semantic-envelope regression, checkpoint-store.md. |
| D26 | Accept retention | Empty operation is documented no-op; nonempty batch still advances OCC when committed. Delta tests/shared store fixtures. No extra DeltaNoOp alias. |
| D27 | Accept retention | Redis load may advance expiry tombstone; write ACL/availability required, marker persistent. checkpoint-store.md, live expiry/OCC conformance stage5. |
| D28 | Accept version-only clear | Postgres cleanup bypasses old bytes/host codec, standard advancing tombstone; locked token/conflict/rollback retained. Invalid schema/retired codec/live OCC tests stage5. |
| D29 | Accept retention | Raw source capture/persistence and prompt OutputPolicy separate; export requires exact allowed refs. output/capture/export tests; no implicit raw sanitizer. |
| D30 | Accept retention | Opaque carrier text is not automatically signed; protected content uses independent referenced messages, excludes self. opaque binding adversarial tests/docs. |
| D31 | Accept retention/measurement | Roundtrip validates metadata preservation at output/label boundaries. Targeted LabelProjection strict roundtrip184allocs plus semantic codec measurements; removing it would weaken trust contract. label adversarial/opaque tests, stage6 profile. |
| D32 | Accept host responsibility | BYOT label semantics unknown to core; Upgrade and DecisionRef are explicit host assertions. label tests/glossary; no classifier added. |
| D33 | Accept documentation | Diagnostic IDs/FNV not anonymization or low-cardinality labels. Host adapters control privacy/cardinality; event identity separate. observer tests/glossary. |
| D34 | Accept retention | Whole extension payload allowlisted; per-field projection requires host codec/policy. export/metadata tests/glossary. |
| D35 | Accept retention | Semantic prefix evidence differs from wire identity/cache hit/pricing. prefix tests/docs; wire confirmation separately bound. |
| D36 | Accept sync scope; reject new API for now | ExportProjection has no cancellation promise. Background codec validation documented; no demonstrated large-export need justifies public API expansion/workers. export tests/glossary. |
| D37 | Accept measurement/retain boundaries | Source/prepared/target snapshots protect separate policies. Clone amplification within selection removed; independent copies retained. history10/100/1000×targets0/1/4 benchmark and concurrency tests. |
| D38 | Accept focused private session | Context/Recorder/Resources/Identity explicitly grouped and prepared pipeline carries session. Three old dependency keys removed; branch bindings copied. Context bridge retains callback/helper propagation, other contextual evidence collectors remain. No wholesale signature rewrite/public orchestration. Session binding/reuse/recording tests. |
| D39 | Accept scope docs; reject wholesale rename | RenderView registry and CompileTarget.View built-in format deliberately separate; no alias added. api-guide/glossary, view acceptance stage3. |
| D40 | Accept validation | Negative MinMessages/nontext configuration fails; documented zero defaults and effective evidence retained. stage4 boundary/fuzz tests. |
| D41 | Accept wording correction | Result DTOs owned mutable, private state immutable, AllSegments deep copies. GoDoc/README/glossary and ownership tests. |
| D42 | Accept selective simplification | Remove obsolete Overlay/positional substring bans; public signatures verified through reflect, independent of filenames. Keep AST import/semantic/context-global boundaries and behavioral regressions. architecture/documentation tests. |
| D43 | Accept glossary; reject broad rename | Descriptor means versioned component identity, unlike ResourceDescriptor; renaming every field would add churn without stronger distinction. glossary. |
| D44 | Accept portability/gates | Python rewrite, isolated checkout, exact atomic refs; validate and release gates aligned. release.md/Makefile/local14fixtures, stage2. |
| D45 | Accept example retention | RunLive is explicit integration example; no evaluator/model client in core, no mandatory evaly. evaluation examples/import boundaries. |

## Defect verification

| Findings | Original behavior on review SHA | New regression/evidence |
| --- | --- | --- |
| F01/F02 | scripts/test_release.py old release fixtures; untracked/tag publication and detached checkout | stage2 local14fixtures, exact refs/isolation/failure/retry/interruption |
| F03/F04/F05/F11/F12 | preserved review probes via scripts/reproduce_review.py | stage3 baseline + identity/config/materialization/view/provenance regression tests |
| F06/F07/F08 | same original budget/overhead probes | stage4 baseline + owned estimator/arithmetic/full-request adversarial tests/fuzz |
| F09/F10 | original TTL/cancel overlays, race count10 | stage5 baseline + wire/live PTTL/deterministic lock-wait regressions |

The reviewed snapshot is 2a1a7ab7fb919c436e210c2804dd615e2d80626a. Old API
compilation failures are never substituted for behavioral defect reproduction.
CTX-001–006 reconciliation remains in remediation-contracts.md. No provider/sibling
SDK, harness, scheduling, permissions UI or model execution was added to the core.
