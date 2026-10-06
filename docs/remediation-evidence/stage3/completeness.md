# Stage 3 independent completeness acceptance

Reviewer: stage1_completeness (read-only repository inspection). Base: ac578ec.
Final scope includes the TraceProfile/Lineage assertions in ActiveRoleProjectionScope
and caller Codecs-slice mutation in ResourceReplay_IndependentCodecs.
Final production corrections preserve original CurrentTurn metadata until
normalization and use ownCompileMessage before callback/deferred/summary cloning;
CurrentTurn and Deferred provenance clone parity regressions are included.

Result: **12/12 fully satisfied = 100%, PASS**. No remaining completeness gaps.
The earlier S3.3 trace-evidence gap was resolved before this acceptance.
Budget implementation and later stages are not accepted by this report.

| Criterion | Status | Evidence |
| --- | --- | --- |
| S3.1 | PASS | identity_policy.go:52 hashes unambiguous JSON tuple prefix/TurnID/kind/ordinal, independent of content/history length. transform_record.go normalization supplies pending-local ordinals and preserves explicit IDs. remediation_validation_test.go:15/51/80/93 verifies trimmed-history retry, pending/current distinction, historical/missing identity errors and content revision separation. Existing generated-ID fail-closed acceptance remains. |
| S3.2 | PASS | mergeCompileRequest selects request segment by nil presence, not length; Tools is request-only. Freeze/Normalize preserve nonnil empty via cloneCompileInputMessages and normalization empty branch. remediation_validation_test.go:118 checks inherited history, cleared system/memory, empty tools, preserved store/version, Freeze/Normalize presence. |
| S3.3 | PASS | compile.go applies role projection to compilePending (which includes current prompt), using normal traceStage and transformation recording. remediation_validation_test.go:157 now enables TraceProfile, validates Lineage and asserts role records for exactly history/pending/current, unchanged logical IDs with changed contentrefs; checks roles, callback mutation isolation, source/persistence roles and formatter source scope. |
| S3.4 | PASS | merge_policy.go removes existing groups by tuple TemplateID/LayerID and appends the complete incoming sequence. remediation_validation_test.go:487 checks separate templates, two messages in the same incoming layer, incoming order and missing-layer append. merge_policy_test.go covers empty template namespace. Unknown fallback removed. |
| S3.5 | PASS | current_turn.go validates prompt-only and invalid persistence before normalization; zero turn becomes absence. remediation_validation_test.go:217 and existing CurrentTurn acceptance check errors, no store effect and zero turn absence. No prompt-safe-to-raw fallback. |
| S3.6 | PASS | Compile checks typed-nil/incomplete configured store and empty conversation ID before load/callback; CompileSnapshot remains stateless. remediation_validation_test.go:217 verifies zero store reads and stateless bypass. |
| S3.7 | PASS | validateBaseCompileConfiguration always runs; deferredConfiguration validates enum/default values before recording short-circuit and before effects, including blocks with nil resolver. applyMergePolicy returns error for unknown policy. remediation_validation_test.go:263 exercises both entrypoints, recording on/off, message/resource block combinations, zero callbacks/store reads and memory/append defaults. |
| S3.8 | PASS | content_part.go canonicalPartValue/ownContentParts rejects typed nil, owns value representations and validates them; materialization.go:116 checks those returned parts, rejecting calls/results. AST helpers use shared canonical representation. remediation_validation_test.go:336/511 covers invalid value/pointer parts, typed nil/no result, valid text/media equivalence and byte ownership for Engine and ResourceResolver, plus helper/codec parity. |
| S3.9 | PASS | view_registry.go:19 rejects reserved/duplicate/invalid named configuration and RenderView returns error before callbacks. remediation_validation_test.go:386 checks builtin collisions/duplicates/no output or callbacks; existing named-view acceptance verifies unique working view. Baseline and migration explicitly distinguish RenderView registry from CompileTarget builtins. |
| S3.10 | PASS | provenance.go:47 registration rejects invalid types/nil callbacks; Decode:60 rejects nil/typed nil/discriminator mismatch while wrapping callback errors. New invariant test:417 covers direct/common codec boundaries; additional CurrentTurn/deferred clone parity matrices cover nil/typed-nil/wrong-discriminator CloneProvenance results. ownCompileMessage checks original-vs-owned identity before source can disappear, including deferred callbacks and standalone summary normalization; stage1 host/builtin clone/codec tests remain and passed root suite. |
| S3.11 | PASS | ResourceCodec.Codecs holds current bindings, snapshot:29 clones them, validateConfiguration compares current topology/revisions with saved config before decode. WithDeferredBlocks and WithReplayResourceCodecs invoke owned snapshots. remediation_validation_test.go:464 checks mismatch and zero decoder calls; resource_replay_codec_test.go:12 now mutates caller Codecs revision after freeze and still successfully replays. Required examples/resource fixtures carry bindings. |
| S3.12 | PASS | scripts/reproduce_review.py archives immutable review SHA and runs preserved unchanged probes. /private/tmp/contexty-stage3-baseline.log contains actual F03/F04/F05/F11/F12 behavior plus assertion PASS. /private/tmp/contexty-stage3-regression.log passes AAA regressions. Final full root race log passes root 41.947s and all packages/examples. The only subsequent production edit is equivalent makezero initialization in resolvedDeferredMessages; affected final cases pass race in stage3-extra.log (1.934s). Pinned lint log reports 0 issues all 3 modules. migration.md and changed examples document implemented semantics while explicitly leaving budgets for stage4. |

Verification inspected: /private/tmp/contexty-stage3-race.log,
/private/tmp/contexty-stage3-lint.log, /private/tmp/contexty-stage3-baseline.log,
/private/tmp/contexty-stage3-regression.log, /private/tmp/contexty-stage3-extra.log.
Acceptance is completeness, not a proof of absolute absence of bugs; separate
correctness reviewer acceptance is required before commit.
