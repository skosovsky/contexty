package contexty

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReplay_AppendBudgetBinding(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		budget *ArtifactBudgetPolicy
		limit  *int
		want   error
	}{
		{"unlimited", nil, nil, nil},
		{"actual-local-budget", &ArtifactBudgetPolicy{TokenLimit: 4}, new(4), nil},
		{"missing-local-budget", &ArtifactBudgetPolicy{TokenLimit: 4}, nil, ErrMissingEstimateReport},
		{"changed-local-limit", &ArtifactBudgetPolicy{TokenLimit: 4}, new(5), ErrInvalidEstimateReport},
		{"invented-local-budget", nil, new(0), ErrInvalidEstimateReport},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange: derive actual ref from decoded typed artifact, not caller-supplied metadata.
			artifact := NewMemoryBlock("projected", TextPayload("safe")).ContextArtifact
			artifact.Budget = scenario.budget
			ref, err := ArtifactContentRef(artifact)
			require.NoError(t, err)
			var manifest CompileManifest
			if scenario.limit != nil {
				manifest.ArtifactBudgets = []ArtifactBudgetRequest{{Input: ref, TokenLimit: *scenario.limit}}
			}
			// Act / Assert: accepted metadata cannot silently replace the actual local budget.
			err = validateReplayAppendBudget(manifest, artifact)
			if scenario.want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, scenario.want)
			}
		})
	}
}
