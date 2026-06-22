package contexty_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestArtifact_CommonBudget(t *testing.T) {
	for _, tc := range []struct {
		name     string
		limit    int
		cost     int
		selected bool
		wantErr  error
	}{
		{name: "host cost exceeds rune count", limit: 3, cost: 10},
		{name: "host cost below rune count", limit: 1, cost: 1, selected: true},
		{name: "zero is an actual limit", limit: 0, cost: 1},
		{name: "zero cost within zero limit", limit: 0, cost: 0, selected: true},
		{name: "negative limit", limit: -1, cost: 1, wantErr: contexty.ErrInvalidBudgetRequest},
		{name: "negative estimate", limit: 1, cost: -1, wantErr: contexty.ErrTokenCountFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: the host estimator deliberately differs from rune counting.
			artifact := contexty.NewMemoryBlock("a", contexty.TextPayload("body")).ContextArtifact.
				WithBudget(contexty.ArtifactBudgetPolicy{TokenLimit: tc.limit})
			counter := fixtureEvidenceEstimator{
				per: func(_ context.Context, messages []contexty.Message) ([]int, error) {
					costs := make([]int, len(messages))
					for i := range costs {
						costs[i] = tc.cost
					}
					return costs, nil
				},
				total: func(_ context.Context, messages []contexty.Message) (int, error) {
					return len(messages) * tc.cost, nil
				},
			}
			engine := contexty.NewEngine(contexty.WithBudgetPipeline(contexty.SegmentHistory,
				contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)}, counter)))
			// Act.
			result, err := engine.CompileSnapshot(
				context.Background(),
				contexty.CompileRequest{Artifacts: []contexty.ContextArtifact{artifact}},
			)
			// Assert.
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Zero(t, result)
				return
			}
			require.NoError(t, err)
			require.Len(t, result.ArtifactEstimates, 1)
			require.Equal(t, tc.cost, result.ArtifactEstimates[0].Tokens)
			require.Equal(t, tc.limit, result.ArtifactEstimates[0].TokenLimit)
			require.Equal(t, contexty.EstimateEstimated, result.ArtifactEstimates[0].Quality)
			require.Nil(t, result.ArtifactEstimates[0].Report)
			require.Equal(t, tc.selected, len(result.Artifacts) == 1)
			require.Equal(t, tc.selected, len(result.Payload.Memory) == 1)
			require.Len(t, result.Source.Artifacts, 1)
			require.Equal(t, "body", result.Source.Artifacts[0].Payload.Text)
		})
	}
}

func TestArtifact_EstimateFailure(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "counter error", true: "counter cancellation"}[cancel], func(t *testing.T) {
			// Arrange.
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			sentinel := errors.New("counter unavailable")
			counter := fixtureEvidenceEstimator{total: func(context.Context, []contexty.Message) (int, error) {
				if cancel {
					stop()
					return 1, nil
				}
				return 0, sentinel
			}}
			engine := contexty.NewEngine(contexty.WithBudgetPipeline(contexty.SegmentHistory,
				contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)}, counter)))
			artifact := contexty.NewMemoryBlock("a", contexty.TextPayload("body")).ContextArtifact.
				WithBudget(contexty.ArtifactBudgetPolicy{TokenLimit: 10})
			// Act.
			result, err := engine.CompileSnapshot(
				ctx,
				contexty.CompileRequest{Artifacts: []contexty.ContextArtifact{artifact}},
			)
			// Assert.
			require.Zero(t, result)
			if cancel {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, sentinel)
				require.ErrorIs(t, err, contexty.ErrTokenCountFailed)
			}
		})
	}
}

func TestArtifact_ExclusionEvidence(t *testing.T) {
	// Arrange: one-rune body has a host cost greater than its local limit.
	calls := 0
	counter := fixtureEvidenceEstimator{total: func(_ context.Context, messages []contexty.Message) (int, error) {
		if len(messages) == 1 && messages[0].ID == "artifact:a" {
			calls++
			return 10, nil
		}
		return 0, nil
	}}
	artifact := contexty.NewMemoryBlock("a", contexty.TextPayload("x")).ContextArtifact.
		WithBudget(contexty.ArtifactBudgetPolicy{TokenLimit: 2})
	engine := contexty.NewEngine(contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureRecordProfile()),
		contexty.WithBudgetPipeline(contexty.SegmentHistory,
			contexty.NewBudgetPipeline(
				contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)}, counter,
				contexty.WithEstimatorDescriptor(contexty.Descriptor{ID: "host/artifact-estimate", Revision: "pinned"}),
			)))
	// Act.
	result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		CompilationID: "artifact-budget", Artifacts: []contexty.ContextArtifact{artifact}})
	// Assert: manifest records the actual exclusion, without a second estimate.
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Empty(t, result.Artifacts)
	require.Len(t, result.Manifest.ExcludedArtifacts, 1)
	require.Equal(t, contexty.ReasonTokenBudgetExceeded, result.Manifest.ExcludedArtifacts[0].Reason)
	require.NoError(t, result.Manifest.Validate())
}
