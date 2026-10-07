# Supported mapping

The semantic role set is closed: system, developer, user, assistant, tool.
`Role.Validate` returns `ErrInvalidRole` for empty or unknown roles. Compilation
validates inputs before host transforms, and outputs before publication, regardless
of output policy. Codecs, materialization, export and delta application use the
same contract. Developer is preserved; it is never implicitly mapped to system.
A role does not grant trust, permissions, or instruction precedence.

Instruction retention is explicit: messages outside history are fixed during
compile budgeting; history instructions are retained only when selected by the
host's `Retention.Roles` (include both system and developer if desired), IDs or
references. Overflow of required messages is an error, not silent eviction.

## Host mapping profile

The optional module `integration/chat` contains a real execution consumer. Core
has no dependency on that consumer. Inputs are ordered host records with stable
message IDs and native chat values; outputs retain the host record identity and
source metadata. Assembly order is system, history (including the current turn
once), tools, memory. This is a host choice, not provider instruction precedence.

| Field | Contract |
|---|---|
| Five roles, message ID, ordered parts | Exact preservation |
| Text | Exact preservation |
| URL image, MIME, inline media bytes | Exact preservation via typed MediaPart |
| Image detail | Explicit unsupported when the destination has no detail field |
| Tool call ID/name/arguments | Exact argument string in host codec; semantic JSON in AST, without float64 |
| Tool result text/name/CallID/IsError | Exact preservation |
| Structured error/progress/control, binary or JSON tool result | Unsupported unless a separate host mapping profile declares it |
| Nested multimodal tool result | Explicit unsupported; never flattened |
| Reasoning | Explicit unsupported; never converted to ordinary text |
| Part and message annotations, scoped metadata | Exact preservation in registered host extension/state codec |
| Provider bytes/scope/Required | Exact preservation in OpaqueState host payload |
| Provider expiry | Timestamp instant and offset preserved by JSON; Go location names and monotonic clock readings are not persisted |
| SourceRefs and native provenance/layer kind | Exact preservation in host records and codec; no inferred source authority |
| Native JSON metadata/annotation values | Semantic JSON preservation, including large integers |
| Core Actor/Origin/LLMCache/Provenance and unrecognized extensions | Unsupported in this profile |
| Native cache controls, argument chunks, unavailable continuation | Explicit unsupported |

Unsupported mapping returns the consumer's `ErrUnsupported`. No partial output
is returned. This is an executable subset, not a claim that native messages and
semantic AST nodes are interchangeable. Exact argument bytes are kept in a typed
host extension because JSON envelopes may normalize whitespace; the native request
restores them only when they still match the semantic arguments. Unsupported
image detail is detected before execution. Core's other media capabilities remain
available to hosts defining a broader profile.

## State and persistence

The host registers one defensive codec for metadata and one for opaque payloads.
Provider state and scoped annotations bind to the exact ordered context prefix
before their carrier through declared OpaqueBinding references. State metadata is
not ordinary text. The core checks codecs, placement, profile and dependencies;
the host checks provider destination, expiry and mandatory continuation at restore
and immediately before execution. Unknown codecs, invalid bytes and stale bindings
fail closed. Required state IDs are pinned by the host; dropping their carrier or
wrapper remains an execution error even under a permissive compile policy.

Save through ConversationCodec with the same registries/profile. Restore performs
core binding validation, then host continuation validation. Clone implementations
must own slices, JSON bytes, timestamps and nested metadata. Live provider replay
and signature verification are outside this offline recipe's evidence.

## Budget and history ownership

Core is the primary history selection/budget owner. The host uses no second
independent history truncation. Its final wire-byte report counts the actual native execution
representation, pins a digest and destination identity, and applies output/wire
reservations once. This offline byte budget is not a provider token budget;
unknown counting or stale reports fail. A provider tokenizer can replace the host
counter. Compile estimates and actual provider usage are distinct evidence.

Native projection validates semantic identity/provenance, declared bindings,
mandatory state, scope/expiry and the final report immediately before execution.
An altered projection requires a new preparation and report; a stale prepared
request is rejected. Hosts must not silently normalize, reorder or truncate it.

The recipe loads an immutable state, compiles a current turn exactly once and
accepts only a terminal complete response. Cancellation and incomplete streaming
produce no ordinary history commit. A single CAS delta appends the current turn
and terminal assistant message. Stable turn/result IDs make successful retries
idempotent; CAS conflicts preserve concurrent writes. Interrupted history needs a
separate explicit host decision. The example uses an in-memory store and offline
terminal response, not a durable backend or a live execution engine.
