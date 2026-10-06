package memory_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/contexty/adapters/blob/memory"
)

func backend(t *testing.T, authorize func(context.Context, memory.Access) error) *memory.Store {
	t.Helper()
	store, err := memory.New(memory.Config{Namespace: "host", MaxObjectBytes: 64, Authorize: authorize})
	require.NoError(t, err)
	return store
}

func allow(_ context.Context, _ memory.Access) error { return nil }

func request(ref string) contexty.BlobPutRequest {
	digest := sha256.Sum256([]byte("source"))
	return contexty.BlobPutRequest{ScopeRef: "write", RetentionRef: ref,
		Content: contexty.BlobContent{MIMEType: "text/plain", Bytes: []byte("payload")},
		Sources: []contexty.ContentRef{{ID: "source", Digest: hex.EncodeToString(digest[:]), Occurrence: ""}}}
}

func checkpoint(ref string) contexty.Descriptor {
	return contexty.Descriptor{ID: ref, Revision: "accepted"}
}

func cleanup(blob contexty.BlobDescriptor) contexty.BlobCleanupIntent {
	return contexty.BlobCleanupIntent{Object: blob.Object, ScopeRef: blob.ScopeRef, RetentionRef: blob.RetentionRef}
}

func TestRetentionCheckpoints(t *testing.T) {
	// Arrange: two checkpoints share one immutable object via distinct claims.
	ctx := context.Background()
	store := backend(t, allow)
	blob, err := store.Put(ctx, request("first"))
	require.NoError(t, err)
	require.NoError(t, store.Retain(ctx, "retain", blob.Object, "second"))
	require.NoError(t, store.CommitCheckpoint(ctx, "commit", checkpoint("one"), []string{"first"}))
	require.NoError(t, store.CommitCheckpoint(ctx, "commit", checkpoint("two"), []string{"second"}))
	// Act: retire source and release just one checkpoint.
	affected, err := store.RetireSource(ctx, "delete", blob.Sources[0])
	require.NoError(t, err)
	require.Equal(t, []contexty.Descriptor{blob.Object}, affected)
	require.NoError(t, store.ReleaseCheckpoint(ctx, "release", checkpoint("one")))
	deleted, err := store.Collect(ctx, "cleanup", blob.Object)
	// Assert: the other checkpoint still protects the object.
	require.ErrorIs(t, err, memory.ErrActiveClaim)
	require.False(t, deleted)
	require.NoError(t, store.CheckBlob(ctx, contexty.BlobAvailabilityRequest{ScopeRef: "fresh", Blob: blob}))
	require.NoError(t, store.ReleaseCheckpoint(ctx, "release", checkpoint("two")))
	require.NoError(t, store.ReleaseCheckpoint(ctx, "release", checkpoint("two")))
	deleted, err = store.Collect(ctx, "cleanup", blob.Object)
	require.NoError(t, err)
	require.True(t, deleted)
	require.ErrorIs(
		t,
		store.CheckBlob(ctx, contexty.BlobAvailabilityRequest{ScopeRef: "fresh", Blob: blob}),
		contexty.ErrBlobMissing,
	)
	deleted, err = store.Collect(ctx, "cleanup", blob.Object)
	require.NoError(t, err)
	require.False(t, deleted)
	require.ErrorIs(
		t,
		store.CommitCheckpoint(ctx, "commit", checkpoint("two"), []string{"second"}),
		memory.ErrClaimConflict,
	)
}

func TestCommitAtomicityAndIdentity(t *testing.T) {
	// Arrange: a valid pending claim and a nonexistent claim.
	ctx := context.Background()
	store := backend(t, allow)
	blob, err := store.Put(ctx, request("first"))
	require.NoError(t, err)
	// Act: attempt a partial/duplicate commit, then the exact valid commit.
	require.ErrorIs(
		t,
		store.CommitCheckpoint(ctx, "commit", checkpoint("one"), []string{"first", "missing"}),
		memory.ErrClaimConflict,
	)
	require.ErrorIs(
		t,
		store.CommitCheckpoint(ctx, "commit", checkpoint("one"), []string{"first", "first"}),
		memory.ErrClaimConflict,
	)
	require.NoError(t, store.CommitCheckpoint(ctx, "commit", checkpoint("one"), []string{"first"}))
	// Assert: invalid commit did not consume any claim/checkpoint identity.
	require.NoError(t, store.CommitCheckpoint(ctx, "commit", checkpoint("one"), []string{"first"}))
	require.ErrorIs(
		t,
		store.CommitCheckpoint(ctx, "commit", checkpoint("two"), []string{"first"}),
		memory.ErrClaimConflict,
	)
	deleted, err := store.ReconcileCleanup(ctx, "cleanup", cleanup(blob))
	require.ErrorIs(t, err, memory.ErrActiveClaim)
	require.False(t, deleted)
	require.NoError(t, store.ReleaseCheckpoint(ctx, "release", checkpoint("one")))
	require.ErrorIs(t, store.Retain(ctx, "retain", blob.Object, "first"), memory.ErrClaimConflict)
}

