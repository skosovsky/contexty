# Host-owned blob retention protocol

Core exposes immutable blob metadata and explicit Put/Get/check ports. It does
not authorize access, expire content, release claims, persist checkpoints, or
delete derived objects. The host supplies fresh scopes and authoritative decisions.

`adapters/blob/memory` is one executable reference backend. It is ephemeral:
it cannot retain checkpoints across a process restart. Production hosts must
persist bytes, claim bindings, checkpoint states, source retirement markers,
and identity tombstones atomically in their own backend. A new memory instance
has a new object-identity incarnation; its refs cannot alias earlier objects.

## Checkpoint handoff

1. `Put` freezes bytes/metadata and creates a provisional retention claim before
   returning a descriptor. Both provisional and committed claims prevent deletion.
2. Compile using the confirmed descriptor. If another checkpoint shares an
   object, call `Retain` with a new claim identity; never share one claim between
   checkpoints. All claims bind immutably to a single object and original scope.
3. Persist the host checkpoint containing the object references. Only after an
   authoritative successful commit call `CommitCheckpoint` with all its claims.
   The entire claim set commits atomically. An exact retry is idempotent; changing
   the set or reusing a released checkpoint identity conflicts.
4. For definitive checkpoint failure reconcile a `BlobCleanupIntent` using
   `ReconcileCleanup`. It aborts only the matched provisional claim and retires
   its object. Committed claims cannot be aborted. `ErrActiveClaim` can mean the
   provisional abort was recorded while another active claim defers deletion.
5. For unknown checkpoint outcome keep provisional claims until the host checks
   authoritative checkpoint state. Do not use age/timeouts as proof of failure.
   If claim commit fails after checkpoint persistence, retain provisional claims
   and reconcile; do not publish a successful handoff or blindly abort.

This handoff is not a cross-system distributed transaction. The host must
coordinate source deletion with checkpoint publication; otherwise it can persist
a checkpoint whose subsequent claim commit correctly fails because the source
was retired. Provisional claims prevent byte loss during that reconciliation.

## Source deletion and collection

`RetireSource` marks derivatives of an exact `ContentRef` (including digest and
occurrence) as retiring, remembers that source identity, and returns metadata-only
candidates. It rejects new writes from that source and new claims/commits on those
objects. It does not silently release existing claims or delete bytes.

The host decides which checkpoints must retire. After authoritative retirement,
`ReleaseCheckpoint` releases only that checkpoint's claims; an exact retry is
idempotent. Other checkpoints and provisional writes remain protected.
`Collect` deletes only a retiring object with no active claims. A missing object
is idempotent success; object, claim and checkpoint identities are never reused
within a backend lifetime. Tombstones contain no payload. A durable backend must
preserve this no-reuse property across restarts.

Every action invokes host authorization outside the backend lock. Commit/release
authorize both checkpoint and actual bound objects, then recheck state and context
cancellation under the mutex. Authorization revocation semantics are owned by
the host; the callback must synchronize its current access decision as required.
Original write scope/retention refs never grant fresh read or cleanup access.

`CheckBlob` checks live immutable metadata and the bound retention identity using
a fresh host scope; it does not fetch bytes. Missing objects give `ErrBlobMissing`,
forged metadata/bindings give `ErrBlobDigestMismatch`, and host denial remains
`ErrBlobDenied`. Pass `WithReplayBlobAvailability(freshScope, backend)` to exact
replay. Blob-bearing accepted outputs fail closed without this explicit port.
Replay checks required output/artifact references once per exact descriptor,
before issuing any output, never via Get/refetch. Missing/expired/denied/tampered
dependencies wrap `ErrMissingReplayDependency` and retain the original typed
host error; cancellation stops subsequent checks. Records without blob references
do not require a backend. Availability is checked at replay time, not guaranteed
forever: the host must keep claims active through consumption of returned refs.

See the executable `ExampleStore` and race tests in `adapters/blob/memory`.
