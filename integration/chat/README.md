# Offline native chat consumer

This optional module owns mapping between semantic context and actual native chat
messages. It has no provider SDK or network execution. Run from the repository root:

```sh
python3 scripts/check_chat.py
python3 scripts/check_chat.py --peer ../prompty
python3 scripts/check_chat.py --published latest
```

The runner copies this module to a temporary directory. Local mode adds temporary
core/peer replacements; published mode resolves core from its public tag with
`GOWORK=off` and no replacements. The checked-in dependency is the preceding
published baseline; the published runner explicitly selects the requested release.
CI checks a supported peer tag, because the peer default branch can lag its
published API. For coordinated development, `--peer` tests the actual local
checkout explicitly; no peer revision is chosen by core.
Before the new role contract is published, use
`--published v0.12.0 --baseline` to verify only the older supported subset. That
result is explicitly marked baseline and never counts as full acceptance.

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
