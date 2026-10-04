# Checkpoint store contract

`ConversationStateStore.CommitState(ctx, id, expectedVersion, deltas...)` is the
canonical batch write. `ApplyDelta` remains a pure state transition, never a store
method. All deltas are applied in order to one loaded state; the persistence
projection and codec must succeed before publishing anything. One successful
batch consumes one OCC revision, including an explicit no-op delta. An empty
batch returns `ErrEmptyCheckpointCommit` without consuming a revision. Concurrent
writers with the same token produce one success and one conflict. `ClearState`
keeps its payload-free, monotonic tombstone contract.

`ProjectCheckpoint` is the explicit pure persistence boundary. Artifact policy
Store retains all supported lifecycles; Skip retains none; Default retains only
Persistent (or the unspecified lifecycle). TurnBound and Ephemeral remain valid
in working state. Invalid lifecycle/persistence values are errors. Projection
owns the resulting data and does not mutate its input. All supplied stores use
this projection before encoding. Memory also validates through its configured
codec, so missing host codecs cannot silently create a different durable contract.

`ConversationCodec` and `ConversationStateCodec.EncodeState` are lossless semantic
codecs, including transient artifacts. Their envelope has a dedicated
`contexty/conversation/1` schema identifier; missing or unsupported schema is an
error. OCC revision is independent of this format. Durable application callers
must not supply duplicate artifact IDs in wire data; decoding rejects them rather
than merging or overwriting revisions. Artifact merges belong to state transitions.
Durable application callers
must use `ProjectCheckpoint` explicitly when encoding checkpoints themselves.

Redis uses a new checkpoint namespace. Namespace and conversation ID are encoded
as hex; a nonempty conversation-specific hash tag binds revision/payload keys to
one Cluster slot. Braces, delimiters, Unicode and empty IDs cannot inject tags.
There is no legacy dual read. Revision markers never expire and must never be
deleted separately. Payload expiry/clear retain monotonic OCC semantics.
Redis 7+ is required for Lua ACL preflight. Publication uses a single MSET for
revision and payload; MSET/PEXPIRE permissions are checked before writes.
Clients require EVAL, GET and MSET permissions for their namespace; TTL writes
also require PEXPIRE. ACL validation happens before publication.
The host must retain revision keys (including a compatible eviction policy),
provide write availability, and configure Redis durability to its own needs.
Observed payload expiry is a separate tombstone transition and may advance the
revision even when a stale batch conflicts; its deltas are never published.

A network error during mutation may occur after the backend committed. Neither
retrying with the same token and receiving a conflict nor reloading a newer token
proves that this host's write succeeded. Hosts may reconcile against unique
application message/artifact identities and exact expected checkpoint content;
without sufficient evidence the outcome remains unknown. No blind reapply with
a fresh token, durable operation journal, or cross-backend transaction is provided.
Compile produces proposals and never commits checkpoint state automatically.
