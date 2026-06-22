package contexty_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/contexty/adapters/blob/memory"
)

func TestReplay_BlobDeletion(t *testing.T) {
	// Arrange: actual offload -> compile -> accepted record codec -> host checkpoint.
	ctx := context.Background()
	store, blob, record, expected := fixtureReplayBlobFixture(t)
	option := contexty.WithReplayBlobAvailability("fresh-replay", store)
	before, err := contexty.Replay(ctx, record, expected, contexty.DefaultJSONSerializer(), option)
	require.NoError(t, err)
	require.Equal(t, blob, before.Artifacts[0].Blob.Object)
	require.Equal(t, 1, store.checks)
	_, err = store.RetireSource(ctx, "delete", blob.Sources[0])
	require.NoError(t, err)
	deleted, err := store.Collect(ctx, "cleanup", blob.Object)
	require.ErrorIs(t, err, memory.ErrActiveClaim)
	require.False(t, deleted)
	require.NoError(
		t,
		store.ReleaseCheckpoint(ctx, "release", contexty.Descriptor{ID: "checkpoint", Revision: "accepted"}),
	)
	deleted, err = store.Collect(ctx, "cleanup", blob.Object)
	require.NoError(t, err)
	require.True(t, deleted)
	// Act: accepted bytes still exist, but their live dependency was deleted.
	after, err := contexty.Replay(ctx, record, expected, contexty.DefaultJSONSerializer(), option)
	// Assert: no partial outputs, byte reads, refetch or deleted-content recovery.
	require.ErrorIs(t, err, contexty.ErrMissingReplayDependency)
	require.ErrorIs(t, err, contexty.ErrBlobMissing)
	require.Zero(t, after)
	require.Equal(t, 0, store.gets)
	require.Equal(t, 2, store.checks)
}

func TestReplay_BlobFreshScope(t *testing.T) {
	// Arrange: stored write scope is known, but is not current replay authorization.
	store, blob, record, expected := fixtureReplayBlobFixture(t)
	// Act.
	result, err := contexty.Replay(context.Background(), record, expected, contexty.DefaultJSONSerializer(),
		contexty.WithReplayBlobAvailability(blob.ScopeRef, store))
	// Assert: ref/write scope cannot bypass the host; no content is fetched.
	require.ErrorIs(t, err, contexty.ErrMissingReplayDependency)
	require.ErrorIs(t, err, contexty.ErrBlobDenied)
	require.Zero(t, result)
	require.Equal(t, 0, store.gets)
}

func TestReplay_BlobConfiguration(t *testing.T) {
	// Arrange: replay outputs actually require a blob; no implicit backend exists.
	store, _, record, expected := fixtureReplayBlobFixture(t)
	var typedNil *fixtureReplayBlobBackend
	for name, options := range map[string][]contexty.ReplayOption{
		"absent":         nil,
		"nil-option":     {nil},
		"nil-port":       {contexty.WithReplayBlobAvailability("fresh-replay", nil)},
		"typed-nil-port": {contexty.WithReplayBlobAvailability("fresh-replay", typedNil)},
		"empty-scope":    {contexty.WithReplayBlobAvailability("", store)},
		"duplicate":      {contexty.WithReplayBlobAvailability("fresh-replay", store), contexty.WithReplayBlobAvailability("fresh-replay", store)},
	} {
		t.Run(name, func(t *testing.T) {
			// Act / Assert: invalid capabilities fail closed without any callback.
			result, err := contexty.Replay(
				context.Background(),
				record,
				expected,
				contexty.DefaultJSONSerializer(),
				options...)
			require.ErrorIs(t, err, contexty.ErrMissingReplayDependency)
			require.Zero(t, result)
			require.Zero(t, store.checks)
			require.Zero(t, store.gets)
		})
	}
}

