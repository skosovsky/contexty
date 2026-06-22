package contexty_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestBudget_CounterCancellation(t *testing.T) {
	// Arrange: cancel simultaneously with an error in the first input estimate.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	counter := fixtureEvidenceEstimator{total: func(context.Context, []contexty.Message) (int, error) {
		cancel()
		return 0, contexty.ErrInvalidDescriptor
	}}
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)}, counter)
	// Act.
	result, err := pipe.Apply(ctx, []contexty.Message{contexty.TextMessage(contexty.RoleUser, "input")})
	// Assert.
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, result)
}

func TestReporter_CounterCancellation(t *testing.T) {
	for _, perMessage := range []bool{false, true} {
		t.Run(strconv.FormatBool(perMessage), func(t *testing.T) {
			// Arrange: cancellation dominates either callback's simultaneous host failure.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			counter := fixtureEvidenceEstimator{
				per: func(_ context.Context, messages []contexty.Message) ([]int, error) {
					if perMessage {
						cancel()
						return nil, contexty.ErrInvalidDescriptor
					}
					return make([]int, len(messages)), nil
				},
				total: func(context.Context, []contexty.Message) (int, error) {
					require.False(t, perMessage, "no following callback after per-message cancellation")
					cancel()
					return 0, contexty.ErrInvalidDescriptor
				},
			}
			reporter, err := contexty.NewEstimateReporter(
				counter,
				fixtureEstimateProfile(),
				contexty.DefaultJSONSerializer(),
			)
			require.NoError(t, err)
			msg := contexty.TextMessage(contexty.RoleUser, "input")
			msg.ID = "input"
			// Act.
			result, err := reporter.Report(ctx, contexty.EstimateRequest{
				Segments: []contexty.EstimateSegment{{Name: "input", Messages: []contexty.Message{msg}}},
				Budget:   contexty.EffectiveInputBudget(100),
			})
			// Assert.
			require.ErrorIs(t, err, context.Canceled)
			require.Zero(t, result)
		})
	}
}

func TestDrop_TailCounterCancellation(t *testing.T) {
	for _, hostError := range []bool{false, true} {
		t.Run(strconv.FormatBool(hostError), func(t *testing.T) {
			// Arrange: truncation recount cancels while returning a usable count or host error.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			counter := fixtureEvidenceEstimator{total: func(context.Context, []contexty.Message) (int, error) {
				cancel()
				if hostError {
					return 0, contexty.ErrInvalidDescriptor
				}
				return 0, nil
			}}
			messages := []contexty.Message{contexty.TextMessage(contexty.RoleUser, "a"),
				contexty.TextMessage(contexty.RoleUser, "b")}
			// Act.
			result, err := contexty.NewDropTailStrategy().Apply(ctx, messages, 2, 1, counter)
			// Assert: never return a canceled truncation as an accepted projection.
			require.ErrorIs(t, err, context.Canceled)
			require.Nil(t, result)
		})
	}
}
