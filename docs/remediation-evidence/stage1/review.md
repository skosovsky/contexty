# Stage 1 independent acceptance

Final frozen diff v3, base 2a1a7ab7fb919c436e210c2804dd615e2d80626a.

Completeness agent `/root/stage1_completeness`: 8/8 = 100%, PASS.

| Criterion | Evidence |
| --- | --- |
| S1.1 | remediation-contracts baseline selects all scoped D and F invariants/defaults/errors |
| S1.2 | host provenance, unchanged built-in wire, artifact migration and event identity selected |
| S1.3 | WithBudgetPipeline(pipe) and repository consumers migrated |
| S1.4 | external-package provenance clone isolation and codec/built-in roundtrip tests |
| S1.5 | ArtifactIDs application/encode/decode, old/wrong namespace presence rejection (empty/null/mixed case), owned ID/state regressions |
| S1.6 | ownership/concurrency matrix, freeze/reuse/reentrancy/cancellation, view applicability; race claims still require stage 6 evidence |
| S1.7 | contracts/migration navigation and CTX-001–006 reconciliation |
| S1.8 | root race suite 37.007s + packages/examples PASS; pinned lint 0 issues all three modules; independent targeted race test PASS |

Correctness agent `/root/stage1_correctness`: PASS. No unresolved confirmed errors
in stage 1 scope. Inspected D01 consumers, D02 host clone/wire, D24 ownership and
namespace enforcement, docs and staged gates. Independent targeted tests and
diff whitespace check PASS; final root race/all-module lint logs inspected PASS.

Initial P2: old message_ids empty/null accepted. A second reproduction showed
mixed-case null bypass because encoding/json matches fields case-insensitively.
Both were corrected with presence enforcement and EqualFold; final regressions
and independent temp-harness reproductions confirm rejection.

These reports accept stage 1 only. F03–F12 and later behavioral gates remain
mandatory. No claim of absolute absence of defects or final goal completion.