func TestProvisionalClaimAndSourceRevision(t *testing.T) {
	// Arrange: two revisions of one logical source and provisional claims.
	ctx := context.Background()
	store := backend(t, allow)
	original := request("first")
	other := request("second")
	other.Sources[0].Occurrence = "another-revision"
	blob, err := store.Put(ctx, original)
	require.NoError(t, err)
	otherBlob, err := store.Put(ctx, other)
	require.NoError(t, err)
	// Act: retire only the original exact source ref.
	affected, err := store.RetireSource(ctx, "delete", original.Sources[0])
	require.NoError(t, err)
	deleted, err := store.Collect(ctx, "cleanup", blob.Object)
	// Assert: pending claim prevents premature deletion; retirement blocks new work.
	require.Equal(t, []contexty.Descriptor{blob.Object}, affected)
	require.ErrorIs(t, err, memory.ErrActiveClaim)
	require.False(t, deleted)
	require.ErrorIs(t, store.CommitCheckpoint(ctx, "commit", checkpoint("one"), []string{"first"}), memory.ErrRetiring)
	require.ErrorIs(t, store.Retain(ctx, "retain", blob.Object, "new"), memory.ErrRetiring)
	_, err = store.Put(ctx, request("third"))
	require.ErrorIs(t, err, memory.ErrRetiring)
	require.NoError(t, store.CommitCheckpoint(ctx, "commit", checkpoint("other"), []string{"second"}))
	require.NoError(t, store.CheckBlob(ctx, contexty.BlobAvailabilityRequest{ScopeRef: "fresh", Blob: otherBlob}))
	deleted, err = store.ReconcileCleanup(ctx, "cleanup", cleanup(blob))
	require.NoError(t, err)
	require.True(t, deleted)
	deleted, err = store.ReconcileCleanup(ctx, "cleanup", cleanup(blob))
	require.NoError(t, err)
	require.False(t, deleted)
}

func TestCleanupBindingAndOtherClaims(t *testing.T) {
	// Arrange: provisional and committed claims on the same object.
	ctx := context.Background()
	store := backend(t, allow)
	blob, err := store.Put(ctx, request("first"))
	require.NoError(t, err)
	require.NoError(t, store.Retain(ctx, "retain", blob.Object, "second"))
	require.NoError(t, store.CommitCheckpoint(ctx, "commit", checkpoint("two"), []string{"second"}))
	for _, intent := range []contexty.BlobCleanupIntent{
		{Object: blob.Object, ScopeRef: "wrong", RetentionRef: "first"},
		{Object: checkpoint("forged"), ScopeRef: blob.ScopeRef, RetentionRef: "first"},
		{Object: blob.Object, ScopeRef: blob.ScopeRef, RetentionRef: "missing"},
	} {
		// Act / Assert: forged intents neither abort nor delete anything.
		deleted, cleanupErr := store.ReconcileCleanup(ctx, "cleanup", intent)
		require.ErrorIs(t, cleanupErr, memory.ErrClaimConflict)
		require.False(t, deleted)
	}
	// Act: definitive failure aborts only the pending claim.
	deleted, err := store.ReconcileCleanup(ctx, "cleanup", cleanup(blob))
	// Assert: the committed claim survives, and an aborted identity cannot be reused.
	require.ErrorIs(t, err, memory.ErrActiveClaim)
	require.False(t, deleted)
	require.ErrorIs(t, store.Retain(ctx, "retain", blob.Object, "first"), memory.ErrRetiring)
	require.NoError(t, store.ReleaseCheckpoint(ctx, "release", checkpoint("two")))
	deleted, err = store.Collect(ctx, "cleanup", blob.Object)
	require.NoError(t, err)
	require.True(t, deleted)
	_, err = store.Put(ctx, request("first"))
	require.ErrorIs(t, err, memory.ErrClaimConflict)
}

