package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestResourceReplay_IndependentCodecs(t *testing.T) {
	// Arrange: root, resource-message and resource-label registries have different topologies.
	record, root, resource := fixtureIndependentResourceRecord(t)
	wire, err := contexty.EncodeSavedRecord(record)
	require.NoError(t, err)
	restored, err := contexty.DecodeSavedRecord(wire)
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(restored.Manifest)
	require.NoError(t, err)
	codecs := map[string]contexty.ResourceCodec{"resolve": resource}
	option := contexty.WithReplayResourceCodecs(codecs)
	delete(codecs, "resolve")
	resource.Labels.Register(
		"late-registration",
		func([]byte) (contexty.Extension, error) { return nil, contexty.ErrReplayCodec },
	)
	// Act: only the frozen explicit capability applies, not caller mutations.
	replayed, err := contexty.Replay(context.Background(), restored, expected, root, option)
	// Assert: labeled original evidence and all three outputs restore without unused decoder calls.
	require.NoError(t, err)
	require.Len(t, replayed.Outputs, 3)
	require.Len(t, replayed.Artifacts[0].Extensions, 1)
	for _, output := range replayed.Outputs {
		var messages []contexty.Message
		for _, segment := range output.Segments {
			messages = append(messages, segment...)
		}
		require.Len(t, messages, 1)
		require.Equal(t, "safe", messages[0].TextContent())
		require.Len(t, messages[0].Extensions, 1)
	}
}

func TestResourceReplay_CodecPreflight(t *testing.T) {
	// Arrange: invalid capability must fail before even the root label decoder runs.
	record, root, resource := fixtureIndependentResourceRecord(t)
	expected, err := contexty.ReplayExpectationFor(record.Manifest)
	require.NoError(t, err)
	decodes := 0
	root.Extensions = contexty.NewExtensionRegistry()
	root.Extensions.Register("fixture-label", func(wire []byte) (contexty.Extension, error) {
		decodes++
		return fixtureWireExtension{wire: string(wire)}, nil
	})
	for _, scenario := range []string{"absent", "empty", "wrong-id", "surplus-id", "duplicate-option", "missing-label", "missing-message"} {
		t.Run(scenario, func(t *testing.T) {
			changed := resource
			options := []contexty.ReplayOption{
				contexty.WithReplayResourceCodecs(map[string]contexty.ResourceCodec{"resolve": resource}),
			}
			switch scenario {
			case "absent":
				options = nil
			case "empty":
				options[0] = contexty.WithReplayResourceCodecs(nil)
			case "wrong-id":
				options[0] = contexty.WithReplayResourceCodecs(map[string]contexty.ResourceCodec{"wrong": resource})
			case "surplus-id":
				options[0] = contexty.WithReplayResourceCodecs(
					map[string]contexty.ResourceCodec{"resolve": resource, "extra": resource},
				)
			case "duplicate-option":
				options = append(options, options[0])
			case "missing-label":
				changed.Labels = nil
				options[0] = contexty.WithReplayResourceCodecs(map[string]contexty.ResourceCodec{"resolve": changed})
			case "missing-message":
				changed.Messages = contexty.DefaultJSONSerializer()
				options[0] = contexty.WithReplayResourceCodecs(map[string]contexty.ResourceCodec{"resolve": changed})
			}
			// Act / Assert: no decoding, output or inferred codec fallback.
			result, replayErr := contexty.Replay(context.Background(), record, expected, root, options...)
			require.ErrorIs(t, replayErr, contexty.ErrReplayCodec)
			require.Zero(t, result)
			require.Zero(t, decodes)
		})
	}
}

func TestResourceReplay_DecoderCancellation(t *testing.T) {
	// Arrange: resource decoding cancels before root output decoding starts.
	record, root, resource := fixtureIndependentResourceRecord(t)
	expected, err := contexty.ReplayExpectationFor(record.Manifest)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	decodes := 0
	resource.Labels = contexty.NewExtensionRegistry()
	resource.Labels.Register("fixture-label", func([]byte) (contexty.Extension, error) {
		decodes++
		cancel()
		return nil, contexty.ErrResourceDenied
	})
	resource.Labels.Register("unused-label", func([]byte) (contexty.Extension, error) {
		t.Fatal("unused decoder must not execute")
		return nil, contexty.ErrReplayCodec
	})
	root.Extensions = contexty.NewExtensionRegistry()
	root.Extensions.Register("fixture-label", func([]byte) (contexty.Extension, error) {
		t.Fatal("root output decoder must not run after resource cancellation")
		return nil, contexty.ErrReplayCodec
	})
	// Act / Assert: cancellation dominates callback error, without partial outputs or later callbacks.
	result, err := contexty.Replay(ctx, record, expected, root,
		contexty.WithReplayResourceCodecs(map[string]contexty.ResourceCodec{"resolve": resource}))
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, result)
	require.Equal(t, 1, decodes)
}
