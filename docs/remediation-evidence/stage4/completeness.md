# Stage 4 independent completeness acceptance

Base: 80fb346. Reviewer: stage1_completeness. Repository inspected read-only.
Final diff includes nonmonotonic pending/candidate repairs, empty-request count,
owned custom eviction boundary, final candidate recount gate, and explicit
atomicity parity/common round validation. The final budgetCandidateUnit helper
extraction preserves the checked branch order; additional nontext overflow and
unsupported binary regressions are included.

Result: **9/9 fully satisfied = 100%, PASS**. No unresolved completeness gaps.
The pending-only and required-only admission gaps and final candidate recount
missing gate identified during acceptance have been resolved and covered by AAA
regressions. This report does not accept stages 5–6 or assert absence of all bugs.

| Criterion | Status | Evidence |
| --- | --- | --- |
| S4.1 | PASS | Constructor no longer inserts CharTokenEstimator. validateBudgetPolicy rejects nil/typed-nil estimators and invalid built-in/drop-head configuration, including custom-configured built-in strategy and typed-nil ports. Both compile entrypoints call budget preflight before deferred/store effects; standalone Apply validates before callbacks. TestRemediation_BudgetExplicitEstimator and BudgetInvalidOptionalConfiguration exercise invalid inputs and no resolver calls. |
| S4.2 | PASS | estimateOwned owns host arguments, checks cancellation before/after callbacks and nonnegative total. DropHead clones estimator weights before use and checks sum/shape. CharFallback EstimateTool gets cloned payload and cancellation recheck. Custom strategy inputs are owned and returned outputs go through ownCompileMessages. BudgetMutatingWeights plus existing ownership/final-cancellation/strategy tests protect retained and caller data; intrinsic read-only CharTokenEstimator borrowing remains explicit. |
| S4.3 | PASS | estimate_arithmetic.go checked sum/multiplication; fixed estimator checks message/part/tool weights and products. Character paths use checked component sums and quotient/remainder ceil. Negative/overflow/inconsistent returned weights fail before subtraction/suffix algorithms. EstimateReporter already clones weights and validates checked aggregate equality. BudgetArithmetic/InvalidWeights, existing tool/fallback edge cases and arithmetic fuzz seeds prevent false admission. Errors reuse ErrInconsistentEstimate and existing count wrapping. |
| S4.4 | PASS | applyBudgetHistory constructs complete FlattenMessages with fixed system/tools/memory and active pending/current protected by fixedIDs. Initial full cost determines admission; pending-only cost now only classifies an already proven failure. Fitting overhead fixture retains history with and without active inputs. NonmonotonicCandidates covers standalone-expensive subsets but fitting full request; complete eviction runs before required-subset cost rejection. Independent repro /private/tmp/contexty-stage4-pending-repro.go now returns full-cost=2 limit=2 error=nil history=1 (previously ErrPendingExceedsBudget). Explicit soft compaction remains separate documented behavior. |
| S4.5 | PASS | Built-in eviction measures each complete retained candidate for nonintrinsic counters, retains required/fixed IDs, checks final recount against HardLimit and performs common tool-round validation before return. Intrinsic counters retain validated additive optimization; host custom strategies remain optional-content ports with final composed aggregate admission. BudgetCandidateFinalEstimate checks changing callback cost, BudgetAtomicityPolicyParity checks explicit atomicity false across intrinsic/host paths. Existing main/target/view final admission regressions pass root suite. |
| S4.6 | PASS | Negative MinMessages is no longer silently normalized, and preflight/direct DropHead reject it. options.go GoDoc and retention-budget.md define minimum optional-block threshold, zero defaults and explicit RetentionPolicy protection. Existing retention test checks MinMessages may remove optional remainder but never protected content. |
| S4.7 | PASS | Character estimators reject unsupported explicit media/binary tool payloads, including pointer-normalized parts. FixedEstimator is explicitly structural approximation. retention-budget.md, remediation baseline and existing estimate evidence docs distinguish approximation/declared quality, strict admission and positive unknown fallback from measured/provider-exact cost. No provider dependency added. |
| S4.8 | PASS | reproduce_review.py executes preserved budget/overhead probes against archived immutable review SHA. stage4-baseline.log reproduces F06 mutation, F07 false/negative counts and F08 fitting-history loss and reports assertions PASS. Equivalent AAA budget regressions and deterministic MaxInt/negative arithmetic fuzz seeds pass; both 10s fuzz jobs also pass (323296 arithmetic and 296978 character executions). |
| S4.9 | PASS | Final stage4-race.log root 50.281s plus all packages/examples PASS; stage4-lint.log 0 issues all three modules. Final affected-case race in stage4-extra.log 1.510s PASS after final equivalent helper extraction and added nontext/binary cases. Guardrail before/after logs PASS; truncate 513→514 allocs/op documents measured +1 allocation, without unsupported speed claims. Migration and retention docs reflect complete-request decisions/soft percentages, explicit estimator and validation semantics; existing callers already use explicit estimators and examples compile in root suite. This independent completeness acceptance is PASS; separate correctness acceptance is required before commit. |

Inspected logs: stage4-race, lint, baseline, regression, fuzz-seeds,
fuzz-arithmetic, fuzz-character, bench-before, bench-after, extra under /private/tmp.
Full criterion coverage is acceptance evidence, not exhaustive proof of every
host callback combination or performance improvement.