func TestAuthorizationAndCancellation(t *testing.T) {
	// Arrange: host decides access per action and fresh explicit scope.
	ctx := context.Background()
	deniedAction := memory.ActionGet
	store := backend(t, func(_ context.Context, access memory.Access) error {
		if access.ScopeRef == "denied" || access.Action == deniedAction {
			return contexty.ErrBlobDenied
		}
		return nil
	})
	blob, err := store.Put(ctx, request("first"))
	require.NoError(t, err)
	// Act / Assert: neither a known descriptor nor a retention claim grants reads.
	_, err = store.Get(ctx, contexty.BlobGetRequest{ScopeRef: blob.ScopeRef, Object: blob.Object, MaxBytes: 64})
	require.ErrorIs(t, err, contexty.ErrBlobDenied)
	require.ErrorIs(
		t,
		store.CheckBlob(ctx, contexty.BlobAvailabilityRequest{ScopeRef: "denied", Blob: blob}),
		contexty.ErrBlobDenied,
	)
	deniedAction = memory.ActionCommit
	require.ErrorIs(
		t,
		store.CommitCheckpoint(ctx, "commit", checkpoint("one"), []string{"first"}),
		contexty.ErrBlobDenied,
	)
	deniedAction = memory.ActionRelease
	require.NoError(t, store.CommitCheckpoint(ctx, "commit", checkpoint("one"), []string{"first"}))
	require.ErrorIs(t, store.ReleaseCheckpoint(ctx, "release", checkpoint("one")), contexty.ErrBlobDenied)

	// Arrange: cancellation inside the host callback must dominate its return.
	canceled, cancel := context.WithCancel(ctx)
	store = backend(t, func(_ context.Context, _ memory.Access) error { cancel(); return nil })
	// Act / Assert: canceled Put exposes no object and mutates no retention state.
	result, err := store.Put(canceled, request("first"))
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, result)
}

func TestStorageIsolationAndLimits(t *testing.T) {
	// Arrange: the host callback deliberately mutates caller-owned buffers.
	ctx := context.Background()
	input := request("first")
	store := backend(t, func(_ context.Context, _ memory.Access) error {
		input.Content.Bytes[0] = 'X'
		input.Sources[0].ID = "mutated"
		return nil
	})
	// Act.
	blob, err := store.Put(ctx, input)
	require.NoError(t, err)
	content, err := store.Get(ctx, contexty.BlobGetRequest{ScopeRef: "read", Object: blob.Object, MaxBytes: 7})
	// Assert: stored bytes/metadata and returned buffers are independently owned.
	require.NoError(t, err)
	require.Equal(t, []byte("payload"), content.Bytes)
	require.Equal(t, "source", blob.Sources[0].ID)
	content.Bytes[0] = 'Y'
	content, err = store.Get(ctx, contexty.BlobGetRequest{ScopeRef: "read", Object: blob.Object, MaxBytes: 7})
	require.NoError(t, err)
	require.Equal(t, []byte("payload"), content.Bytes)
	_, err = store.Get(ctx, contexty.BlobGetRequest{ScopeRef: "read", Object: blob.Object, MaxBytes: 6})
	require.ErrorIs(t, err, contexty.ErrBlobSizeLimit)
	altered := blob.Clone()
	altered.Sources[0].ID = "forged"
	require.ErrorIs(
		t,
		store.CheckBlob(ctx, contexty.BlobAvailabilityRequest{ScopeRef: "fresh", Blob: altered}),
		contexty.ErrBlobDigestMismatch,
	)
}

func TestConcurrentRetainCleanup(t *testing.T) {
	// Arrange: retirement happens before contenders, so new retention cannot race
	// an eligible object back into use. Authorization may safely call the backend.
	ctx := context.Background()
	store := backend(t, allow)
	blob, err := store.Put(ctx, request("first"))
	require.NoError(t, err)
	_, err = store.RetireSource(ctx, "delete", blob.Sources[0])
	require.NoError(t, err)
	var group sync.WaitGroup
	outcomes := make(chan error, 2)
	// Act.
	group.Go(func() { outcomes <- store.Retain(ctx, "retain", blob.Object, "new") })
	group.Go(func() {
		_, cleanupErr := store.ReconcileCleanup(ctx, "cleanup", cleanup(blob))
		outcomes <- cleanupErr
	})
	group.Wait()
	close(outcomes)
	// Assert: no successful new claim can survive object cleanup.
	for outcome := range outcomes {
		require.True(
			t,
			outcome == nil || errors.Is(outcome, memory.ErrRetiring) || errors.Is(outcome, contexty.ErrBlobMissing),
		)
	}
	require.ErrorIs(
		t,
		store.CheckBlob(ctx, contexty.BlobAvailabilityRequest{ScopeRef: "fresh", Blob: blob}),
		contexty.ErrBlobMissing,
	)
}

