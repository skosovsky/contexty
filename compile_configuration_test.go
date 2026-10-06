package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestCompile_Configuration(t *testing.T) {
	// Arrange: resolver returns identical bytes under distinct secret variables.
	var observed []string
	blocks := []contexty.DeferredBlock{
		{},
		{Name: "selected", Resolve: func(ctx context.Context) (contexty.DeferredResult, error) {
			vars := contexty.CompileResolveVarFromContext(ctx)
			observed = append(observed, vars["secretKey"])
			vars["secretKey"] = "callback-mutated"
			return contexty.DeferredResult{Messages: []contexty.Message{fixtureRollingText("resolved", "safe")}}, nil
		}},
	}
	engine := fixtureEngine(
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(
			fixtureBindings(fixtureRecordProfile(), fixtureBinding(contexty.RecordingResolver, "", "", 1)),
		),
		contexty.WithDeferredBlocks(blocks...),
		contexty.WithCompileContentCapture(contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(func(context.Context, contexty.CaptureCandidate) (bool, error) { return true, nil })),
	)
	blocks[1].Name = "caller-mutated"
	compile := func(value string, reversed bool) contexty.CompileResult {
		options := []contexty.CompileOption{
			contexty.WithResolveVar("secretKey", value),
			contexty.WithResolveVar("other", "constant"),
		}
		if reversed {
			options[0], options[1] = options[1], options[0]
		}
		result, err := engine.CompileSnapshot(
			context.Background(),
			contexty.CompileRequest{CompilationID: "configuration", Options: options},
		)
		require.NoError(t, err)
		return result
	}
	// Act: compile distinct values and equivalent option order.
	first := compile("SECRET-RESOLVE-A", false)
	second := compile("SECRET-RESOLVE-B", false)
	reordered := compile("SECRET-RESOLVE-A", true)
	// Assert: config hashes differ without leaking values; placement/defaults and snapshots are exact.
	require.Equal(t, first.Payload, second.Payload)
	require.NotEqual(t, first.Manifest.CompileConfiguration.Options, second.Manifest.CompileConfiguration.Options)
	require.Equal(t, first.Manifest.Digest, reordered.Manifest.Digest)
	require.Equal(t, []string{"SECRET-RESOLVE-A", "SECRET-RESOLVE-B", "SECRET-RESOLVE-A"}, observed)
	require.Equal(t, []contexty.DeferredConfiguration{{Index: 1, Name: "selected", Segment: contexty.SegmentMemory,
		MergePolicy: contexty.PolicyAppend}}, first.Manifest.CompileConfiguration.Deferred)
	wire, err := contexty.EncodeManifest(*first.Manifest)
	require.NoError(t, err)
	require.NotContains(t, string(wire), "SECRET-RESOLVE")
	require.NotContains(t, string(wire), "secretKey")
	accepted, err := first.Record.Accept("host-accept")
	require.NoError(t, err)
	for _, field := range []string{"options", "segment", "policy", "name"} {
		t.Run(field, func(t *testing.T) {
			// Arrange/Act: change replay intent without executing a resolver.
			expected, copyErr := contexty.ReplayExpectationFor(*first.Manifest)
			require.NoError(t, copyErr)
			switch field {
			case "options":
				expected.CompileConfiguration.Options = second.Manifest.CompileConfiguration.Options
			case "segment":
				expected.CompileConfiguration.Deferred[0].Segment = contexty.SegmentSystem
			case "policy":
				expected.CompileConfiguration.Deferred[0].MergePolicy = contexty.PolicyReplaceByOrigin
			case "name":
				expected.CompileConfiguration.Deferred[0].Name = "changed"
			}
			replayed, replayErr := contexty.Replay(
				context.Background(),
				accepted,
				expected,
				contexty.DefaultJSONSerializer(),
			)
			// Assert.
			require.ErrorIs(t, replayErr, contexty.ErrReplayMismatch)
			require.Zero(t, replayed)
			require.Len(t, observed, 3)
			require.NoError(t, accepted.Validate())
		})
	}
	malformed, err := first.Manifest.Clone()
	require.NoError(t, err)
	malformed.CompileConfiguration.Deferred = nil
	require.ErrorIs(t, malformed.Validate(), contexty.ErrInvalidRecordingComponent)
}

func TestDeferred_ConfigurationFailures(t *testing.T) {
	for _, scenario := range []string{"segment", "policy"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: an active resolver cannot silently target an unknown segment/policy.
			calls, reads := 0, 0
			block := contexty.DeferredBlock{
				Name: "invalid",
				Resolve: func(context.Context) (contexty.DeferredResult, error) {
					calls++
					return contexty.DeferredResult{Messages: nil}, nil
				},
			}
			if scenario == "segment" {
				block.Segment = "unknown"
			} else {
				block.MergePolicy = "unknown"
			}
			store := fixtureComponentStoreProbe{
				ConversationStateStore: contexty.NewMemoryConversationStateStore(),
				reads:                  &reads,
			}
			engine := fixtureEngine(
				contexty.WithTraceProfile(fixtureTraceProfile()),
				contexty.WithDeferredBlocks(block),
				contexty.WithCompileRecording(
					fixtureBindings(fixtureRecordProfile(), fixtureBinding(contexty.RecordingResolver, "", "", 0)),
				),
				contexty.WithStateStore(store),
				contexty.WithConversationID("thread"),
			)
			// Act.
			compiled, err := engine.Compile(context.Background(), contexty.CompileRequest{CompilationID: scenario})
			// Assert: fail before callbacks, state load or partial output.
			require.ErrorIs(t, err, contexty.ErrInvalidCompileConfiguration)
			require.Zero(t, compiled)
			require.Zero(t, calls)
			require.Zero(t, reads)
		})
	}
}
