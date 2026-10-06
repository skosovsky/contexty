# Stage 3 independent correctness acceptance

Verdict: PASS for the final frozen stage 3 diff relative to ac578ec. No unresolved confirmed correctness findings in S3.1–S3.12 scope.

Reviewed logical event identity and pending/current-turn ordinals, missing historical identity errors, explicit identity preservation, nil versus empty overrides, active role projection with transformation/trace evidence and raw writeback preservation, tuple-based whole layer replacement, current-turn/stateful preflight, deferred enum validation before store/resolver effects, canonical pointer/value AST ownership and materialization rejection, named-view registration guards, provenance registration/decode/clone invariants, and current ResourceCodec.Codecs identity validation before decoding. Inspected normative baseline, migration and affected consumers/examples.

Resolved confirmed findings:
- P2: Compile cloned CurrentTurn during store merge before checked normalization, losing nil/wrong-discriminator host provenance; CompileSnapshot rejected the same input. Preserving the original turn until checked normalization fixes entrypoint parity. Independent repro /private/tmp/contexty-stage3-clone-review.go now returns ErrInvalidProvenance and no output for both entrypoints; regression covers nil/typed-nil/wrong discriminator, raw/prompt-safe, both entrypoints.
- P2: resolvedDeferredMessages cloned callback messages before checking provenance integrity. Invalid host cloning silently erased metadata and returned success. Shared ownCompileMessage now checks original-to-owned provenance identity before transfer; deferred and identity fallback paths use it. Independent repro /private/tmp/contexty-stage3-deferred-provenance-review.go now returns ErrInvalidProvenance and no output. Regression covers nil/typed-nil/wrong discriminator with durable identity enabled/disabled.

Evidence:
- Independent go test ./... -run TestRemediation_ -count=1 PASS; /private/tmp/contexty-stage3-independent-review.log.
- Independent repro reruns above PASS (errors returned as required).
- git diff --check PASS.
- Final root race suite inspected PASS, root 41.947s; /private/tmp/contexty-stage3-race.log.
- Final affected race run inspected PASS, root 1.934s; /private/tmp/contexty-stage3-extra.log (after equivalent makezero adjustment).
- Final pinned lint inspected PASS, 0 issues root/Redis/Postgres; /private/tmp/contexty-stage3-lint.log.
- Baseline and equivalent regression logs inspected: F03/F04/F05/F11/F12 review-SHA behavioral assertions PASS; /private/tmp/contexty-stage3-baseline.log and /private/tmp/contexty-stage3-regression.log.

Limits: acceptance covers stage 3, not stage 4 arithmetic/estimator/aggregate budget repairs or later stores/internal/doc gates. Passing tests and review do not establish absolute absence of defects or correctness of arbitrary host callback implementations.