func TestCommitRetirementAtomicity(t *testing.T) {
	// Arrange: two valid pending claims, then retire only the second object.
	ctx := context.Background()
	store := backend(t, allow)
	first, err := store.Put(ctx, request("first"))
	require.NoError(t, err)
	secondInput := request("second")
	secondInput.Sources[0].ID = "second-source"
	second, err := store.Put(ctx, secondInput)
	require.NoError(t, err)
	_, err = store.RetireSource(ctx, "delete", second.Sources[0])
	require.NoError(t, err)
	// Act: failed multi-object commit must not commit the first claim.
	err = store.CommitCheckpoint(ctx, "commit", checkpoint("one"), []string{"first", "second"})
	// Assert: first claim/checkpoint identity remain available for a valid commit.
	require.ErrorIs(t, err, memory.ErrRetiring)
	require.NoError(t, store.CommitCheckpoint(ctx, "commit", checkpoint("one"), []string{"first"}))
	require.NoError(t, store.CheckBlob(ctx, contexty.BlobAvailabilityRequest{ScopeRef: "fresh", Blob: first}))
}

func TestAuthorizationRechecksState(t *testing.T) {
	// Arrange: host callback retires the source during commit authorization.
	ctx := context.Background()
	var store *memory.Store
	var blob contexty.BlobDescriptor
	store = backend(t, func(_ context.Context, access memory.Access) error {
		if access.Action == memory.ActionCommit && access.ClaimRef != "" {
			_, err := store.RetireSource(ctx, "delete", blob.Sources[0])
			return err
		}
		return nil
	})
	var err error
	blob, err = store.Put(ctx, request("first"))
	require.NoError(t, err)
	// Act: callbacks execute without holding the backend mutex.
	err = store.CommitCheckpoint(ctx, "commit", checkpoint("one"), []string{"first"})
	// Assert: reentrant authorization neither deadlocks nor bypasses retirement.
	require.ErrorIs(t, err, memory.ErrRetiring)
	deleted, err := store.ReconcileCleanup(ctx, "cleanup", cleanup(blob))
	require.NoError(t, err)
	require.True(t, deleted)
}

func TestObjectAuthorizationAndCommitCopies(t *testing.T) {
	// Arrange: host permits checkpoint-level action but denies one actual object.
	ctx := context.Background()
	denied := true
	refs := []string{"second", "first"}
	store := backend(t, func(_ context.Context, access memory.Access) error {
		if access.Action == memory.ActionCommit && access.ClaimRef == "second" && denied {
			return contexty.ErrBlobDenied
		}
		if access.Action == memory.ActionCommit {
			refs[0] = "caller-mutated"
		}
		return nil
	})
	blob, err := store.Put(ctx, request("first"))
	require.NoError(t, err)
	require.NoError(t, store.Retain(ctx, "retain", blob.Object, "second"))
	// Act / Assert: object-level denial prevents the entire commit.
	require.ErrorIs(t, store.CommitCheckpoint(ctx, "commit", checkpoint("one"), refs), contexty.ErrBlobDenied)
	denied = false
	refs = []string{"second", "first"}
	require.NoError(t, store.CommitCheckpoint(ctx, "commit", checkpoint("one"), refs))
	refs[1] = "another-mutation"
	// Assert: canonical saved claim set is independent of caller order/buffers.
	require.NoError(t, store.CommitCheckpoint(ctx, "commit", checkpoint("one"), []string{"first", "second"}))
	require.NoError(t, store.ReleaseCheckpoint(ctx, "release", checkpoint("one")))
}

func TestBackendIncarnationAndDescriptorBinding(t *testing.T) {
	// Arrange: backend instances use the same host namespace but independent life.
	ctx := context.Background()
	first := backend(t, allow)
	second := backend(t, allow)
	blob, err := first.Put(ctx, request("first"))
	require.NoError(t, err)
	other, err := second.Put(ctx, request("first"))
	require.NoError(t, err)
	// Act: forge retention binding or try the old instance's ref on a new backend.
	altered := blob.Clone()
	altered.RetentionRef = "unknown"
	// Assert: descriptors cannot impersonate another retention binding/incarnation.
	require.NotEqual(t, blob.Object, other.Object)
	require.ErrorIs(
		t,
		first.CheckBlob(ctx, contexty.BlobAvailabilityRequest{ScopeRef: "fresh", Blob: altered}),
		contexty.ErrBlobDigestMismatch,
	)
	require.ErrorIs(
		t,
		second.CheckBlob(ctx, contexty.BlobAvailabilityRequest{ScopeRef: "fresh", Blob: blob}),
		contexty.ErrBlobMissing,
	)
}

