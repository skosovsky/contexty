package contexty_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestFinal_EstimatorOwnership(t *testing.T) {
	for _, target := range []bool{false, true} {
		t.Run(strconv.FormatBool(target), func(t *testing.T) {
			// Arrange: a host counter mutates its owned inputs after counting final content.
			counter := fixtureEvidenceEstimator{
				per: func(_ context.Context, messages []contexty.Message) ([]int, error) {
					return make([]int, len(messages)), nil
				},
				total: func(_ context.Context, messages []contexty.Message) (int, error) {
					if len(messages) == 1 && messages[0].TextContent() == "final" {
						messages[0].Parts[0] = contexty.TextPart{Text: "counter mutation"}
					}
					return len(messages), nil
				},
			}
			engine, request := fixtureFinalCancellationRequest(target, counter)
			// Act.
			result, err := engine.CompileSnapshot(context.Background(), request)
			// Assert: a final estimator cannot mutate the already verified representation.
			require.NoError(t, err)
			if target {
				require.Equal(t, "final", result.Projections["target"].Messages[0].TextContent())
				require.Equal(t, "input", result.Payload.History[0].TextContent())
			} else {
				require.Equal(t, "final", result.Payload.History[0].TextContent())
			}
			require.Equal(t, "input", result.Source.History[0].TextContent())
			require.Equal(t, "input", request.History[0].TextContent())
		})
	}
}

func TestInitial_EstimatorOwnership(t *testing.T) {
	// Arrange: a host counter must not rewrite the pipeline's working input.
	counter := fixtureEvidenceEstimator{total: func(_ context.Context, messages []contexty.Message) (int, error) {
		messages[0].Parts[0] = contexty.TextPart{Text: "counter mutation"}
		return 1, nil
	}}
	msg := contexty.TextMessage(contexty.RoleUser, "input")
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)}, counter)
	// Act.
	resultBudget, err := pipe.Apply(context.Background(), []contexty.Message{msg})
	result := resultBudget.Messages
	// Assert: both caller and returned working representation remain authoritative.
	require.NoError(t, err)
	require.Equal(t, "input", msg.TextContent())
	require.Equal(t, "input", result[0].TextContent())
}

func TestObserver_EstimatorOwnership(t *testing.T) {
	// Arrange: final checks and the subsequent passive telemetry count see owned copies.
	calls := 0
	counter := fixtureEvidenceEstimator{total: func(_ context.Context, messages []contexty.Message) (int, error) {
		if len(messages) == 1 && messages[0].TextContent() == "final" {
			calls++
			messages[0].Parts[0] = contexty.TextPart{Text: "observer count mutation"}
		}
		return len(messages), nil
	}}
	_, request := fixtureFinalCancellationRequest(false, counter)
	observer := &contexty.RecordingObserver{}
	engine := fixtureEngine(contexty.WithObserver(observer), contexty.WithBudgetPipeline(contexty.SegmentHistory,
		contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)}, counter)))
	// Act.
	result, err := engine.CompileSnapshot(context.Background(), request)
	// Assert: a passive count cannot mutate the final payload after budget admission.
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Equal(t, "final", result.Payload.History[0].TextContent())
	require.Equal(t, "input", result.Source.History[0].TextContent())
}
