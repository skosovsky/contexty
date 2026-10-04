package contexty_test

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestTrace_Configuration(t *testing.T) {
	// Arrange: registries and configuration remain caller-owned and mutable.
	registry := contexty.NewExtensionRegistry()
	registry.Register("fixture-label", func(data []byte) (contexty.Extension, error) {
		return fixtureWireExtension{wire: string(data)}, nil
	})
	trace := fixtureTraceProfile()
	trace.RequireOrigins = true
	trace.Codec.Extensions = registry
	trace.Codecs = []contexty.CodecBinding{fixtureCodecBinding(contexty.CodecLabel, "fixture-label"),
		fixtureCodecBinding(contexty.CodecExtension, "fixture-label")}
	trace.Labels = contexty.LabelProjection{
		Registry:      registry,
		RequiredTypes: []string{"fixture-label", "fixture-label"},
		Policy: fixtureLabelPolicy(func(context.Context, []contexty.Message, contexty.Message,
			contexty.Descriptor) (contexty.LabelDecision, error) {
			return contexty.LabelDecision{}, nil
		}),
	}
	profile := fixtureBindings(fixtureRecordProfile(), fixtureBinding(contexty.RecordingLabelPolicy, "", "", 0))
	options := []contexty.EngineOption{contexty.WithCompileRecording(profile),
		contexty.WithCompileContentCapture(contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(func(context.Context, contexty.CaptureCandidate) (bool, error) { return true, nil }))}
	first := fixtureEngine(append(slices.Clone(options), contexty.WithTraceProfile(trace))...)
	slices.Reverse(trace.Codecs)
	second := fixtureEngine(append(slices.Clone(options), contexty.WithTraceProfile(trace))...)
	trace.Codecs[0].Descriptor.Revision = "mutated"
	trace.Labels.RequiredTypes[0] = "mutated"
	registry.Register("late", func(data []byte) (contexty.Extension, error) {
		return fixtureWireExtension{wire: string(data)}, nil
	})
	request := contexty.CompileRequest{CompilationID: "configuration", RequireDurableIdentity: true}
	// Act: no decoder execution is necessary for this empty request.
	compiled, err := first.CompileSnapshot(context.Background(), request)
	require.NoError(t, err)
	reordered, err := second.CompileSnapshot(context.Background(), request)
	// Assert: all configured decoders are pinned, order is canonical, mutations are isolated.
	require.NoError(t, err)
	require.Equal(t, compiled.Manifest.Digest, reordered.Manifest.Digest)
	config := compiled.Manifest.TraceConfiguration
	require.True(t, config.RequireOrigins)
	require.True(t, config.RequireDurableIdentity)
	require.Equal(t, []string{"fixture-label"}, config.RequiredLabelTypes)
	require.Len(t, config.Codecs, 4)
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	reorderedIntent, err := contexty.ReplayExpectationFor(*compiled.Manifest)
	require.NoError(t, err)
	slices.Reverse(reorderedIntent.TraceConfiguration.Codecs)
	reorderedIntent.TraceConfiguration.RequiredLabelTypes = append(
		reorderedIntent.TraceConfiguration.RequiredLabelTypes, "fixture-label")
	replayed, err := contexty.Replay(context.Background(), accepted, reorderedIntent, contexty.DefaultJSONSerializer())
	require.NoError(t, err, "codec list order and duplicate required types are not execution changes")
	require.Equal(t, config, replayed.Manifest.TraceConfiguration)
	for _, scenario := range []string{"origins", "identity", "labels", "codec"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange/Act: change one aspect of current replay intent.
			expected, copyErr := contexty.ReplayExpectationFor(*compiled.Manifest)
			require.NoError(t, copyErr)
			switch scenario {
			case "origins":
				expected.TraceConfiguration.RequireOrigins = false
			case "identity":
				expected.TraceConfiguration.RequireDurableIdentity = false
			case "labels":
				expected.TraceConfiguration.RequiredLabelTypes[0] = "changed"
			case "codec":
				expected.TraceConfiguration.Codecs[0].Descriptor.Revision = "changed"
			}
			replayed, replayErr := contexty.Replay(
				context.Background(),
				accepted,
				expected,
				contexty.DefaultJSONSerializer(),
			)
			// Assert: identical bytes do not allow reuse under different contract intent.
			require.ErrorIs(t, replayErr, contexty.ErrReplayMismatch)
			require.Zero(t, replayed)
			require.NoError(t, accepted.Validate())
		})
	}
}

func TestCodec_BindingFailures(t *testing.T) {
	for _, scenario := range []string{"missing", "duplicate", "extra", "wrong-kind", "nil-decoder", "required-missing"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: invalid decoder topology must fail before callbacks and state load.
			calls, reads := 0, 0
			registry := contexty.NewExtensionRegistry()
			if scenario == "nil-decoder" {
				registry.Register("fixture-label", nil)
			} else {
				registry.Register("fixture-label", func(data []byte) (contexty.Extension, error) {
					calls++
					return fixtureWireExtension{wire: string(data)}, nil
				})
			}
			trace := fixtureTraceProfile()
			trace.Codec.Extensions = registry
			trace.Codecs = []contexty.CodecBinding{fixtureCodecBinding(contexty.CodecExtension, "fixture-label")}
			switch scenario {
			case "missing":
				trace.Codecs = nil
			case "duplicate":
				trace.Codecs = append(trace.Codecs, trace.Codecs[0])
			case "extra":
				trace.Codecs = append(trace.Codecs, fixtureCodecBinding(contexty.CodecExtension, "absent"))
			case "wrong-kind":
				trace.Codecs[0].Kind = contexty.CodecLabel
			case "required-missing":
				trace.Labels.RequiredTypes = []string{"absent"}
			}
			store := fixtureComponentStoreProbe{
				ConversationStateStore: contexty.NewMemoryConversationStateStore(),
				reads:                  &reads,
			}
			engine := fixtureEngine(
				contexty.WithTraceProfile(trace),
				contexty.WithCompileRecording(fixtureRecordProfile()),
				contexty.WithStateStore(store),
				contexty.WithConversationID("thread"),
			)
			// Act.
			compiled, err := engine.Compile(context.Background(), contexty.CompileRequest{CompilationID: scenario})
			// Assert.
			require.Error(t, err)
			require.Zero(t, compiled)
			require.Zero(t, calls)
			require.Zero(t, reads)
		})
	}
}
