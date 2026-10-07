# Host-owned opaque state

`OpaqueState` is one envelope over `Message.Extensions`. It stores a host-owned
`Extension` payload, an explicit `Codec` descriptor, placement among content parts,
and context bindings. It is not text, a tool result, a media item, or a local
`CompactionRecord`. The library does not interpret, decrypt, or generate payload
bytes. Host adapters own signatures, provider protocol rules and wire encoding.

Register the payload decoder with `ExtensionRegistry.RegisterOpaquePayload` using
the same descriptor as `OpaqueState.Codec`. Ordinary extension registration does
not establish this binding. Missing or mismatched codecs fail; no raw fallback is
available. Envelope encoding includes the host type discriminator, placement and
bindings. Clone, identity, capture and storage preserve those fields and extension
order. Payload implementations must provide defensive `CloneExtension` copies.

`Placement.AfterPart == -1` places state before the first content part; otherwise
it names an existing part index. States sharing that position retain their
`Extensions` order. Placement cannot be rebound by a transform.

`Binding.Required` names exact message revisions, excluding the carrier itself.
`Binding.Prefix` optionally specifies the exact ordered context beginning through
`Binding.Boundary`; that message must precede the carrier. Inserts, reorders,
mutation or truncation inside this scope invalidate the state. Changes after the
boundary do not invalidate it unless separately named in `Required`. Core does
not infer dependency rules from a vendor name or payload bytes. The carrier
body itself is outside these references: a full self-reference would be cyclic.
Hosts place signed content in a separate referenced message when its revision
must be protected. Placement alone does not assert a signature over carrier text.

An engine receiving state requires `WithOpaqueStatePolicy`, which pins the
receiving profile and invalidation policy. Compilation without opaque envelopes
needs no state policy.
The default `OpaqueFailClosed` rejects invalid or removed state. Explicit
`OpaqueDropInvalid` permits dropping states whose declared dependencies no longer
hold, including a carrier omitted by selection. Drops are recorded with exact
carrier revisions and state IDs; no payload repair occurs. Dropping one state may
change a revision required by another: validation continues until the remaining
state is valid. Unknown codecs, malformed envelopes, incompatible profiles and
attempts to mutate bytes, binding or placement remain errors in drop mode.

Validation runs after all ordinary transforms and the final host output policy,
before final estimates and output identity. Output policy may transform ordinary
context but cannot rewrite an existing opaque envelope. Main and named outputs
are checked independently. Accepted replay restores saved state and bindings
through registered codecs without rerunning selection, materialization or output
policies; policy/profile/encoding identity is evidence.
Durable persistence and checkpoints validate the entire selected conversation,
with explicit codec and profile, rather than checking each segment in isolation.

Plain-text renderers omit opaque payloads. Export also omits state by default,
even if a generic extension allowlist includes its wrapper. Explicit state IDs,
a compatible consumer profile and all exact dependencies in the actual exported
representation are required. Export does not disclose raw dependencies to repair
an incompatible selection. Estimation requires an adapter declaring state cost,
or follows the configured unknown-cost contract; byte length is not a token cost.

See `examples/opaque_state` for two independent host payload types, a signature
fixture and an external compaction item. These are local protocol fixtures;
provider SDKs and remote compaction requests remain outside core.

The [optional native consumer](../integration/chat/README.md) registers host codecs
for exact provider bytes and scoped annotations, while preserving native JSON
metadata semantically. It checks native destination/expiry after persistence and
immediately before execution. Mandatory state IDs are host-owned accepted-source
evidence: persist that list separately and never reconstruct it from a truncated
projection. Core's drop mode alone does not satisfy a mandatory continuation.
