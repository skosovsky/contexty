# Stage 5 independent correctness acceptance

Verdict: PASS for the final frozen diff relative to 2a543b7. No confirmed unresolved correctness findings in F09/F10/D22/D28 and S5.1–S5.7 scope.

Reviewed production changes and regression/integration evidence:
- Redis uses quotient/remainder ceiling from nanosecond durations to milliseconds. Positive values cannot select persistence; zero remains explicit persistence, negative configuration panics. MaxDuration ceiling fits int64 and is below Lua exact integer range. Actual mutation passes the resulting positive decimal TTL to PEXPIRE, preserving persistent version keys, expiry revision consumption and ABA protection. Wire tests include 1ns,999999ns,1ms,1500us,MaxDuration; live PTTL probe alters only the Lua return expression and preserves mutation logic.
- Memory Load/Commit/Clear recheck ctx after acquiring their lock; mutation paths check immediately before publication. Observed cancellation while waiting cannot mutate payload/version. Checked deterministic lock-wait tests, the existing cancellation-after-codec publication guard, and single-mutex/non-reentrancy documentation. Keeping the simple reference-store mutex is an explicit D22 decision backed by local measurements, not a throughput promise.
- Postgres ClearState uses the internal nil-update sentinel to choose version-only SELECT FOR UPDATE, performs OCC comparison before any payload decode, encodes a standard empty checkpoint with intrinsic/default codec, advances the monotonic tombstone and publishes transactionally. Load/Commit retain their codec path. Missing-row first publication relies on unique-key conflict handling; stale/max-int/concurrent/atomic batch cases preserve the existing contracts. No remote payload decode or host codec callback is reachable in the clear branch.
- Store/migration docs describe selected semantics; published benchmark samples match their first recorded run and explicitly avoid production performance claims. Later samples vary under concurrent load, which does not invalidate the stated measured observation.

Independent checks executed:
- GOCACHE=/private/tmp/contexty-go-cache go test . -run TestRemediation_Memory -count=3: PASS (root0.691s).
- Redis module: go test ./... -run TestRemediation_TTLWireCeiling -count=3: PASS (0.702s).
- Postgres module: addressed integration run for invalid/retired payload clear, missing/stale clear and exhausted revision: PASS with live postgres16 container, no skips (3.416s), /private/tmp/contexty-stage5-independent-postgres.log.
- git diff --check: PASS.

Provided authoritative evidence inspected:
- docs/remediation-evidence/stage5/root-race.log, redis-race.log, postgres-race.log: PASS; real Redis standalone/cluster and Postgres containers executed without skipped integration tests. Live positive PTTL, atomic write permissions, missing/stale/recreate/clear/version-exhaustion cases passed.
- Root/Redis lint evidence and /private/tmp/contexty-stage5-postgres-lint.log: 0 issues in all three modules.
- docs/remediation-evidence/stage5/baseline.log: immutable review-SHA F09/F10 overlay behavioral probes executed and old defects observed; equivalent new regressions pass.
- docs/remediation-evidence/stage5/memory.log and memory-bench.log: deterministic cancellation/serialization tests and three parallel measurement samples verified.

Limits: acceptance covers stage 5 only. It does not prove absolute absence of errors, production latency, arbitrary host codec correctness, or completion of stage 6/final DoD.
