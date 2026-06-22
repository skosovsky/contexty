package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func fixtureAppendLabelPolicy(scenario string, cancel context.CancelFunc) fixtureLabelPolicy {
	return func(_ context.Context, inputs []contexty.Message, output contexty.Message, _ contexty.Descriptor) (contexty.LabelDecision, error) {
		if len(inputs) != 2 || output.TextContent() != "old\nsafe" {
			return contexty.LabelDecision{Extensions: output.Extensions}, nil
		}
		switch scenario {
		case "conflict":
			return contexty.LabelDecision{}, contexty.ErrLabelConflict
		case "upgrade":
			return contexty.LabelDecision{Extensions: inputs[0].Extensions, Upgrade: true}, nil
		case "cancel":
			cancel()
			return contexty.LabelDecision{}, contexty.ErrLabelConflict
		default:
			return contexty.LabelDecision{Extensions: inputs[0].Extensions}, nil
		}
	}
}

func fixtureNamedAppendBlock(t *testing.T, id, text string) (contexty.DeferredBlock, *int) {
	t.Helper()
	resolver, request, body := fixtureResourceFixture(t)
	request.ID = id
	reads := new(int)
	resolver.Reader = fixtureResourceReader(
		func(context.Context, contexty.ResourceReadRequest) (contexty.ResourceBody, error) {
			*reads++
			return body, nil
		},
	)
	resolver.Projection = fixtureResourcePolicy(
		func(_ context.Context, received contexty.ResourceBody) (contexty.ContextArtifact, error) {
			artifact := received.Artifact.Clone()
			artifact.ID = "shared"
			artifact.Payload = contexty.TextPayload(text)
			artifact.Lifecycle = contexty.ArtifactLifecyclePersistent
			artifact.MergePolicy = contexty.PolicyAppend
			return artifact, nil
		},
	)
	return fixtureAdapterResourceBlock(t, resolver, request), reads
}

func fixtureCombineResourceBlocks(first, second contexty.DeferredBlock) contexty.DeferredBlock {
	combined := first
	combined.Resources = append(combined.Resources, second.Resources...)
	combined.Resolve = func(ctx context.Context) (contexty.DeferredResult, error) {
		left, err := first.Resolve(ctx)
		if err != nil {
			return contexty.DeferredResult{}, err
		}
		right, err := second.Resolve(ctx)
		return contexty.DeferredResult{Resources: append(left.Resources, right.Resources...)}, err
	}
	return combined
}

func fixtureAppendSequenceEngine(blocks []contexty.DeferredBlock, strict bool) *contexty.Engine {
	trace := fixtureTraceProfile()
	trace.RequireOrigins = strict
	bindings := []contexty.RecordingComponent{fixtureBinding(contexty.RecordingResolver, "", "", 0)}
	if len(blocks) > 1 {
		bindings = append(bindings, fixtureBinding(contexty.RecordingResolver, "", "", 1))
	}
	return contexty.NewEngine(
		contexty.WithDeferredBlocks(blocks...),
		contexty.WithTraceProfile(trace),
		contexty.WithCompileRecording(fixtureBindings(fixtureRecordProfile("memory"), bindings...)),
		contexty.WithCompileContentCapture(
			contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent),
		),
	)
}

func fixtureAssertAppendSequenceReplay(
	t *testing.T,
	compiled contexty.CompileResult,
	accepted contexty.SavedCompileRecord,
) {
	t.Helper()
	expected, err := contexty.ReplayExpectationFor(accepted.Manifest)
	require.NoError(t, err)
	replayed, err := contexty.Replay(context.Background(), accepted, expected, contexty.DefaultJSONSerializer(),
		contexty.WithReplayResourceCodecs(map[string]contexty.ResourceCodec{
			"first": {
				Messages: contexty.DefaultJSONSerializer(),
			},
			"second": {Messages: contexty.DefaultJSONSerializer()},
		}))
	require.NoError(t, err)
	require.Equal(t, compiled.Artifacts, replayed.Artifacts)
	require.Equal(t, compiled.Payload.Memory, replayed.Outputs[0].Segments["memory"])
}

func fixtureAssertAppendReplay(t *testing.T, compiled contexty.CompileResult,
	accepted contexty.SavedCompileRecord, reads *int,
) {
	t.Helper()
	wire, err := contexty.EncodeSavedRecord(accepted)
	require.NoError(t, err)
	restored, err := contexty.DecodeSavedRecord(wire)
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(restored.Manifest)
	require.NoError(t, err)
	replayed, err := contexty.Replay(context.Background(), restored, expected, contexty.DefaultJSONSerializer(),
		contexty.WithReplayResourceCodecs(map[string]contexty.ResourceCodec{
			"resolve": {Messages: contexty.DefaultJSONSerializer()},
		}),
	)
	require.NoError(t, err)
	require.Equal(t, compiled.Artifacts, replayed.Artifacts)
	require.Equal(t, compiled.Payload.Memory, replayed.Outputs[0].Segments["memory"])
	require.Equal(t, 1, *reads)
}
