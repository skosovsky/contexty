# Stage 4 independent correctness acceptance

Verdict: PASS for final stage 4 diff relative to 80fb346. No unresolved confirmed correctness issues in S4.1–S4.9 scope.

Reviewed explicit estimator and known policy preflight, frozen built-in configuration, estimator/tool callback ownership and cancellation, owned returned weights, checked nonnegative arithmetic and safe ceiling division, whole-request budgeting, fixed/pending/current protection, candidate eviction with contextual nonadditive estimates, tool-round/rolling-tail scopes, retention and MinMessages, custom strategy/summarizer bounded optional-block contracts, mandatory composed/final cost validation, approximation/unknown-quality documentation and migration.

Resolved confirmed P2 findings:
1. Cross-request subset estimates incorrectly controlled admission: pending-only cost could reject a fitting full request; required-only cost could reject an overflowing full request with a fitting retained complete candidate. Pending-only checks now classify errors only after aggregate failure, and built-in complete-candidate eviction precedes subset reservations. Independent repro /private/tmp/contexty-stage4-nonadditive-review.go now succeeds for full cost2 despite pending-only100; /private/tmp/contexty-stage4-candidate-review.go retains system+one history costing8 despite required-only100, limit10.
2. Empty standalone Apply bypassed the estimator and reported a fitting zero count for positive-over-limit/negative host estimates. Empty input now traverses estimateOwned; /private/tmp/contexty-stage4-empty-review.go returns error instead of false admission.
3. Complete-candidate eviction ignored explicit KeepTurnAtomicity=false for host estimators, diverging from the same costs via FixedEstimator. It now respects the flag and validates final round layout before returning. /private/tmp/contexty-stage4-atomicity-review.go now returns ErrInvalidToolRound for both estimators.

Also inspected final complete-candidate re-estimate hard-limit check (mutable counter regression), custom strategy owned inputs/results, and direct pre/post cancellation around EstimateTool.

Verification:
- Four independent repro programs above rerun against corrected semantic state: required outcomes confirmed.
- Independent go test ./... -run TestRemediation_Budget -count=1 PASS; /private/tmp/contexty-stage4-independent-review.log, includes added nonmonotonic/empty/final-estimate/atomicity/unsupported-binary and two-image MaxInt overflow tests.
- Final root race log inspected PASS (root50.281s); /private/tmp/contexty-stage4-race.log.
- Final affected race after equivalent budgetCandidateUnit lint extraction inspected PASS (root1.510s); /private/tmp/contexty-stage4-extra.log.
- Final pinned lint inspected 0 issues root/Redis/Postgres; /private/tmp/contexty-stage4-lint.log. Earlier gocognit issue resolved with behavior-preserving helper extraction, inspected independently.
- git diff --check PASS.
- Preserved actual review-SHA baseline F06/F07/F08 behavior evidence inspected PASS; /private/tmp/contexty-stage4-baseline.log.
- Arithmetic and character fuzz10s logs inspected PASS (323296/296978 executions), deterministic seeds present; /private/tmp/contexty-stage4-fuzz-arithmetic.log and /private/tmp/contexty-stage4-fuzz-character.log.
- Before/after bench guardrail logs inspected PASS; /private/tmp/contexty-stage4-bench-before.log and /private/tmp/contexty-stage4-bench-after.log.

Limits: acceptance is stage 4 only. Arbitrary nonadditive costs do not imply an optimal subset solver; custom strategies/summarizers retain their documented optional-block allowance contract and complete outputs are independently admitted. Tests/review do not establish absolute absence of defects, provider-exact tokenization, or later store/internal/doc gate completion.
