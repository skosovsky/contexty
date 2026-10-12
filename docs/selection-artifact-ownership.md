# Selection artifact preparation

`selectionCandidates` borrows the snapshot's private immutable artifact index for
exact `artifact:` message-ID lookup. It must not enumerate, sort or copy the full
artifact set for each candidate. The existing index belongs to the snapshot;
selection adds no cache, global state or lifetime beyond the invocation.

For N candidates, A artifacts and M matches, lookup work is O(N), within the
O(A+N+M) bound including construction of the snapshot index. Artifact payload
copies during this stage are limited to M: unmatched candidates copy no artifact
payloads or extensions. Matched artifacts are cloned into each owned candidate
before computing its artifact ContentRef. Message ownership copies and digest
work remain separate; neither total compile time nor total compile allocations
are claimed to be linear.

Selection callbacks receive another owned candidate copy. They may mutate
payloads, extensions, source references, budgets and messages without affecting
the snapshot, prepared candidates, another output or a previous snapshot.
BYOT extensions retain their CloneExtension and codec boundaries; redundant
internal clone counts are not a public compatibility guarantee.

The public `Artifacts()` accessor still returns independently owned copies in
deterministic ID order. Materialization, priorities, required refs, tool rounds,
chronology, coverage, lineage, cancellation and error contracts are unchanged.

## Acceptance ledger (fixed before implementation)

Each row is one mandatory gate. PASS counts only complete execution; FAIL,
NOT RUN and partial results count zero. Completeness is 100 × PASS / 22.

| ID | Mandatory requirement |
| --- | --- |
| R01 | Private ownership/complexity contract documented before implementation |
| R02 | Scope confined to selection, tests and evidence; APIs/dependencies/persistence unchanged |
| R03 | No per-candidate full artifact traversal/copy; bounded exact-ID indexed lookup |
| R04 | Unmatched clone upper bound <= A, including N=A=100 and 300 |
| R05 | Matched clones separated from message ownership; full-set copies independent of N |
| R06 | Independent N=10/100/300, A=0/10/100/300 matched/unmatched scaling fixtures |
| R07 | Artifacts accessor deterministic and independently owned |
| R08 | Candidate and selection callback mutations cannot change snapshots/other candidates |
| R09 | Compile callback/output mutations cannot change source/sibling outputs or later compiles |
| R10 | Large JSON/binary, Extensions, SourceRefs and Budget isolation |
| R11 | Same artifact ID with new revision yields new ContentRef; old snapshot unchanged |
| R12 | Exact artifact message IDs, ContentRefs and pre-change output compatibility |
| R13 | Priority, required refs, rounds, order, coverage and lineage suites PASS |
| R14 | Materialization invocation contract and typed media suites PASS |
| R15 | Empty/unmatched artifacts and invalid/canceled/error compilation tested |
| R16 | BYOT clone/codec suites PASS; no global/long-lived cache added |
| R17 | >=3 sequential baseline/candidate benchmem samples, same host/toolchain |
| R18 | Internal and public CompileSnapshot benchmarks, explicit materializer, 0/1/4 targets |
| R19 | Setup outside timer; all invocation work inside; independent N/A benchmark matrix |
| R20 | N=A=100/300 B/op and allocs/op decrease; linear lookup/copy evidence; timing discussed |
| R21 | Full root race/contracts and existing identity/budget/replay/checkpoint suites PASS |
| R22 | Exact commands, source/tree identity, raw logs and two independent final approvals |
