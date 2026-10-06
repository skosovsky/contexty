# Task 24 execution journal

Base: 2a1a7ab7fb919c436e210c2804dd615e2d80626a; clean main at start.
Run stages sequentially; two independent reviewers inspect the same frozen diff.
Commit only after completeness=100% and no unresolved confirmed correctness issues.
Full objective and all F01–F12 / D01–D45 remain mandatory.

## Stage 1 — contracts/API

Scope: lock final semantics before behavioral repairs in stages 3–5; implement
signature/type changes D01, D02 and D24 with consumer migration. Enforcement of
F03–F12, D03–08/10/13/16/39/40 remains assigned to the subsequent ordered stages.
This staging does not claim those behavioral fixes are done.

Checklist (8 equally weighted criteria):
- [x] S1.1: normative contracts select every D01–08/10/13/16/24/39/40 decision and F03–F12 invariant, with explicit defaults/errors/scopes.
- [x] S1.2: event identity/provenance extensibility and legacy wire migration selected explicitly.
- [x] S1.3: D01 new signature implemented and all repository consumers migrated.
- [x] S1.4: D02 host-implementable provenance methods, built-in wire compatibility, external-package clone/codec regression.
- [x] S1.5: D24 ArtifactIDs implemented in application/encode/decode, explicit old removal rejection and AAA roundtrip/isolation regressions.
- [x] S1.6: ownership/concurrency/freeze/callback/cancellation matrix and view applicability documented without claiming unverified tests.
- [x] S1.7: migration/current contract navigation and CTX-001–006 reconciliation present.
- [x] S1.8: root regression suite, examples and pinned lint pass.

Acceptance procedure: store the two independent reports for the final diff before commit.

## Remaining sequential stages

2: F01/F02 + D44 release isolation, precise refs, cleanup and local fixtures.
3: F03/F04/F05/F11/F12 + D04–08/16/39 identity/config/validation and codec migration.
4: F06/F07/F08 + D03/10/12/13/40 estimator/arithmetic/whole-request budgets.
5: F09/F10 + D22/28 stores, TTL, cancellation, clear parity, OCC tests.
6: D14/18/23/37 measurements, D38/42 justified internals, docs/quickstart/glossary,
all D decisions with evidence, concurrency verification and final DoD gates.

Stage SHAs and reviewer results are recorded after acceptance. Each commit's SHA
is recorded in the next journal update (a commit cannot contain its own SHA).

Stage 1 acceptance: completeness agent stage1_completeness 8/8=100%; correctness
agent stage1_correctness PASS, prior D24 empty/null/mixed-case finding resolved.
Final root go test -race ./... PASS; golangci-lint 2.14.0 all modules 0 issues.
Evidence: docs/remediation-evidence/stage1/. Commit: feat: remediation contracts;
SHA recorded at start of stage 2.

## Stage 2 — release

Stage 1 commit: 8a01f87 (feat: remediation contracts).
Scope F01/F02/D44; contract docs/release.md precedes implementation.
Checklist (7 equally weighted criteria):
- [x] S2.1: isolated release from committed HEAD, exact staging; source HEAD/branch/index/tracked/untracked/tags preserved.
- [x] S2.2: exact root/submodule refs, atomic push, no unrelated tags or branch publication.
- [x] S2.3: cleanup on success/preparation/tag/push failure touches only invocation resources; outcome reported; unknown outcome never deletes refs.
- [x] S2.4: portable module rewriting and validated relative module list; no BSD sed dependency.
- [x] S2.5: release gates aligned with validate and platform/recovery documentation linked.
- [x] S2.6: AAA local fixtures cover clean/untracked/multiple modules/rejected push/preparation/tag failure/retry/cleanup, plus baseline reproduction on review SHA.
- [x] S2.7: fixture suite and shell/Python checks pass; two independent reviewers accept final diff.

Stage 2 acceptance: completeness 7/7=100%, correctness PASS; interruption outcome
P2 fixed and independently repro-tested. All14 fixtures PASS incl reviewSHA F01/F02;
independent12 current fixtures PASS; bash-n/py_compile/diff-check PASS.
Evidence docs/remediation-evidence/stage2/. Commit fix: release isolation;
SHA recorded at start of stage 3.

## Stage 3 — identity/config/validation

