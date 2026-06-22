package contexty_test

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestRecording_Components(t *testing.T) {
	// Arrange/Act: callbacks are bound independently of aggregate pipeline identity.
	compiled, declared := fixtureComponentFixture(t)
	// Assert: every configured port has an explicit identity; actual edges use it.
	require.Len(t, compiled.Manifest.Profile.Components, 10)
	for _, binding := range declared.Components {
		require.Contains(t, compiled.Manifest.Profile.Components, binding)
		stage := map[contexty.RecordingComponentKind]string{contexty.RecordingHook: "hook", contexty.RecordingResolver: "deferred",
			contexty.RecordingSegmentFormatter: "format", contexty.RecordingRolePolicy: "role",
			contexty.RecordingTargetFormatter: "", contexty.RecordingLabelPolicy: "", contexty.RecordingTraceMapping: "",
			contexty.RecordingIdentityPolicy: "", contexty.RecordingViewRenderer: ""}[binding.Key.Kind]
		if stage == "" {
			continue
		}
		found := false
		for _, record := range compiled.Lineage.Records {
			if record.Stage == stage && record.Transform == binding.Descriptor {
				found = true
			}
		}
		require.True(t, found, "missing actual component edge: %+v", binding)
	}
	require.Equal(t, fixtureBinding(contexty.RecordingViewRenderer, "xml", "", 0).Descriptor,
		compiled.Projections["xml"].Rendered.Renderer)
	formatterFound := false
	for _, record := range compiled.Projections["formatted"].Lineage.Records {
		if record.Transform == fixtureBinding(contexty.RecordingTargetFormatter, "formatted", "", 0).Descriptor {
			formatterFound = true
		}
	}
	require.True(t, formatterFound)
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(accepted.Manifest)
	require.NoError(t, err)
	replayed, err := contexty.Replay(context.Background(), accepted, expected, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	require.Equal(t, compiled.Payload.History, replayed.Outputs[0].Segments["history"])
	slices.Reverse(expected.Profile.Components)
	reordered, err := contexty.Replay(context.Background(), accepted, expected, contexty.DefaultJSONSerializer())
	require.NoError(t, err, "caller binding order is not an execution change")
	require.Equal(t, replayed.Outputs, reordered.Outputs)
	for index := range expected.Profile.Components {
		changed, copyErr := contexty.ReplayExpectationFor(accepted.Manifest)
		require.NoError(t, copyErr)
		changed.Profile.Components[index].Descriptor.Revision = "changed"
		failed, replayErr := contexty.Replay(context.Background(), accepted, changed, contexty.DefaultJSONSerializer())
		require.ErrorIs(t, replayErr, contexty.ErrReplayMismatch)
		require.Zero(t, failed)
		require.Equal(t, "pinned", accepted.Manifest.Profile.Components[index].Descriptor.Revision)
	}
}

func TestRecordingComponent_CanonicalOrder(t *testing.T) {
	// Arrange: identical slot bindings supplied in different caller order.
	profile := fixtureBindings(fixtureRecordProfile(), fixtureBinding(contexty.RecordingHook, "", "", 0),
		fixtureBinding(contexty.RecordingHook, "", "", 1))
	options := []contexty.EngineOption{contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithTransformHooks(contexty.NewRedactionHook(), contexty.NewRedactionHook())}
	request := contexty.CompileRequest{
		CompilationID: "canonical-components",
		History:       []contexty.Message{fixtureRollingText("m", "input")},
	}
	// Act.
	firstOptions := append(slices.Clone(options), contexty.WithCompileRecording(profile))
	first, err := contexty.NewEngine(firstOptions...).CompileSnapshot(context.Background(), request)
	require.NoError(t, err)
	slices.Reverse(profile.Components)
	secondOptions := append(slices.Clone(options), contexty.WithCompileRecording(profile))
	second, err := contexty.NewEngine(secondOptions...).CompileSnapshot(context.Background(), request)
	// Assert: canonical identity and graph are independent of binding list order.
	require.NoError(t, err)
	require.Equal(t, first.Manifest.Digest, second.Manifest.Digest)
	require.Equal(t, first.Lineage, second.Lineage)
	// Arrange/Act/Assert: a malformed persisted key is rejected by manifest validation.
	malformed, err := first.Manifest.Clone()
	require.NoError(t, err)
	malformed.Profile.Components[0].Key.Target = "unexpected-scope"
	require.ErrorIs(t, malformed.Validate(), contexty.ErrInvalidRecordingComponent)
}

func TestRecordingComponent_Failures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		bindings []contexty.RecordingComponent
	}{
		{name: "missing"},
		{name: "wrong slot", bindings: []contexty.RecordingComponent{fixtureBinding(contexty.RecordingHook, "", "", 1)}},
		{name: "extra", bindings: []contexty.RecordingComponent{fixtureBinding(contexty.RecordingHook, "", "", 0), fixtureBinding(contexty.RecordingResolver, "", "", 0)}},
		{name: "duplicate", bindings: []contexty.RecordingComponent{fixtureBinding(contexty.RecordingHook, "", "", 0), fixtureBinding(contexty.RecordingHook, "", "", 0)}},
		{name: "invalid scope", bindings: []contexty.RecordingComponent{fixtureBinding(contexty.RecordingHook, "target", "", 0)}},
		{name: "unknown kind", bindings: []contexty.RecordingComponent{fixtureBinding("unknown", "", "", 0)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: invalid topology must never reach a host callback.
			calls := 0
			reads := 0
			store := fixtureComponentStoreProbe{
				ConversationStateStore: contexty.NewMemoryConversationStateStore(),
				reads:                  &reads,
			}
			profile := fixtureRecordProfile()
			profile.Components = slices.Clone(tc.bindings)
			engine := contexty.NewEngine(
				contexty.WithTraceProfile(fixtureTraceProfile()),
				contexty.WithCompileRecording(profile),
				contexty.WithStateStore(store), contexty.WithConversationID("thread"),
				contexty.WithTransformHooks(
					contexty.RedactionHook{Replacer: func(text string) string { calls++; return text }},
				),
			)
			// Act.
			result, err := engine.Compile(
				context.Background(),
				contexty.CompileRequest{
					CompilationID: "invalid",
					History:       []contexty.Message{fixtureRollingText("m", "input")},
				},
			)
			// Assert.
			require.ErrorIs(t, err, contexty.ErrInvalidRecordingComponent)
			require.Nil(t, result.Manifest)
			require.Zero(t, calls)
			require.Zero(t, reads)
		})
	}
}
