package contexty_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestTransform_CallbackCancellation(t *testing.T) {
	for _, stage := range []string{"hook", "role", "segment", "target", "summarize"} {
		for _, hostError := range []bool{false, true} {
			t.Run(stage+"/"+strconv.FormatBool(hostError), func(t *testing.T) {
				// Arrange: cancellation and a host error occur in the same callback.
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var callbackError error
				if hostError {
					callbackError = contexty.ErrInvalidDescriptor
				}
				engine, request := fixtureCancelTransformFixture(stage, cancel, callbackError)
				// Act.
				result, err := engine.CompileSnapshot(ctx, request)
				// Assert: cancellation wins, and no partial result escapes.
				require.ErrorIs(t, err, context.Canceled)
				require.Zero(t, result)
			})
		}
	}
}

func TestRole_CallbackOwnership(t *testing.T) {
	// Arrange: the role-only port tries to modify content and provenance.
	input := fixtureRollingText("input", "safe")
	input.SourceRefs = []contexty.SourceRef{{ID: "source"}}
	engine := fixtureEngine(contexty.WithRoleProjectionPolicy(contexty.RoleProjectionFunc(
		func(message contexty.Message) (contexty.Role, error) {
			message.Parts[0] = contexty.TextPart{Text: "mutated"}
			message.SourceRefs[0].ID = "mutated"
			return contexty.RoleAssistant, nil
		})))
	// Act.
	result, err := engine.CompileSnapshot(
		context.Background(),
		contexty.CompileRequest{History: []contexty.Message{input}},
	)
	// Assert: only the returned role is authoritative.
	require.NoError(t, err)
	require.Equal(t, contexty.RoleAssistant, result.Payload.History[0].Role)
	require.Equal(t, "safe", result.Payload.History[0].TextContent())
	require.Equal(t, "source", result.Payload.History[0].SourceRefs[0].ID)
	require.Equal(t, "safe", input.TextContent())
}

func TestHook_CancellationStopsNext(t *testing.T) {
	// Arrange: a hook cancels successfully; a later hook must not run.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	first := fixtureDeferredHook(
		func(_ context.Context, snapshot contexty.ConversationSnapshot) (contexty.ConversationSnapshot, error) {
			cancel()
			return snapshot, nil
		},
	)
	second := fixtureDeferredHook(
		func(_ context.Context, snapshot contexty.ConversationSnapshot) (contexty.ConversationSnapshot, error) {
			calls++
			return snapshot, nil
		},
	)
	// Act.
	result, err := contexty.TransformPipeline(ctx, contexty.EmptySnapshot(), first, second)
	// Assert.
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, result)
	require.Zero(t, calls)
}

func TestSummarizer_CallbackOwnership(t *testing.T) {
	// Arrange: callback mutations must not rewrite the summarize input's lineage.
	input := fixtureRollingText("input", "long safe input")
	input.SourceRefs = []contexty.SourceRef{{ID: "source"}}
	original := fixtureRefForMessage(t, input)
	profile := fixtureTraceProfile()
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(4),
		Summarizer: stubSummarizer(func(_ context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
			messages := request.Messages
			fixtureMutateMessage(messages[0])
			return fixtureRollingText("summary", "sum"), nil
		})}, contexty.CharTokenEstimator{}, contexty.WithSummarizerDescriptor(profile.Stages["summarize"]))
	engine := fixtureEngine(
		contexty.WithTraceProfile(profile),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
	)
	// Act.
	result, err := engine.CompileSnapshot(
		context.Background(),
		contexty.CompileRequest{CompilationID: "owned-summary", History: []contexty.Message{input}},
	)
	// Assert: successful summary records the original input identity, not the host mutation.
	require.NoError(t, err)
	require.Equal(t, "sum", result.Payload.History[0].TextContent())
	require.Equal(t, "long safe input", result.Source.History[0].TextContent())
	require.Equal(t, "long safe input", input.TextContent())
	found := false
	for _, record := range result.Lineage.Records {
		if record.Stage == "summarize" {
			found = true
			require.Len(t, record.Inputs, 1)
			require.Equal(t, original.Digest, record.Inputs[0].Digest)
		}
	}
	require.True(t, found)
}
