package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

type fixtureComponentStoreProbe struct {
	contexty.ConversationStateStore

	reads *int
}

func (s fixtureComponentStoreProbe) LoadState(ctx context.Context, id string) (contexty.ConversationState, error) {
	*s.reads++
	return s.ConversationStateStore.LoadState(ctx, id)
}

func fixtureComponentFixture(t *testing.T) (contexty.CompileResult, contexty.RecordProfile) {
	t.Helper()
	profile := fixtureBindings(fixtureRecordProfile("formatted", "xml"),
		fixtureBinding(contexty.RecordingHook, "", "", 0), fixtureBinding(contexty.RecordingHook, "", "", 1),
		fixtureBinding(contexty.RecordingResolver, "", "", 0),
		fixtureBinding(contexty.RecordingSegmentFormatter, "", contexty.SegmentHistory, 0),
		fixtureBinding(contexty.RecordingTargetFormatter, "formatted", "", 0),
		fixtureBinding(contexty.RecordingViewRenderer, "xml", "", 0),
		fixtureBinding(contexty.RecordingRolePolicy, "", "", 0),
		fixtureBinding(contexty.RecordingLabelPolicy, "", "", 0),
		fixtureBinding(contexty.RecordingTraceMapping, "", "", 0),
		fixtureBinding(contexty.RecordingIdentityPolicy, "", "", 0))
	trace := fixtureTraceProfile()
	trace.Mapping = func(context.Context, string, []contexty.Message, []contexty.Message) (map[string][]contexty.ContentRef, error) {
		return map[string][]contexty.ContentRef{}, nil
	}
	trace.Labels.Policy = fixtureLabelPolicy(
		func(context.Context, []contexty.Message, contexty.Message, contexty.Descriptor) (contexty.LabelDecision, error) {
			return contexty.LabelDecision{}, nil
		},
	)
	formatter := func(_ context.Context, messages []contexty.Message) ([]contexty.Message, error) {
		messages[0].Parts = []contexty.ContentPart{contexty.TextPart{Text: "formatted"}}
		return messages, nil
	}
	engine := fixtureEngine(
		contexty.WithTraceProfile(trace),
		contexty.WithCompileRecording(profile),
		contexty.WithCompileContentCapture(
			contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent),
		),
		contexty.WithTransformHooks(fixtureTextTransform{Replacer: func(text string) string { return text + "!" }},
			fixtureTextTransform{Replacer: func(text string) string { return text + "?" }}),
		contexty.WithSegmentFormatter(contexty.SegmentHistory, formatter),
		contexty.WithRoleProjectionPolicy(
			contexty.RoleProjectionFunc(
				func(contexty.Message) (contexty.Role, error) { return contexty.RoleUser, nil },
			),
		),
		contexty.WithDeferredBlocks(
			contexty.DeferredBlock{Name: "body", Resolve: func(context.Context) (contexty.DeferredResult, error) {
				return contexty.DeferredResult{Messages: []contexty.Message{fixtureRollingText("dep", "resolved")}}, nil
			}},
		),
	)
	// Mutating caller configuration after installing it cannot change recordings.
	profile.Components[0].Descriptor.Revision = "caller-mutated"
	result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		CompilationID: "components",
		History: []contexty.Message{
			fixtureRollingText("m", "input"),
		},
		IdentityPolicy: contexty.NewStableMessageIdentityPolicy("host"),
		Targets: []contexty.CompileTarget{
			{Segments: []contexty.SegmentName{contexty.SegmentHistory}, Name: "formatted", Formatter: formatter},
			{Name: "xml", View: string(contexty.ViewLLMXML)},
		},
	})
	require.NoError(t, err)
	profile.Components[0].Descriptor.Revision = "pinned"
	return result, profile
}