func TestReplay_BlobHostFailures(t *testing.T) {
	// Arrange: live host checks may report expiry, tampering, denial or cancellation.
	_, _, record, expected := fixtureReplayBlobFixture(t)
	for _, failure := range []error{contexty.ErrBlobExpired, contexty.ErrBlobDigestMismatch, contexty.ErrBlobDenied, errors.New("host check unavailable")} {
		t.Run(failure.Error(), func(t *testing.T) {
			checks := 0
			port := fixtureBlobAvailability(
				func(context.Context, contexty.BlobAvailabilityRequest) error { checks++; return failure },
			)
			// Act / Assert: original host error remains discoverable, output is zero.
			result, err := contexty.Replay(context.Background(), record, expected, contexty.DefaultJSONSerializer(),
				contexty.WithReplayBlobAvailability("fresh-replay", port))
			require.ErrorIs(t, err, contexty.ErrMissingReplayDependency)
			require.ErrorIs(t, err, failure)
			require.Zero(t, result)
			require.Equal(t, 1, checks)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	port := fixtureBlobAvailability(
		func(context.Context, contexty.BlobAvailabilityRequest) error { cancel(); return contexty.ErrBlobDenied },
	)
	// Act / Assert: cancellation inside host callback dominates even a denial.
	result, err := contexty.Replay(
		ctx,
		record,
		expected,
		contexty.DefaultJSONSerializer(),
		contexty.WithReplayBlobAvailability("fresh-replay", port),
	)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, result)
}

func TestReplay_BlobCallbackIsolation(t *testing.T) {
	// Arrange: host callback tries to mutate a descriptor's source container.
	_, blob, record, expected := fixtureReplayBlobFixture(t)
	port := fixtureBlobAvailability(func(_ context.Context, request contexty.BlobAvailabilityRequest) error {
		request.Blob.Sources[0].ID = "host-mutated"
		return nil
	})
	// Act.
	result, err := contexty.Replay(context.Background(), record, expected, contexty.DefaultJSONSerializer(),
		contexty.WithReplayBlobAvailability("fresh-replay", port))
	// Assert: actual output and saved record remain the accepted immutable data.
	require.NoError(t, err)
	require.Equal(t, blob, result.Artifacts[0].Blob.Object)
	require.NoError(t, record.Validate())
}

func TestReplay_HistoricalBlobDependencies(t *testing.T) {
	// Arrange: repeated same blob in two calls and main/target projections.
	blob := fixtureBlobReceipt(fixtureBlobRequest(t))
	record, expected := fixtureHistoricalBlobRecord(t, []contexty.BlobDescriptor{blob, blob})
	checks := 0
	port := fixtureBlobAvailability(func(_ context.Context, request contexty.BlobAvailabilityRequest) error {
		checks++
		require.Equal(t, blob, request.Blob)
		require.Equal(t, "fresh-replay", request.ScopeRef)
		return nil
	})
	// Act.
	result, err := contexty.Replay(context.Background(), record, expected, contexty.DefaultJSONSerializer(),
		contexty.WithReplayBlobAvailability("fresh-replay", port))
	// Assert: exact duplicate dependencies are checked once, then all outputs issue.
	require.NoError(t, err)
	require.Len(t, result.Outputs, 2)
	require.Equal(t, 1, checks)
	result, err = contexty.Replay(context.Background(), record, expected, contexty.DefaultJSONSerializer())
	require.ErrorIs(t, err, contexty.ErrMissingReplayDependency)
	require.Zero(t, result)
	port = fixtureBlobAvailability(
		func(context.Context, contexty.BlobAvailabilityRequest) error { return contexty.ErrBlobMissing },
	)
	result, err = contexty.Replay(context.Background(), record, expected, contexty.DefaultJSONSerializer(),
		contexty.WithReplayBlobAvailability("fresh-replay", port))
	require.ErrorIs(t, err, contexty.ErrMissingReplayDependency)
	require.ErrorIs(t, err, contexty.ErrBlobMissing)
	require.Zero(t, result)
}

func TestReplay_BlobStopsChecks(t *testing.T) {
	// Arrange: two distinct dependencies in deterministic call order.
	blob := fixtureBlobReceipt(fixtureBlobRequest(t))
	second := blob.Clone()
	second.Object.ID = "another-object"
	record, expected := fixtureHistoricalBlobRecord(t, []contexty.BlobDescriptor{blob, second})
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "cancellation"}[canceled], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			checks := 0
			port := fixtureBlobAvailability(func(_ context.Context, request contexty.BlobAvailabilityRequest) error {
				checks++
				require.Equal(t, blob, request.Blob)
				if canceled {
					cancel()
				}
				return contexty.ErrBlobMissing
			})
			// Act: first failure/cancellation stops subsequent dependencies.
			result, err := contexty.Replay(ctx, record, expected, contexty.DefaultJSONSerializer(),
				contexty.WithReplayBlobAvailability("fresh-replay", port))
			// Assert: zero result and exactly one callback, no partial success.
			want := contexty.ErrMissingReplayDependency
			if canceled {
				want = context.Canceled
			}
			require.ErrorIs(t, err, want)
			require.Zero(t, result)
			require.Equal(t, 1, checks)
		})
	}
}

func TestReplay_BlobValidatesAllBeforeChecks(t *testing.T) {
	// Arrange: first dependency valid, second malformed but canonically saved.
	blob := fixtureBlobReceipt(fixtureBlobRequest(t))
	second := blob.Clone()
	second.Digest = "invalid"
	record, expected := fixtureHistoricalBlobRecord(t, []contexty.BlobDescriptor{blob, second})
	checks := 0
	port := fixtureBlobAvailability(
		func(context.Context, contexty.BlobAvailabilityRequest) error { checks++; return nil },
	)
	// Act.
	result, err := contexty.Replay(context.Background(), record, expected, contexty.DefaultJSONSerializer(),
		contexty.WithReplayBlobAvailability("fresh-replay", port))
	// Assert: no host callback runs until all required descriptors are validated.
	require.ErrorIs(t, err, contexty.ErrMissingReplayDependency)
	require.ErrorIs(t, err, contexty.ErrInvalidBlob)
	require.Zero(t, result)
	require.Zero(t, checks)
}
