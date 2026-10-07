# Offline native chat consumer

This optional module owns mapping between semantic context and actual native chat
messages. It has no provider SDK or network execution. Run from the repository root:

```sh
python3 scripts/check_chat.py --report /tmp/chat-source.json
python3 scripts/check_chat.py --peer ../prompty
python3 scripts/check_chat.py --baseline
GOPROXY=file:///path/to/candidate-proxy,https://proxy.golang.org python3 scripts/check_chat.py --candidate v0.13.1
python3 scripts/check_chat.py --published v0.13.1 --report /tmp/chat-published.json
```

The version shown for candidate/published is an example: select the exact candidate
or release version. `latest` is rejected. The runner copies this module to a
temporary directory with an isolated module cache, `GOWORK=off`, `GOENV=off`,
empty GOFLAGS and the exact registry Go toolchain. Host private-module settings
are overridden so public verification always uses the checksum database.
Source mode selects this checkout with one temporary core replacement. Its peer
is the exact public prompty v0.15.0 at commit
`f0db738d9ed9d1c8f739adbdfaeb7b5701f1d491`; the resolved download SHA and checksums
are verified. `--peer` explicitly checks a local peer checkout and records its SHA;
this development mode does not substitute for the required public-peer lane.

Candidate mode requires a file artifact proxy prepared from immutable release
bytes; only the candidate core namespace bypasses the public checksum database.
Published mode forces the public Go proxy and checksum database. Candidate and
published modes reject every replacement and unexpected selected core/peer
version. All modes download modules, verify their checksums, record the resolved
module graph when `--report` is supplied, then run fresh race tests, vet and the
executable recipe. Any download, graph validation or semantic failure exits nonzero. Individual
semantic test skips fail; command packages without test files remain valid.
Published downloads retry transient proxy/network failures at most eight times
with five-second intervals; checksum and semantic failures never retry.

`--baseline` selects public v0.12.0 and checks only the older supported subset.
Its result is explicitly marked baseline and never counts as full acceptance of
a candidate's role contract. The full local/CI gate owns source, candidate and
baseline lanes separately; release verification checks the exact new public tag.

`Mapper.Import` accepts stable host records and returns semantic messages plus
mandatory continuation IDs. Persist both in the host; after truncation the list
must still come from the accepted source, not be recomputed from surviving state.
`Mapper.Export` fails if a mandatory state disappeared. It verifies core bindings
and native continuation scope/expiry. Unknown data is rejected explicitly.

The profile supports five roles, text, inline media and URL images, complete JSON
tool calls and single-text tool results. Reasoning, composite media tool results,
image detail, cache controls, streaming argument chunks and unrecognized core
metadata fail. The typed metadata extension keeps native part hints, annotations,
provenance and JSON metadata. The opaque codec carries provider bytes and scoped
annotations. Native JSON metadata is semantically preserved (including large
integers); argument strings and provider payload bytes are exact. No raw state is
rendered as text. See [SupportedMapping](../../docs/supported-mapping.md).

`CompileTurn` uses a traced identity output policy and fail-closed state policy.
`Prepare` checks semantic revisions, projects the final native execution and
produces a report in **wire bytes**, not estimated provider tokens. `ValidatePrepared`
checks source/provenance identity, state gates and the actual execution digest
immediately before use. Do not apply independent native normalization/truncation.
A provider-specific host can substitute its tokenizer and broader mapping profile.

`CommitTerminal` requires an explicit terminal flag and completed native outcome.
Only current user turn and terminal assistant response are appended through one
CAS commit. The example is idempotent after a successful retry and preserves
concurrent writes on conflict. Bind terminal continuation to the committed prefix.
The host remains responsible for authenticating terminal events and persisting
mandatory IDs/codec descriptors. Usage is independent of the final request report.

`cmd/recipe` executes the full offline path using an in-memory codec-backed store.
These tests do not prove live provider replay, signature validity, token estimates
or production durability.
