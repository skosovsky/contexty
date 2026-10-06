package contexty_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestAcceptance_IndependentPreparedOutputs(t *testing.T) {
	t.Parallel()
	// Arrange: main can retain only one history message; consumers select their own windows.
	history := []contexty.Message{
		{ID: "old", Role: contexty.RoleUser, Parts: []contexty.ContentPart{contexty.TextPart{Text: "old context"}}},
		{ID: "new", Role: contexty.RoleUser, Parts: []contexty.ContentPart{contexty.TextPart{Text: "new context"}}},
	}
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(5)},
		&contexty.FixedEstimator{TokensPerMessage: 5},
	)
	engine := fixtureEngine(contexty.WithBudgetPipeline(pipe))
	// Act.
	result, err := engine.CompileSnapshot(t.Context(), contexty.CompileRequest{History: history,
		Targets: []contexty.CompileTarget{
			{Name: "wide", Segments: []contexty.SegmentName{contexty.SegmentHistory}},
			{Name: "empty", Segments: []contexty.SegmentName{contexty.SegmentTools}},
		}})
	// Assert.
	require.NoError(t, err)
	require.Len(t, result.Payload.History, 1)
	require.Len(t, result.Projections["wide"].Messages, 2)
	require.Empty(t, result.Projections["empty"].Messages)
	require.Len(t, result.Projections["wide"].InputSnapshot.Segment(contexty.SegmentHistory), 2)
}

func fixtureArtifactContentRef(t *testing.T, a contexty.ContextArtifact) contexty.ContentRef {
	t.Helper()
	ref, err := contexty.ArtifactContentRef(a)
	require.NoError(t, err)
	return ref
}
