# Output policy contract

`OutputPolicy` is an optional host callback at the final semantic output boundary.
Its descriptor identifies the host's implementation and revision. A configured
policy with no callback or an invalid descriptor fails compilation. No configured
policy means no sanitization is implied.

The host receives an owned `OutputPolicyInput`: output kind, output name and all
four semantic payload segments. This includes selected artifacts, tool arguments
and results, memory and the compiled current turn. It may reject the output or
return transformed typed content. Core does not detect secrets, infer trust from
roles, classify prompt injections or authorize tool execution.

Message IDs and order must remain unchanged within every segment. Tool call and
result parts retain their positions, counts, IDs, names and result error status; tool-bearing messages
also retain their roles. Their typed JSON, text and binary payloads may change.
Other messages may use any known provider role. The policy cannot select, omit,
insert or move messages. Invalid JSON or nil content parts fail before acceptance.
Host extensions remain typed values and require the same codec contract as other
compiled messages; the policy does not convert them into text.

The engine runs the callback after ordinary content transformations, pending turn
insertion and target formatting, before final budget, identity and tool round
checks. Text views render the accepted semantic content. Every output applies the
policy independently. Cancellation is checked before and after host execution.
Arguments and accepted results are defensive copies.

Prompt changes do not rewrite source messages, raw current turns or other targets.
Current-turn persistence and record capture remain explicit independent choices;
a redacted prompt does not imply that private raw data was excluded from records.
Accepted records preserve the transformed output and its evidence. Replay does
not execute the callback again. Changes invalidate estimates and prefix evidence
that depend on previous content; final checks use accepted content.

## Explicit artifact materialization

`ArtifactMaterializationPolicy` has a pinned `Identity` and a `Materialize`
callback returning `ArtifactRepresentation{Role, Parts}` from an owned artifact.
Configure it using `WithArtifactMaterialization`; artifact-bearing compilation
without a policy fails with `ErrMissingArtifactMaterialization`. No hidden role
is chosen for retrieval, memory or previews. Direct caller messages remain valid
without this policy. Invalid roles/parts return `ErrInvalidArtifactMaterialization`.
Artifact materialization cannot introduce tool calls/results. It retains the
artifact identity, source refs and typed extensions; roles confer no permissions.

`ArtifactContentParts` exposes typed text/media payloads without assigning a role;
a host can use it inside its explicit policy. Hosts may instead choose a different
typed representation. Counters estimate that actual materialized message, not the
canonical artifact body or an assumed system message. `ResourceResolver` requires
its own `Materialization` configured with the same pinned identity as the engine.
Resolution, append and blob preview compilation share this representation contract.
Policy decisions bind exact artifact/message refs in lineage and the manifest.
Replay validates evidence without calling the materializer again.

## Required content and accepted revisions

Core validates exact retention and selected mandatory units before the final
output policy. This prevents earlier hooks, formatters or patches from silently
replacing required revisions. The configured final policy may explicitly project
those revisions while preserving IDs/order/tool structure. Its decision maps
pre-boundary refs to accepted refs; mandatory checks and final recount then use
that mapping. An expanding projection can fail the final budget. No automatic
recount evidence from older bytes is reused for the accepted output.

Generic `TransformHook` remains an ordinary stage. The built-in `RedactionHook`
and `NewRedactionHook` are removed; they were not a complete output boundary.
No built-in PII detector, injection classifier or trust lattice is supplied.

## Disclosure and durable state

Output policy changes only the issued semantic prompt. Source/raw current turn,
canonical artifact payloads, checkpoint decisions and raw record capture remain
separate host-owned contracts. Accepted replay returns accepted messages without
rerunning the callback. Export message selection refers to those accepted messages.
Exporting canonical artifact payloads is a separate disclosure choice: a sanitized
materialized message does not sanitize its backing artifact body. Approve that
body only after independent host review; use message-only export for the accepted
prompt representation. Artifact metadata allowlists do not transform body bytes.

`ExportSelection.ArtifactPayloadRefs` explicitly approves exact canonical artifact
revisions from `ArtifactContentRef`, independently of `MessageIDs` selecting accepted
prompt messages. This replaces export selection `ArtifactIDs`; projection
`ArtifactIDs` remains participation evidence. Stale, malformed or duplicate payload
refs fail export. A payload ref grants disclosure of the original canonical typed
body, not the output-policy representation; select only messages when handing off
the accepted prompt. Neither sanitization nor metadata allowlisting rewrites that
canonical body.