func TestCanceledRetentionMutations(t *testing.T) {
	for _, action := range []memory.Action{memory.ActionRetain, memory.ActionCommit, memory.ActionRetireSource, memory.ActionCleanup} {
		t.Run(string(action), func(t *testing.T) {
			// Arrange: only the requested mutation cancels in host authorization.
			ctx := context.Background()
			canceled, cancel := context.WithCancel(ctx)
			defer cancel()
			store := backend(t, func(_ context.Context, access memory.Access) error {
				if access.ScopeRef == "cancel" {
					cancel()
				}
				return nil
			})
			blob, err := store.Put(ctx, request("first"))
			require.NoError(t, err)
			// Act.
			err = canceledMutation(canceled, store, blob, action)
			// Assert: cancellation neither retires nor consumes any claim identity.
			require.ErrorIs(t, err, context.Canceled)
			require.NoError(t, store.CommitCheckpoint(ctx, "commit", checkpoint("one"), []string{"first"}))
			require.NoError(t, store.Retain(ctx, "retain", blob.Object, "second"))
		})
	}
}

func canceledMutation(
	ctx context.Context,
	store *memory.Store,
	blob contexty.BlobDescriptor,
	action memory.Action,
) error {
	switch action {
	case memory.ActionRetain:
		return store.Retain(ctx, "cancel", blob.Object, "second")
	case memory.ActionCommit:
		return store.CommitCheckpoint(ctx, "cancel", checkpoint("one"), []string{"first"})
	case memory.ActionRetireSource:
		_, err := store.RetireSource(ctx, "cancel", blob.Sources[0])
		return err
	case memory.ActionCleanup:
		_, err := store.ReconcileCleanup(ctx, "cancel", cleanup(blob))
		return err
	case memory.ActionPut, memory.ActionGet, memory.ActionCheck, memory.ActionRelease:
		return memory.ErrConfiguration
	default:
		return memory.ErrConfiguration
	}
}

func TestCanceledReleaseAndCollection(t *testing.T) {
	// Arrange: committed claim on a retiring object.
	ctx := context.Background()
	var cancel context.CancelFunc
	store := backend(t, func(_ context.Context, access memory.Access) error {
		if access.ScopeRef == "cancel" {
			cancel()
		}
		return nil
	})
	blob, err := store.Put(ctx, request("first"))
	require.NoError(t, err)
	require.NoError(t, store.CommitCheckpoint(ctx, "commit", checkpoint("one"), []string{"first"}))
	_, err = store.RetireSource(ctx, "delete", blob.Sources[0])
	require.NoError(t, err)
	canceled, cancel := context.WithCancel(ctx)
	defer cancel()
	// Act / Assert: canceled release must preserve the protecting claim.
	require.ErrorIs(t, store.ReleaseCheckpoint(canceled, "cancel", checkpoint("one")), context.Canceled)
	deleted, err := store.Collect(ctx, "cleanup", blob.Object)
	require.ErrorIs(t, err, memory.ErrActiveClaim)
	require.False(t, deleted)
	require.NoError(t, store.ReleaseCheckpoint(ctx, "release", checkpoint("one")))
	canceled, cancel = context.WithCancel(ctx)
	defer cancel()
	// Act / Assert: canceled cleanup must leave even an eligible object intact.
	deleted, err = store.Collect(canceled, "cancel", blob.Object)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, deleted)
	require.NoError(t, store.CheckBlob(ctx, contexty.BlobAvailabilityRequest{ScopeRef: "read", Blob: blob}))
	deleted, err = store.Collect(ctx, "cleanup", blob.Object)
	require.NoError(t, err)
	require.True(t, deleted)
}

func TestRemediation_EarlyPutRejection(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		// Arrange: large known input and an authorization callback that must not run.
		calls := 0
		store := backend(t, func(context.Context, memory.Access) error { calls++; return nil })
		put := request("early")
		put.Content.Bytes = make([]byte, 1024)
		ctx, cancel := context.WithCancel(context.Background())
		if canceled {
			cancel()
		}
		// Act.
		_, err := store.Put(ctx, put)
		cancel()
		// Assert: early admission costs do not run host policy or publish state.
		if canceled {
			require.ErrorIs(t, err, context.Canceled)
		} else {
			require.ErrorIs(t, err, contexty.ErrBlobSizeLimit)
		}
		require.Zero(t, calls)
		put.Content.Bytes = []byte("valid")
		_, err = store.Put(context.Background(), put)
		require.NoError(t, err)
	}
}
