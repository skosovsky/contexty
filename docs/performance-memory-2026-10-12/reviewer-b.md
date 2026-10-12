# Reviewer B — independent correctness acceptance

Verdict: **APPROVE**. Найденных неисправленных correctness bugs: **0**.

Приёмка выполнена после сообщения root `FINAL LOGS READY`. Отчёт Reviewer A не
читался; findings с ним не обсуждались. Производственные файлы и постоянные тесты
не изменялись этим ревьюером.

## Reviewed identity

- Baseline: `08b2101e8c05a99f1cf05b2979fdb778b25eed46`.
- Frozen source tree: `73a9410a2beefbb89d92a778b32086de8c9c453513d967237eb8dcc3b4c905e6`.
- Diff: `8e550ccf9da7a77e5d1a818043246fccce762567a55306f188e9996a536942a8`.
- Shared final evidence manifest: `final-evidence.json`, SHA256
  `494bf1d65b6f9e9d29ecf0d0c593826543f40641ad847cc1e679850e9c692da3`.

Независимо сверены все 497 source hashes в frozen checkout и workspace, все
19 общих evidence hashes. Обе стороны benchmark используют один fixture hash,
Go 1.27.1, darwin/arm64 и Apple M1 Max.

## Correctness analysis

| Area | Result and evidence |
| --- | --- |
| Ownership | PASS: match cloned before entering ContextCandidate; Payload, Blob, host Extensions, OwnerRef/SourceRefs and Budget copied by unchanged ContextArtifact.Clone. cloneCandidates adds callback ownership boundary. Large JSON/binary mutation tests and own concurrent mutation probe pass. |
| Indexed identity | PASS: all production map constructors, updates and codec decode maintain map key == artifact.ID. Exactly one `artifact:` prefix is stripped; map lookup is equivalent to former full-string comparison. Nested prefix, whitespace, Unicode, NUL, empty ID and zero snapshot checked independently against literal former attachment logic. Invalid public input remains validated by unchanged compile path. |
| Stale refs / revisions | PASS: ArtifactContentRef is still computed from the owned match. WithArtifact creates a new map; prior snapshot never changes. Permanent revision regression plus concurrent revision probe pass. |
| Priority / required refs / rounds / order | PASS: preparation order, ordinals and message members unchanged; attachment changes only lookup and removes redundant copies. Full existing contract/adversarial selection suites pass in root race run. Baseline and candidate compatibility golden agree for main and 0/1/4 targets. |
| Budget / coverage / lineage | PASS: admission and downstream evidence paths unchanged. Artifact-local budget sees the same owned artifact and exact ref. Existing budget, identity, coverage, replay and checkpoint suites pass in root race run. |
| Materialization / typed media | PASS: neither callback invocation nor representation cache modified. Explicit materializer public fixtures, host mutation tests, typed media tests and existing materialization suites pass; no additional cache introduced. |
| BYOT clone / codec | PASS: selected artifact still uses CloneExtension, callback gets another clone, codec remains unchanged. Artifact and message host extension round-trip/mutation regressions pass. Removed redundant internal clone counts are explicitly excluded from compatibility contract. |
| Cancellation / errors | PASS: compile validation and cancellation checks unchanged. Empty, invalid budget, pre-canceled context, host error and host error with cancellation regressions pass on baseline and candidate. |
| Concurrency | PASS: borrowed map is read-only, immutable snapshot update makes another map, no host callback receives internal mutable payload. Root race suite and 8 workers × 20 iterations independent snapshot/candidate mutation race probe pass. |

## Independent probes and benchmark review

Own probes run in a separate `/tmp/contexty-reviewer-b-20261012` copy of the frozen
candidate. Exact command and full probe source are preserved in
[reviewer-b-probes.log](reviewer-b-probes.log):

```sh
python3 "$WORKSPACE/.cursor/tasks/evidence/performance-memory-2026-10-12/audit-run.py" go test -race -count=1 -run '^TestReviewerB' .
```

Both independent tests PASS. Probe work ran after benchmark lock release, through
the required absolute runner; no benchmark was run by this reviewer.

Benchmark setup and engine construction precede ResetTimer. Actual selection,
lookup, ownership copies, digest computation, compile validation and target
processing remain timed. Reusing the snapshot’s already-owned ID map is the
implementation, rather than moving a new per-call index into fixture setup.
Private preparation isolates matched/unmatched attachment; public compile uses
explicit materialization and 0/1/4 targets. An independent read-only parser of the
final raw logs verified 60 fixtures with exactly 3 samples per tree and all
10 N=A=100/300 B/op and allocs/op decrease gates. The source has one indexed
lookup per candidate and one artifact clone per match, removing N×A artifact
copy/traversal. This does not assert linear complexity for full compile.

Final [root-race.log](root-race.log), [root-lint.log](root-lint.log),
[format.log](format.log), regressions, baseline golden compatibility and
comparison gates PASS. The baseline clone regression is intentionally FAIL,
confirming that it detects the original multiplicative behavior.

## Limits

Review establishes no found unfixed bugs in the frozen candidate, not an
absolute proof. Host BYOT implementations must honor their own deep-clone and
concurrency contracts. The immutable map invariant applies to states constructed
through supported APIs; deliberate private-field corruption is outside that
contract. Three short benchmark samples, sometimes one invocation for a large
public fixture, support allocation gates but no statistical latency guarantee.
B/op is allocation traffic, not retained heap or leak evidence. External consumer
modules and live providers were not changed or exercised. Future source changes
require renewed acceptance of affected requirements.
