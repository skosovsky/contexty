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
