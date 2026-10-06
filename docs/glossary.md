# Context boundaries and identity

| Term | Meaning |
| --- | --- |
| Event identity / Message.ID | Host logical event, stable across history trimming and retries. CurrentTurn/Pending generated IDs bind TurnID, event kind and turn ordinal. Static/history content requires explicit IDs or a host policy. |
| Content revision / ContentRef | Exact typed content digest plus occurrence; a new content revision does not invent a new logical event. |
| Observer ID / fingerprint | Diagnostic identity only; neither anonymization nor safe low-cardinality metric label. Explicit IDs may contain host data. |
| Source | Owned normalized input before prompt transforms. Raw capture and prompt OutputPolicy have distinct scopes. |
| Prepared | Shared resolved/transformed candidates before each output's independent selection/admission. |
| Prompt | Accepted main or target projection supplied to a model; contexty does not execute that model. |
| Checkpoint | Persistence projection explicitly committed by the host using OCC; may retain raw content omitted from prompt. |
| MessageIDs / ArtifactIDs | Different delta namespaces; remove-artifact uses artifact_ids and rejects message_ids. |
| RenderView | Registered snapshot renderer; skips compile/deferred resolution. Named views may apply configured budget, formatter and role projection, invoking host callbacks. |
| CompileTarget.View | Built-in output format after target compile; named RenderView registry is not consulted. |
| Immutable state | ConversationState owns private data and structurally shares unchanged state; getters return deep copies. |
| Owned DTO | Public slices/maps are mutable data owned by the receiving caller; they are not immutable wrappers. |
| Descriptor | Versioned component identity (ID + Revision). ResourceDescriptor describes resource content/locators; they are different types and scopes. |
| Estimate quality | Capability/coverage evidence; a structural approximation or unknown-quality fallback is not measured provider usage. |
| Prefix evidence | Semantic binding of a prepared prefix; no remote cache hit, pricing or access claim. |

ExportProjection is synchronous and does not promise cancellation. Its standalone
codec validation uses background context. A context-bearing export API is deferred
until a host demonstrates a need; no worker or retry abstraction is added.

RetireSource globally retires an exact ContentRef including Occurrence. Use globally
unique tenant source IDs or separate stores. CheckBlob confirms availability and
binding, not active claim retention; hosts keep claims active during use. Reference
memory blob stores retain identity tombstones/claims and have no automatic TTL GC.
Extension allowlists admit whole payloads; per-field projection belongs to the host.
LabelDecision.Upgrade is a host assertion, because the core cannot interpret BYOT
labels. Strict codec roundtrip checks preserve metadata at projection boundaries.

An interrupted tool result is a typed unknown-outcome marker. Its IsError=false
presentation does not assert successful execution; the host maps it to a provider
format and owns retry/recovery/permissions. Empty delta operation is an explicit
no-op; a committed nonempty batch still consumes a new OCC revision.

ConversationCodec is a semantic roundtrip, not persistence admission: negative
Version and unknown artifact lifecycle survive both directions. Stores reject
invalid/exhausted OCC transitions; compile/persistence policies check lifecycle.
Do not decode arbitrary archival bytes and treat them as an accepted checkpoint.
