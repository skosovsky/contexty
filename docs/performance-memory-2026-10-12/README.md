# Selection artifact performance acceptance

The implementation replaces repeated artifact enumeration with exact-ID lookup
in the snapshot's existing immutable map. Only matches are cloned into owned
candidates. No public API, dependency, materialization policy, persistence or
consumer changed. There is no new cache or index allocation during selection.

The fixed acceptance denominator is 22, in
[the ownership contract](../selection-artifact-ownership.md). Reviewer A verifies
completeness; Reviewer B independently searches for correctness counterexamples.
Their final verdicts are saved as `reviewer-a.md` and `reviewer-b.md` here.

## Identity and process

Baseline commit: `08b2101e8c05a99f1cf05b2979fdb778b25eed46`.
Final source identity and diff hashes: [identity.json](identity.json).
[source-files.json](source-files.json) covers every tracked baseline file plus
the new permanent regression/benchmark files and ownership contract.
The immutable review checkout is `/tmp/contexty-selection-candidate-20261012`.
Evidence and verdict metadata are outside the source hash to avoid recursive
hashes. [candidate.diff](candidate.diff) includes implementation, new tests and
contract, including files not yet tracked by Git. No Git commit or release was
created. Verification checks both the workspace and the frozen checkout.

Process attestation (Ann, this task's executing agent): the ownership contract,
fixed R01–R22 ledger and clone-bound regression were written in the first patch,
before editing `selection.go`. The new regression was run on an archive of the
baseline and failed on the multiplicative clones. The baseline benchmark matrix
completed before the implementation patch. The chat's ordered tool calls record
that sequence; there was no intermediate Git commit. The denominator and gates
were not changed during implementation or review.

## Reproduction and commands

All Go processes used `GOWORK=off`, writable
`GOCACHE=/tmp/library-performance-gocache-20261012` and the original audit's
exclusive advisory lock. Both trees used Go 1.27.1 on the same darwin/arm64 Apple
M1 Max host. Exact environment and fixture hash: [environment.json](environment.json).

From the library root, create the baseline using `git archive
08b2101e8c05a99f1cf05b2979fdb778b25eed46`, extract into
`/tmp/contexty-selection-baseline-20261012`, and copy both
`selection_artifact_*_test.go` files into that root. The original self-contained
audit bundle remains at
[.cursor/tasks/evidence/performance-memory-2026-10-12](../../.cursor/tasks/evidence/performance-memory-2026-10-12/report.md).

Use the **absolute** path to the root's
`.cursor/tasks/evidence/performance-memory-2026-10-12/audit-run.py` as `RUNNER`
when running from the archived baseline. The final commands, in execution order:

```sh
# cwd: /tmp/contexty-selection-baseline-20261012; expected exit 0
python3 "$RUNNER" go test -run '^$' -bench '^BenchmarkSelectionArtifact' -benchmem -benchtime=100ms -count=3 .
# cwd: library root; expected exit 0
python3 "$RUNNER" go test -run '^$' -bench '^BenchmarkSelectionArtifact' -benchmem -benchtime=100ms -count=3 .
python3 "$RUNNER" go test -v -run '^TestSelectionArtifact' .
python3 "$RUNNER" go test -race -count=1 ./...
GOLANGCI_LINT_CACHE=/tmp/contexty-selection-lint-cache python3 "$RUNNER" golangci-lint run --allow-serial-runners ./...
python3 "$RUNNER" golangci-lint fmt --diff
# cwd: /tmp/contexty-selection-baseline-20261012; expected exit 0
python3 "$RUNNER" go test -v -run '^TestSelectionArtifact(Owned|Accessor|Compile|Compatibility|Codec)' .
# cwd: /tmp/contexty-selection-baseline-20261012; expected exit 1 (red regression)
python3 "$RUNNER" go test -v -run '^TestSelectionArtifactCloneBound$' .
# cwd: library root; expected exit 0
python3 docs/performance-memory-2026-10-12/compare.py
python3 docs/performance-memory-2026-10-12/verify.py
git diff --check
```

Each invocation's command, lock acquisition/release timestamps and exit code
are recorded in its raw log. Redirect output to the corresponding file below.
The final benchmark, regression, full root race and lint runs use the frozen
source after all test formatting changes. No benchmark overlapped another Go
process from this task or either reviewer. Unrelated host workloads are not
controlled. No ns/op CI threshold is introduced.

## Evidence

| File | Meaning |
| --- | --- |
| [baseline-regression.log](baseline-regression.log) | Expected FAIL: target clone bound detects old N×A behavior |
| [baseline-compatibility.log](baseline-compatibility.log) | Baseline isolation, codec, error and golden-output tests PASS |
| [candidate-regression.log](candidate-regression.log) | All new permanent regression tests PASS, 24 scaling fixtures |
| [baseline-bench.log](baseline-bench.log) / [candidate-bench.log](candidate-bench.log) | Three benchmem samples per fixture, sequential |
| [comparison-gate.log](comparison-gate.log) | Allocation gates PASS |
| [comparison.md](comparison.md) / [comparison.json](comparison.json) | All 60 fixture medians and timing changes |
| [root-race.log](root-race.log) | Full root `./...` suite with fresh race tests; includes contract, selection, materialization, identity, budgets, replay, checkpoint and tooling |
| [root-lint.log](root-lint.log) / [format.log](format.log) | Root lint and configured formatting checks PASS |
| [source-verification.log](source-verification.log) | Final source identity and exit-code checks |

Private fixtures vary N=10/100/300 independently of A=0/10/100/300, matched and
unmatched. Public compile adds artifacts through the explicit deterministic
materializer and measures 0/1/4 targets for every N/A combination. Every fixture
and engine is built before ResetTimer; lookup, ownership copies, validation,
digests and target compilation are inside each measured call. Matched private
fixtures have M=min(N,A); the artifact-only extension counter excludes Message
ownership clones. Multiple candidates for one artifact are separately checked
for isolation. Unmatched fixtures make zero artifact clones, matched fixtures
make exactly M. This proves the lookup/copy component's bound together with the
single existing-map lookup in the implementation; it does not claim whole
compile linearity.

The golden output hash is
`5c7d45f2d2eda652d18aa568312e33d1b2334b096cae1bc8e9c0cb779778df52`
on baseline and candidate. It includes payloads, artifacts, selection refs and
priorities, lineage and all target outputs for 0/1/4 targets. Existing suites
add required refs, tool rounds, coverage, identity, budget and replay checks.
Adversarial tests cover candidate/callback/output mutation, 64 KiB JSON and
binary data, host extensions/codec, SourceRefs/OwnerRef/Budget, revised content
with the same artifact ID, previous snapshots, exact prefix matching and
invalid/canceled/failing compile.

Final medians (bytes are B/op, not live heap):

| Operation | N=A | baseline B/op | candidate B/op | Reduction |
| --- | ---: | ---: | ---: | ---: |
| Preparation, unmatched | 100 | 15,329,318 | 518,641 | 96.62% |
| Preparation, unmatched | 300 | 132,242,444 | 1,517,322 | 98.85% |
| CompileSnapshot, 0 targets | 100 | 52,860,941 | 23,193,313 | 56.12% |
| CompileSnapshot, 0 targets | 300 | 331,258,384 | 69,603,796 | 78.99% |
| CompileSnapshot, 4 targets | 300 | 1,648,215,280 | 340,267,136 | 79.36% |

Both allocation metrics decreased for all ten mandatory matched/unmatched and
0/1/4-target gates at N=A=100/300. No timing median increased by 5% or more in
this run; smaller differences are not treated as established regressions.
This observation is descriptive, not a new acceptance threshold.

The initial lint run found test formatting, complexity and JSON-tag issues;
they were corrected without suppressions or changing lint rules. Its cache
warnings came from the sandbox's read-only default lint cache; final lint used a
writable cache. `initial-lint.log` and `initial-format.log` are diagnostic logs,
not final gates. The media test initially lacked an explicit media token cost;
both baseline and candidate rejected that fixture. The corrected test uses a
FixedEstimator on main and targets. No library behavior was changed for it.

Affected integration modules: none. The production diff is confined to private
selection lookup; public compile results match the baseline and APIs/codecs
are unchanged. No external adapter/live-provider tests, release publication or
consumer pin updates were required or performed.

Allocation traffic is cumulative B/op, not retained heap, a leak or RSS.
Timing is descriptive: large artifact cases improve; small/no-artifact cases
can vary from host noise and constant lookup overhead. Three short samples
(some large fixtures execute once per sample) cannot establish a statistical
latency guarantee. Reviewers' absence of found bugs is not an absolute proof.
