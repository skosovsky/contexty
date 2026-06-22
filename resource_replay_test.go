package contexty_test

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestResource_AcceptedReplay(t *testing.T) {
	// Arrange: host explicitly approves actual raw resolution dependencies.
	engine, calls := fixtureResourceRecordingEngine(t, fixtureContentPolicy(fixtureAllowContent))
	ctx := context.Background()
	compiled, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{CompilationID: "resource-replay"})
	require.NoError(t, err)
	require.Len(t, compiled.Manifest.Resources, 1)
	require.Equal(t, 4, compiled.Manifest.Resources[0].Estimate.Total)
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	wire, err := contexty.EncodeSavedRecord(accepted)
	require.NoError(t, err)
	require.Contains(t, string(wire), "private body")
	restored, err := contexty.DecodeSavedRecord(wire)
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(restored.Manifest)
	require.NoError(t, err)
	// Act: exact replay from saved bytes, with no resolution execution or refetch.
	resourceCodecs := contexty.WithReplayResourceCodecs(map[string]contexty.ResourceCodec{
		"resolve": {Messages: contexty.DefaultJSONSerializer()},
	})
	replayed, err := contexty.Replay(ctx, restored, expected, contexty.DefaultJSONSerializer(), resourceCodecs)
	// Assert: actual original/projected artifacts validate before outputs escape.
	require.NoError(t, err)
	require.Equal(t, 1, *calls)
	require.Equal(t, "safe", replayed.Outputs[0].Segments["memory"][0].TextContent())
	require.Equal(t, compiled.Artifacts, replayed.Artifacts)
	for _, scenario := range []string{"missing-body", "wrong-kind", "current-configuration"} {
		t.Run(scenario, func(t *testing.T) {
			changed, cloneErr := restored.Clone()
			require.NoError(t, cloneErr)
			intent, intentErr := contexty.ReplayExpectationFor(restored.Manifest)
			require.NoError(t, intentErr)
			body := restored.Manifest.Resources[0].Selection.Resource.Content
			switch scenario {
			case "missing-body":
				changed.Content = slices.DeleteFunc(
					changed.Content,
					func(content contexty.SavedContent) bool { return content.Ref == body },
				)
			case "wrong-kind":
				for index := range changed.Content {
					if changed.Content[index].Ref == body {
						changed.Content[index].Kind = contexty.SavedMessage
					}
				}
			case "current-configuration":
				intent.CompileConfiguration.Deferred[0].Resources[0].Configuration.Reader.Revision = "changed"
			}
			failed, replayErr := contexty.Replay(ctx, changed, intent, contexty.DefaultJSONSerializer(), resourceCodecs)
			require.ErrorIs(t, replayErr, map[string]error{"missing-body": contexty.ErrMissingReplayDependency,
				"wrong-kind": contexty.ErrUnsupportedReplay, "current-configuration": contexty.ErrReplayMismatch}[scenario])
			require.Zero(t, failed)
			require.Equal(t, 1, *calls)
		})
	}
}

func TestResource_CaptureCancellation(t *testing.T) {
	// Arrange: the host cancels while deciding retention for the actual raw body.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	policies := 0
	policy := fixtureContentPolicy(func(_ context.Context, candidate contexty.CaptureCandidate) (bool, error) {
		policies++
		require.Equal(t, "resource-read", candidate.Stage)
		cancel()
		return true, contexty.ErrResourceDenied
	})
	engine, calls := fixtureResourceRecordingEngine(t, policy)
	// Act / Assert: cancellation dominates host error and prevents later capture/output.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{CompilationID: "canceled-resource"})
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, result)
	require.Equal(t, 1, *calls)
	require.Equal(t, 1, policies)
}

func TestResource_RecordContainerIsolation(t *testing.T) {
	// Arrange: Source, manifest and privacy-approved proposal are distinct evidence owners.
	engine, _ := fixtureResourceRecordingEngine(t, fixtureContentPolicy(fixtureAllowContent))
	compiled, err := engine.CompileSnapshot(
		context.Background(),
		contexty.CompileRequest{CompilationID: "owned-resource"},
	)
	require.NoError(t, err)
	// Act: mutate caller-visible Source and one independent estimate container.
	compiled.Source.DeferredResources[0].Configuration.Estimate.Capabilities[contexty.EstimateText] = contexty.EstimateUnknown
	compiled.Manifest.Resources[0].Estimate.Profile.Capabilities[contexty.EstimateText] = contexty.EstimateUnknown
	// Assert: selected manifest intent and saved proposal remain independent and valid.
	require.Equal(
		t,
		contexty.EstimateEstimated,
		compiled.Manifest.Resources[0].Selection.Configuration.Estimate.Capabilities[contexty.EstimateText],
	)
	require.Equal(
		t,
		contexty.EstimateEstimated,
		compiled.Manifest.CompileConfiguration.Deferred[0].Resources[0].Configuration.Estimate.Capabilities[contexty.EstimateText],
	)
	require.NoError(t, compiled.Record.Validate())
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	require.NoError(t, accepted.Validate())
}

func TestResource_CapturePrivacy(t *testing.T) {
	// Arrange: denial of the actual raw body must dominate all later purposes.
	policy := fixtureContentPolicy(func(_ context.Context, candidate contexty.CaptureCandidate) (bool, error) {
		return candidate.Content.Ref.ID != "body", nil
	})
	engine, calls := fixtureResourceRecordingEngine(t, policy)
	// Act: compile may produce prompt-safe content, but cannot accept missing evidence.
	compiled, err := engine.CompileSnapshot(
		context.Background(),
		contexty.CompileRequest{CompilationID: "private-resource"},
	)
	// Assert: no denied bytes in saved proposal; raw absence cannot be hidden by its hash.
	require.NoError(t, err)
	require.Equal(t, 1, *calls)
	wire, err := contexty.EncodeSavedRecord(*compiled.Record)
	require.NoError(t, err)
	require.NotContains(t, string(wire), "private body")
	accepted, err := compiled.Record.Accept("not-enough-content")
	require.ErrorIs(t, err, contexty.ErrMissingReplayDependency)
	require.Zero(t, accepted)
	require.Equal(t, "safe", compiled.Payload.Memory[0].TextContent())
}
