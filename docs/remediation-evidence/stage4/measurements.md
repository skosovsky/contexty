# Stage 4 measurements

Host: macOS/arm64, Go1.27.1. Before:80fb346, after:accepted stage4 diff.
Both complete make bench-guardrails runs passed; logs retained alongside reports.
Truncate observed513→514allocs/op,310959→312747B/op; one owned weight copy
replaces borrowed host weights. Rune counting no longer allocates a rune slice.
Observed timings410018→118923ns/op are individual local runs, with possible host
load variation, not a general performance guarantee. No thresholds were relaxed.
The final race suite also executes allocation guardrail tests.

Deterministic fuzz seeds include0/negative/MaxInt/near-MaxInt sums and MaxInt
character divisor. Two10s campaigns passed323296 and296978 executions.
Pointer/value/codec/IDs use explicit edge cases from stage3; final DoD retains
further concurrency, adapter integration and related checks.