Stage 2 commit: ac578ec (fix: release isolation).
Normative baseline docs/remediation-contracts.md precedes all implementation.
Scope F03/F04/F05/F11/F12 + D04–08/16/39; budget implementation stays in stage 4.
Checklist (12 equally weighted criteria):
- [x] S3.1: F03 event IDs use explicit TurnID/kind/turn ordinal, retries independent of trimmed history, explicit IDs preserved; insufficient historical/derived identity errors rather than positional fallback.
- [x] S3.2: D04 nil inheritance versus explicit empty clear preserved through store merge/Freeze/Normalize; Tools from request only.
- [x] S3.3: D05 role projection covers Pending/CurrentTurn with trace/transformation evidence; hooks/segment formatters scope remains explicit, raw/persistence unchanged.
- [x] S3.4: D06 whole incoming group replaces (TemplateID,LayerID), keeps ordering/all messages, missing layers append.
- [x] S3.5: D07 zero/nil current turn absent, prompt-only/unknown-policy errors before source loss; no hidden fallback.
- [x] S3.6: D08 incomplete stateful Compile errors before store/callback; CompileSnapshot stateless.
- [x] S3.7: F04 both entrypoints/on-off recording/resource and message blocks validate known enum/defaults before side effects; unknown-policy fallback removed.
- [x] S3.8: F05 pointer/value canonical ownership and validation, typed nil error/no panic, helpers consistent, Engine and ResourceResolver tested.
- [x] S3.9: F11 reserved/duplicate named view registrations fail before render callbacks; unique registered view works, applicability documented.
- [x] S3.10: F12 registration rejects invalid decoder/type; data decode errors on nil/typed-nil/discriminator mismatch; callback errors and built-in/common codec roundtrip preserved.
- [x] S3.11: D16 current ResourceCodec custom bindings independent from saved configuration, snapshot-owned, mismatch rejected before decode; required consumers migrated.
- [x] S3.12: preserved baseline behavioral probes prove F03/F04/F05/F11/F12 on review SHA; equivalent AAA regressions and root race/pinned lint pass; docs/migration/examples synchronized.

Stage 3 acceptance: completeness 12/12=100%; correctness PASS. Two confirmed
provenance clone-boundary P2 findings corrected and independently reproduced.
Final root race41.947s PASS; affected final race1.934s PASS; pinned lint all3
modules0 issues; baseline behavioral F03/F04/F05/F11/F12 PASS.
Reports and logs: docs/remediation-evidence/stage3/.
Commit fix: context validation; SHA recorded at start of stage4.

## Stage 4 — budgets

Stage 3 commit: 80fb346 (fix: context validation).
Normative baseline docs/remediation-contracts.md precedes implementation.
Scope F06/F07/F08 + D03/10/12/13/40.
Checklist (9 equally weighted criteria):
- [x] S4.1: explicit estimator required, nil/typednil and invalid known config rejected before callbacks on both compile entries and standalone Apply.
- [x] S4.2: F06 owned estimator inputs and weights, cancellation before/after callbacks; mutating/retained aliases cannot change accepted context.
- [x] S4.3: F07 checked nonnegative arithmetic, safe ceil, totals/per-message consistency and typed errors prevent false admission; builtins/tool/fallback paths covered.
- [x] S4.4: F08 one full request estimate includes fixed/pending/current, fitting input retained unless explicit soft compaction; no cross-request additivity assumption.
- [x] S4.5: overflow eviction evaluates complete candidates, keeps required/tool rounds, mandatory final output validation for main/targets/views.
- [x] S4.6: D12/D40 MinMessages optional block threshold/defaults documented; negative configuration errors, explicit retention used for protection.
- [x] S4.7: built-in approximation rejects unsupported media/binary payload, FixedEstimator structural meaning and quality/fallback distinctions documented.
- [x] S4.8: preserved baseline F06/F07/F08 behavioral probes executed at review SHA; equivalent AAA regressions and deterministic arithmetic fuzz seeds.
- [x] S4.9: root race/pinned lint/appropriate guardrails pass, contracts/migration/examples synchronized; independent100%/PASS acceptance before commit.

Stage 4 acceptance: completeness9/9=100%; correctnessPASS. Cross-request subset
preflight, empty-cost bypass and atomicity-path P2 findings corrected; final
candidate re-estimate also hard-checked. All independent repros now pass.
Root race50.281s PASS; affected final race1.510s PASS; pinned lint all3 zeroissues;
fuzz10s+10s PASS; benchguardrails before/afterPASS513→514truncateallocs/op.
Preserved reviewSHA F06/F07/F08 behavioral baseline PASS. Reports/logs/measurements:
docs/remediation-evidence/stage4/. Commit fix: budget accounting; SHA in stage5.

## Stage 5 — stores

