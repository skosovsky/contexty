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
