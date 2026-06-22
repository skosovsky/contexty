package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/contexty/adapters/blob/memory"
)

type fixtureBlobAvailability func(context.Context, contexty.BlobAvailabilityRequest) error

func (f fixtureBlobAvailability) CheckBlob(ctx context.Context, request contexty.BlobAvailabilityRequest) error {
	return f(ctx, request)
}

func fixtureBlobReplayOption(t *testing.T, blob contexty.BlobDescriptor) contexty.ReplayOption {
	t.Helper()
	return contexty.WithReplayBlobAvailability("fresh-replay", fixtureBlobAvailability(
		func(ctx context.Context, request contexty.BlobAvailabilityRequest) error {
			require.NoError(t, ctx.Err())
			require.Equal(t, "fresh-replay", request.ScopeRef)
			require.Equal(t, blob, request.Blob)
			return nil
		}))
}

type fixtureReplayBlobBackend struct {
	*memory.Store

	gets   int
	checks int
}

func (s *fixtureReplayBlobBackend) Get(
	ctx context.Context,
	request contexty.BlobGetRequest,
) (contexty.BlobContent, error) {
	s.gets++
	return s.Store.Get(ctx, request)
}

func (s *fixtureReplayBlobBackend) CheckBlob(ctx context.Context, request contexty.BlobAvailabilityRequest) error {
	s.checks++
	return s.Store.CheckBlob(ctx, request)
}

func fixtureReplayBlobFixture(t *testing.T) (*fixtureReplayBlobBackend, contexty.BlobDescriptor,
	contexty.SavedCompileRecord, contexty.ReplayExpectation,
) {
	t.Helper()
	backend, err := memory.New(memory.Config{Namespace: "replay", MaxObjectBytes: 20000,
		Authorize: func(_ context.Context, access memory.Access) error {
			if access.Action == memory.ActionCheck && access.ScopeRef != "fresh-replay" {
				return contexty.ErrBlobDenied
			}
			return nil
		}})
	require.NoError(t, err)
	store := &fixtureReplayBlobBackend{Store: backend, gets: 0, checks: 0}
	offloader := contexty.BlobOffloader{
		PolicyIdentity: contexty.Descriptor{ID: "host-policy", Revision: "pinned"},
		Storage:        store,
		Policy: fixtureBlobPolicy(
			func(context.Context, contexty.BlobOffloadCandidate) (contexty.BlobOffloadDecision, error) {
				return contexty.BlobOffloadDecision{Disposition: contexty.BlobOffload,
					Preview: contexty.BlobContent{MIMEType: fixtureBlobMIME, Bytes: []byte("ok")}}, nil
			},
		),
	}
	prepared, err := offloader.ProjectArtifact(context.Background(), fixtureBlobArtifactRequest())
	require.NoError(t, err)
	engine := contexty.NewEngine(
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureRecordProfile()),
		contexty.WithCompileContentCapture(
			contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent),
		),
	)
	result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		CompilationID: "blob-replay", Artifacts: []contexty.ContextArtifact{*prepared.Artifact},
		Lineage: prepared.Lineage, Origins: []contexty.ContentRef{prepared.Artifact.Blob.Original}})
	require.NoError(t, err)
	accepted, err := result.Record.Accept("host-accept")
	require.NoError(t, err)
	wire, err := contexty.EncodeSavedRecord(accepted)
	require.NoError(t, err)
	restored, err := contexty.DecodeSavedRecord(wire)
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(restored.Manifest)
	require.NoError(t, err)
	require.NoError(t, store.CommitCheckpoint(context.Background(), "commit",
		contexty.Descriptor{ID: "checkpoint", Revision: "accepted"}, []string{prepared.Selection.Stored.RetentionRef}))
	return store, *prepared.Selection.Stored, restored, expected
}

func fixtureHistoricalBlobRecord(
	t *testing.T,
	blobs []contexty.BlobDescriptor,
) (contexty.SavedCompileRecord, contexty.ReplayExpectation) {
	t.Helper()
	request := fixtureHistoricalArgumentRequest()
	for index := range request.History {
		request.History[index].Extensions = nil
	}
	for index, blob := range blobs {
		call := request.History[0].Parts[index].(contexty.ToolCallPart)
		call.ArgumentsBlob = &blob
		request.History[0].Parts[index] = call
	}
	engine := contexty.NewEngine(
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureRecordProfile("copy")),
		contexty.WithCompileContentCapture(
			contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent),
		),
	)
	result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		CompilationID: "argument-replay", History: request.History, Targets: []contexty.CompileTarget{{Name: "copy"}}})
	require.NoError(t, err)
	accepted, err := result.Record.Accept("host-accept")
	require.NoError(t, err)
	wire, err := contexty.EncodeSavedRecord(accepted)
	require.NoError(t, err)
	restored, err := contexty.DecodeSavedRecord(wire)
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(restored.Manifest)
	require.NoError(t, err)
	return restored, expected
}

type fixtureContentPolicy func(context.Context, contexty.CaptureCandidate) (bool, error)

func (f fixtureContentPolicy) Keep(ctx context.Context, candidate contexty.CaptureCandidate) (bool, error) {
	return f(ctx, candidate)
}

func fixtureAllowContent(context.Context, contexty.CaptureCandidate) (bool, error) { return true, nil }

func fixtureSimpleRecord(t *testing.T) (contexty.SavedCompileRecord, contexty.ReplayExpectation) {
	t.Helper()
	message := contexty.TextMessage(contexty.RoleUser, "safe")
	message.ID = "m"
	result, err := contexty.NewEngine(contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureRecordProfile()),
		contexty.WithCompileContentCapture(contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent)),
		contexty.WithBudgetPipeline(contexty.SegmentHistory,
			contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10)}, contexty.CharTokenEstimator{}),
		),
	).
		CompileSnapshot(context.Background(), contexty.CompileRequest{CompilationID: "simple", History: []contexty.Message{message}})
	require.NoError(t, err)
	record, err := result.Record.Accept("host-accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(*result.Manifest)
	require.NoError(t, err)
	return record, expected
}
