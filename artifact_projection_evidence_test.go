package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func fixtureArtifactProjectionEvidence(t *testing.T) contexty.CompileResult {
	t.Helper()
	artifact := contexty.NewMemoryBlock("document", contexty.TextPayload("data")).WithBudget(
		contexty.ArtifactBudgetPolicy{TokenLimit: 10})
	mainProfile := fixtureEstimateProfile()
	targetProfile := fixtureEstimateProfile()
	targetProfile.Model = contexty.Descriptor{ID: "consumer-model", Revision: "1"}
	targetProfile.Estimator = contexty.Descriptor{ID: "consumer-estimator", Revision: "1"}
	mainReporter, err := contexty.NewEstimateReporter(&contexty.FixedEstimator{TokensPerMessage: 20}, mainProfile,
		contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	targetReporter, err := contexty.NewEstimateReporter(&contexty.FixedEstimator{TokensPerMessage: 5}, targetProfile,
		contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	engine := fixtureEngine(
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureRecordProfile("consumer")),
		contexty.WithBudgetPipeline(contexty.NewBudgetPipeline(
			contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)}, mainReporter)),
	)
	result, err := engine.CompileSnapshot(t.Context(), contexty.CompileRequest{
		CompilationID: "per-output-evidence", Artifacts: []contexty.ContextArtifact{artifact},
		Targets: []contexty.CompileTarget{
			{
				Name:             "consumer",
				IncludeArtifacts: true,
				Budget: contexty.NewBudgetPipeline(
					contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)},
					targetReporter,
				),
			},
		},
	})
	require.NoError(t, err)
	return result
}

func TestAcceptance_ArtifactEvidenceIndependentProfiles(t *testing.T) {
	t.Parallel()
	// Arrange: the same candidate fails the main cap and fits a consumer with another model.
	result := fixtureArtifactProjectionEvidence(t)
	// Act.
	err := result.Manifest.Validate()
	// Assert.
	require.NoError(t, err)
	require.Empty(t, result.Manifest.Artifacts)
	require.Len(t, result.Manifest.Outputs[1].ArtifactRefs, 1)
	require.Equal(t, 20, result.Manifest.ArtifactEstimates[0].Tokens)
	require.Equal(t, 5, result.Manifest.Outputs[1].ArtifactEstimates[0].Tokens)
	require.NotEqual(t, result.Manifest.ArtifactEstimates[0].Report.Profile.Model,
		result.Manifest.Outputs[1].ArtifactEstimates[0].Report.Profile.Model)
}

func TestAcceptance_ArtifactEvidenceRejectsTargetTampering(t *testing.T) {
	t.Parallel()
	result := fixtureArtifactProjectionEvidence(t)
	for _, scenario := range []struct {
		name   string
		mutate func(*contexty.CompileManifest)
		want   error
	}{
		{name: "missing", mutate: func(m *contexty.CompileManifest) { m.Outputs[1].ArtifactEstimates = nil }, want: contexty.ErrMissingEstimateReport},
		{name: "duplicate", mutate: func(m *contexty.CompileManifest) {
			m.Outputs[1].ArtifactEstimates = append(m.Outputs[1].ArtifactEstimates, m.Outputs[1].ArtifactEstimates[0])
		}, want: contexty.ErrInvalidEstimateReport},
		{name: "foreign input", mutate: func(m *contexty.CompileManifest) { m.Outputs[1].ArtifactEstimates[0].Input.ID = "foreign" }, want: contexty.ErrInvalidEstimateReport},
		{name: "cap", mutate: func(m *contexty.CompileManifest) { m.Outputs[1].ArtifactEstimates[0].TokenLimit++ }, want: contexty.ErrInvalidEstimateReport},
		{name: "main report in target", mutate: func(m *contexty.CompileManifest) {
			m.Outputs[1].ArtifactEstimates[0].Report = m.ArtifactEstimates[0].Report
		}, want: contexty.ErrStaleEstimate},
		{name: "foreign exclusion", mutate: func(m *contexty.CompileManifest) {
			m.Outputs[1].ExcludedArtifacts = []contexty.ArtifactExclusion{{Input: contexty.ContentRef{ID: "foreign", Digest: "digest"}, Reason: "not_selected"}}
		}, want: contexty.ErrInvalidCoverage},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			// Arrange.
			manifest, err := result.Manifest.Clone()
			require.NoError(t, err)
			scenario.mutate(&manifest)
			// Act.
			err = manifest.Validate()
			// Assert.
			require.ErrorIs(t, err, scenario.want)
		})
	}
}

func TestAcceptance_ArtifactGlobalPackingBelowLocalCap(t *testing.T) {
	t.Parallel()
	// Arrange: the artifact fits its local cap, but history has higher priority in a one-unit output.
	artifact := contexty.NewMemoryBlock("document", contexty.TextPayload("data")).WithBudget(
		contexty.ArtifactBudgetPolicy{TokenLimit: 10})
	policy := contexty.SelectionPolicy{Identity: contexty.Descriptor{ID: "host/priority", Revision: "1"},
		Select: func(_ context.Context, candidates []contexty.ContextCandidate) ([]contexty.SelectionChoice, error) {
			plan := make([]contexty.SelectionChoice, 0, len(candidates))
			for _, candidate := range candidates {
				priority := 0
				if candidate.Segment == contexty.SegmentHistory {
					priority = 10
				}
				plan = append(plan, contexty.SelectionChoice{Ref: candidate.Ref, Priority: priority})
			}
			return plan, nil
		}}
	engine := fixtureEngine(contexty.WithSelectionPolicy(policy),
		contexty.WithBudgetPipeline(contexty.NewBudgetPipeline(
			contexty.BudgetConfig{
				Budget: contexty.EffectiveInputBudget(1),
			},
			&contexty.FixedEstimator{TokensPerMessage: 1},
		)),
		contexty.WithTraceProfile(fixtureTraceProfile()), contexty.WithCompileRecording(fixtureRecordProfile()))
	// Act.
	result, err := engine.CompileSnapshot(t.Context(), contexty.CompileRequest{
		CompilationID: "global-artifact-cap",
		History: []contexty.Message{
			fixtureRollingText("history", "keep"),
		},
		Artifacts: []contexty.ContextArtifact{artifact},
	})
	// Assert.
	require.NoError(t, err)
	require.Empty(t, result.Artifacts)
	require.Equal(t, 1, result.Manifest.ArtifactEstimates[0].Tokens)
	require.Equal(t, contexty.ReasonTokenBudgetExceeded, result.Manifest.ExcludedArtifacts[0].Reason)
	require.NoError(t, result.Manifest.Validate())
}
