package contexty_test

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestResourceAppend_IndependentCodecs(t *testing.T) {
	// Arrange: actual append uses three independently configured codec topologies.
	record, root, resource := fixtureIndependentResourceRecord(t, true)
	wire, err := contexty.EncodeSavedRecord(record)
	require.NoError(t, err)
	restored, err := contexty.DecodeSavedRecord(wire)
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(restored.Manifest)
	require.NoError(t, err)
	// Act: no runtime ports exist in the replay capability.
	replayed, err := contexty.Replay(context.Background(), restored, expected, root,
		contexty.WithReplayResourceCodecs(map[string]contexty.ResourceCodec{"resolve": resource}))
	// Assert: root-only old labels and resource labels both survive main/two targets.
	require.NoError(t, err)
	require.Len(t, replayed.Outputs, 3)
	require.Len(t, replayed.Artifacts, 1)
	require.Equal(t, "old\nsafe", replayed.Artifacts[0].Payload.Text)
	require.Len(t, replayed.Artifacts[0].Extensions, 2)
	require.Equal(t, []contexty.SourceRef{{ID: "old-source"}, {ID: "opaque-source"}}, replayed.Artifacts[0].SourceRefs)
	for _, output := range replayed.Outputs {
		var messages []contexty.Message
		for _, segment := range output.Segments {
			messages = append(messages, segment...)
		}
		require.Len(t, messages, 1)
		require.Equal(t, "old\nsafe", messages[0].TextContent())
		require.Len(t, messages[0].Extensions, 2)
	}
}

func TestResourceAppend_MissingDependencies(t *testing.T) {
	// Arrange: accepted append requires old, incoming, derived artifact and derived message.
	record, root, resource := fixtureIndependentResourceRecord(t, true)
	merge := record.Manifest.Resources[0].Merge
	for _, scenario := range []struct {
		name string
		ref  contexty.ContentRef
	}{
		{"old", merge.Inputs[0]}, {"incoming", merge.Inputs[1]},
		{"derived-artifact", merge.Artifact}, {"derived-message", merge.Message},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			changed, err := record.Clone()
			require.NoError(t, err)
			expected, err := contexty.ReplayExpectationFor(record.Manifest)
			require.NoError(t, err)
			ref := scenario.ref
			ref.Occurrence = ""
			changed.Content = slices.DeleteFunc(
				changed.Content,
				func(content contexty.SavedContent) bool { return content.Ref == ref },
			)
			// Act / Assert: metadata alone cannot replace any removed dependency.
			result, err := contexty.Replay(context.Background(), changed, expected, root,
				contexty.WithReplayResourceCodecs(map[string]contexty.ResourceCodec{"resolve": resource}))
			require.ErrorIs(t, err, contexty.ErrMissingReplayDependency)
			require.Zero(t, result)
		})
	}
}

func TestResourceAppend_ReplayRootCancellation(t *testing.T) {
	// Arrange: resource codecs validate first; old/derived artifact decode uses current root.
	record, root, resource := fixtureIndependentResourceRecord(t, true)
	expected, err := contexty.ReplayExpectationFor(record.Manifest)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	decodes := 0
	root.Extensions = contexty.NewExtensionRegistry()
	root.Extensions.Register("fixture-label", func([]byte) (contexty.Extension, error) {
		decodes++
		cancel()
		return nil, contexty.ErrResourceDenied
	})
	// Act / Assert: cancellation dominates codec error and stops the next old/derived/output decode.
	result, err := contexty.Replay(ctx, record, expected, root,
		contexty.WithReplayResourceCodecs(map[string]contexty.ResourceCodec{"resolve": resource}))
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, result)
	require.Equal(t, 1, decodes)
}
