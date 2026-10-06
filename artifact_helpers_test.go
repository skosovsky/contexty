package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func fixtureArtifactEstimateFixture(t *testing.T) (contexty.CompileResult, *int) {
	t.Helper()
	calls := new(int)
	counter := fixtureEvidenceEstimator{
		per: func(ctx context.Context, messages []contexty.Message) ([]int, error) {
			*calls++
			return contexty.CharTokenEstimator{}.EstimatePerMessage(ctx, messages)
		},
		total: func(ctx context.Context, messages []contexty.Message) (int, error) {
			*calls++
			return contexty.CharTokenEstimator{}.Estimate(ctx, messages)
		},
	}
	reporter, err := contexty.NewEstimateReporter(counter, fixtureEstimateProfile(), contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	admitted := contexty.NewMemoryBlock("admitted", contexty.TextPayload("abc")).ContextArtifact.
		WithBudget(contexty.ArtifactBudgetPolicy{TokenLimit: 3})
	excluded := contexty.NewMemoryBlock("excluded", contexty.TextPayload("12345")).ContextArtifact.
		WithBudget(contexty.ArtifactBudgetPolicy{TokenLimit: 2})
	inactive := contexty.NewRetrievalDocument("inactive", contexty.TextPayload("private")).
		ContextArtifact.WithTurn("other").
		WithBudget(contexty.ArtifactBudgetPolicy{TokenLimit: 1})
	unlimited := contexty.NewMemoryBlock("unlimited", contexty.TextPayload("tail")).ContextArtifact
	engine := fixtureEngine(
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureRecordProfile()),
		contexty.WithCompileContentCapture(
			contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent),
		),
		contexty.WithBudgetPipeline(
			contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)}, reporter),
		),
	)
	result, err := engine.CompileSnapshot(
		context.Background(),
		contexty.CompileRequest{CompilationID: "artifact-cost", TurnID: "current",
			Artifacts: []contexty.ContextArtifact{admitted, excluded, inactive, unlimited}},
	)
	require.NoError(t, err)
	return result, calls
}