Stage4 commit:2a543b7 (fix: budget accounting).
Normative baseline docs/remediation-contracts.md precedes implementation.
ScopeF09/F10/D22/D28; OCC tombstones and atomic batch commit preserved.
Checklist (7 equally weighted criteria):
- [x] S5.1: Redis positive TTL ceil to positive milliseconds, zero persistent, negative rejected; boundary/maxduration wire tests and live positive PTTL integration evidence.
- [x] S5.2: F10 memory operations recheck cancellation after lock and before mutation; deterministic waiting-lock tests prove unchanged state/version on cancellation.
- [x] S5.3: D28 Postgres clear independent of old payload codecs, version-only OCC, advances empty tombstone; stale/exhausted/concurrent/missing cases preserve parity.
- [x] S5.4: atomic batches, ABA tombstones, expiry identity and recreate protections remain; shared conformance plus root/adapter regressions pass.
- [x] S5.5: D22 codec-under-lock non-reentrancy documented and measured; keep simple mutex with explicit tradeoff, no speculative lock manager.
- [x] S5.6: immutable reviewSHA behavioral F09/F10 overlay probes executed; equivalent AAA regressions cover fixes.
- [x] S5.7: all3modules race/pinnedlint; Redis/Postgres isolated container tests run without skips; store contracts/migration reflect semantics, both independent acceptance100%/PASS.


Stage 5 acceptance: completeness7/7=100%; correctnessPASS. No confirmed unresolved
findings. Root race42.754s, Redis17.196s, Postgres4.172s PASS; isolated
containers without skips, all3 pinnedlint0issues. Independent Memory/TTL ×3
and addressed live Postgres3.416s PASS. Original F09/F10 baseline -race count10
PASS. Mutex benchmark samples/repeat document callback cost and timing noise,
without throughput claims. Evidence docs/remediation-evidence/stage5/.
Commit fix: store boundaries; SHA recorded at start of stage6.

## Stage 6 — private implementation and docs

Stage5 commit:4cb73bb (fix: store boundaries), signed.
Scope D14/D18/D23/D37/D38/D42, final docs and D01–45 decisions/DoD.
Checklist (10 equally weighted criteria):
- [x] S6.1: reproducible baseline/after history×target and append allocation measurements; repeated estimation/digest costs measured without callback purity/cache assumptions.
- [x] S6.2: D18 early cancellation/known size rejection before clone/hash/authorization; retained prepublication checks and AAA regressions.
- [x] S6.3: D23 private append ownership transfer reduces redundant clones; public immutable state/input/result isolation preserved and regression tested.
- [x] S6.4: D38 private compileSession explicitly groups recorder/resources/identity, branch-local bindings and callback context propagation; old dependency keys removed without public framework.
- [x] S6.5: D42 obsolete file/text migration tests removed or replaced by typed/behavioral contracts; import and semantic boundaries retained.
- [x] S6.6: runnable durable quickstart, compact README/navigation, glossary, owned DTO GoDoc and concrete final migration/scopes; useful old README details preserved in thematic docs.
- [x] S6.7: every D01–D45 explicitly accepted/rejected with reason and evidence; F01–F12 old behavior/new regression evidence mapped; CTX reconciliation retained.
- [x] S6.8: concurrency/ownership matrix claims backed by race-tested Engine/BudgetPipeline/registry/store reuse and branch isolation tests; host synchronization/nonreentrancy limitations explicit.
- [x] S6.9: addressed reproducible fuzz/edge checks for arithmetic, event IDs, pointer/value and codec boundaries; no implicit estimators/unknown enum fallback in chosen production paths.
- [x] S6.10: final all3 race/pinned lint/isolated Redis+Postgres without skips, acceptance/bench/release fixtures/oldSHA repro and independence gates PASS; both reviewers accept final diff100%/PASS before commit.


Stage 6 acceptance: completeness10/10=100%; correctnessPASS. Evidence component
measurement gap and RenderView/stale preflight wording resolved, both reviewers
reaccepted final state. CItrue makevalidate exit0: all3 pinnedlint0, acceptancePASS,
benchguardrailsPASS, rootrace37.137s/Redis17.131s/Postgres4.796sPASS in isolated
containers without skips. Release12currentfixturesPASS plus separately enabled
2oldSHAfixturesPASS; F03–12 original probesPASS. IDs/parts/codec fuzz167864/325332
executionsPASS; finalboundaryrace2.217s and rootlint0PASS. Measurements/tests/docs
reports in docs/remediation-evidence/stage6/. Commit refactor: compile internals;
SHA recorded in final audit. Final independent whole-goal audit remains required.
