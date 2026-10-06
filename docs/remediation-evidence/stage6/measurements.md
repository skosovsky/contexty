# Local private-boundary measurements

Baseline: stage5 4cb73bb plus the unchanged new benchmark fixture. After: stage6
private append transfer and selection segment aggregation plus compileSession.
Go1.27.1, darwin/arm64, Apple M1 Max, GOMAXPROCS default10. Command:

```
GOCACHE=/private/tmp/contexty-go-cache go test -run '^$' -bench '^BenchmarkRemediation_(Append|CompileSnapshots)$' -benchmem -benchtime=100ms -count=1
```

| Fixture | Baseline allocs/op | After allocs/op | Baseline B/op | After B/op |
| --- | ---: | ---: | ---: | ---: |
| Append10 | 71 | 27 | 9520 | 4240 |
| Append100 | 611 | 207 | 92145 | 36561 |
| Append1000 | 6011 | 2007 | 784755 | 343901 |
| Compile history10 targets0/1/4 | 3552/7244/18322 | 3336/6807/17227 | 325934/662029/1672227 | 295267/600280/1518385 |
| Compile history100 targets0/1/4 | 51134/103600/261000 | 30798/62905/159250 | 6329486/12697325/31802827 | 2821160/5677571/14251144 |
| Compile history1000 targets0/1/4 | 2311672/4636199/11609632 | 308598/630175/1594755 | 343768840/687868176/1720130000 | 28902548/58170296/145916664 |

Original profile (history100/target1,500ms) attributes79.02% of allocated bytes
cumulatively to selectOutput: appending each message cloned the accumulated
segment repeatedly. Stage6 aggregates privately and crosses the public snapshot
clone boundary once per segment. Append drops repeated private clones while
copying old/new messages once. Public input/getter mutation tests and concurrent
main/target reuse exercise ownership after these changes.

CPU samples were dominated by runtime/GC/system scheduling; Message.Clone accounts
for5.69% cumulative samples. This small profile gives no basis for assuming host
callbacks pure or introducing callback/address caches. Digest/report/estimate
checks, strict codec roundtrips and policy snapshots remain. D14/D31 retain those
checks; D23/D37 optimize measured copy amplification only. Timings are single local
samples under variable system load; no latency or throughput promise is made.

Logs: baseline-bench.log, after-bench.log, allocation-profile.txt, cpu-profile.txt,
profile-run.log. Profiles were written to /private/tmp; summaries are retained here.

Addressed fuzz commands (`go test -tags=fuzz -run '^$' -fuzz NAME -fuzztime=10s .`):
FuzzRemediationEventIdentity:167864 executions PASS; FuzzRemediationPartsAndCodec:
325332 executions PASS. Seeds include delimiters/NUL/max ordinal, invalid UTF-8,
empty/Unicode parts, invalid schema/null/truncated JSON. Stage4 arithmetic fuzz
seeds and runs remain in `../stage4`; no sample is treated as exhaustive proof.


## D14/D31 evidence operations

Targeted current-code measurements explicitly execute digest, raw estimate,
EstimateReporter.Report and semantic codec roundtrip on the same10/100 messages,
plus LabelProjection.Project with a required registered host label. Command:
`go test -run '^$' -bench '^BenchmarkRemediation_(EvidenceComponents|LabelRoundtrip)$' -benchmem -benchtime=100ms .`.
These components were not changed; no before/after improvement is claimed.

| Operation | Input messages | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: |
| MessageContentRef for each message | 10 | 57915 | 32811 | 630 |
| Raw CharTokenEstimator.Estimate | 10 | 400 | 80 | 1 |
| EstimateReporter.Report | 10 | 330280 | 182319 | 3256 |
| JSONSerializer Marshal/Unmarshal for each | 10 | 26358 | 10822 | 190 |
| MessageContentRef for each message | 100 | 578930 | 328186 | 6356 |
| Raw CharTokenEstimator.Estimate | 100 | 3726 | 896 | 1 |
| EstimateReporter.Report | 100 | 2364748 | 1497782 | 24989 |
| JSONSerializer Marshal/Unmarshal for each | 100 | 258840 | 108259 | 1947 |
| LabelProjection.Project with strict input/output codec checks | 1 | 12344 | 8748 | 184 |

Report and digest costs are material compared with the text approximation; this
measures their actual paths rather than inferring them from clone profiles.
Different accepted content/profile/output requires independent evidence. A host
callback has no purity promise, so these samples do not justify memoizing its
result across boundaries. An owned validated estimate snapshot is deferred until
profiling a concrete host workload proves a safe same-input reuse opportunity.
Strict label roundtrips cost184allocs in this small fixture; they protect required
metadata and remain mandatory. No arbitrary host codec cost/production count is
inferred. See evidence-components.log for the full samples.
