package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestEstimator_Configuration(t *testing.T) {
	for _, reporterBacked := range []bool{false, true} {
		t.Run(map[bool]string{false: "integer", true: "reporter"}[reporterBacked], func(t *testing.T) {
			// Arrange: caller parameters are mutable; constructed counters must own copies.
			counter := &contexty.FixedEstimator{TokensPerMessage: 2, TokensPerContentPart: 3}
			var estimator contexty.TokenEstimator = counter
			if reporterBacked {
				reporter, err := contexty.NewEstimateReporter(
					counter, fixtureEstimateProfile(), contexty.DefaultJSONSerializer(),
				)
				require.NoError(t, err)
				estimator = reporter
			}
			pipe := contexty.NewBudgetPipeline(
				contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)},
				estimator,
			)
			counter.TokensPerMessage = 999
			// Act.
			compiled, err := fixtureTruncationEngine(pipe).CompileSnapshot(context.Background(),
				contexty.CompileRequest{
					CompilationID: "fixed",
					History:       []contexty.Message{fixtureRollingText("m", "abc")},
				})
			// Assert: actual count, identity, codec and independent replay expectation agree.
			require.NoError(t, err)
			budget := compiled.Manifest.Budgets[0]
			require.Equal(t, 5, budget.EstimatedTokens)
			require.Equal(t, 2, budget.Estimator.Fixed.PerMessage)
			require.Equal(t, 3, budget.Estimator.Fixed.PerPart)
			accepted, err := compiled.Record.Accept("host-accept")
			require.NoError(t, err)
			expected, err := contexty.ReplayExpectationFor(*compiled.Manifest)
			require.NoError(t, err)
			expected.Budgets[0].Estimator.Fixed.PerMessage++
			replayed, err := contexty.Replay(context.Background(), accepted, expected, contexty.DefaultJSONSerializer())
			require.ErrorIs(t, err, contexty.ErrReplayMismatch)
			require.Zero(t, replayed)
			require.NoError(t, accepted.Validate())
		})
	}
}

func TestCharacter_EstimatorIdentity(t *testing.T) {
	// Arrange: effective default non-text weight and ratio are pinned.
	counter := &contexty.CharFallbackEstimator{CharsPerToken: 2}
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)}, counter)
	counter.CharsPerToken = 1
	// Act.
	compiled, err := fixtureTruncationEngine(pipe).CompileSnapshot(context.Background(),
		contexty.CompileRequest{
			CompilationID: "character",
			History:       []contexty.Message{fixtureRollingText("m", "abc")},
		})
	// Assert: no caller mutation and no zero-valued default ambiguity.
	require.NoError(t, err)
	budget := compiled.Manifest.Budgets[0]
	require.Equal(t, 2, budget.EstimatedTokens)
	require.Equal(t, 2, budget.Estimator.Character.CharsPerToken)
	require.Equal(t, contexty.DefaultTokensPerNonTextPart, budget.Estimator.Character.NonTextWeight)
	malformed, err := compiled.Manifest.Clone()
	require.NoError(t, err)
	malformed.Budgets[0].Estimator.Character = nil
	require.ErrorIs(t, malformed.Validate(), contexty.ErrInvalidRecordingComponent)
}

func TestEstimator_HostBinding(t *testing.T) {
	for _, scenario := range []string{"missing", "reserved", "explicit", "tool-missing", "tool-explicit"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: custom integer/tool estimators cannot infer their own identity.
			calls := 0
			var counter contexty.TokenEstimator = fixtureEvidenceEstimator{
				total: func(context.Context, []contexty.Message) (int, error) { calls++; return 0, nil },
			}
			if scenario == "tool-missing" || scenario == "tool-explicit" {
				counter = &contexty.CharFallbackEstimator{CharsPerToken: 4,
					EstimateTool: func(contexty.ToolCallPart) int { calls++; return 1 }}
			}
			var options []contexty.BudgetPipelineOption
			if scenario == "explicit" || scenario == "tool-explicit" || scenario == "reserved" {
				descriptor := contexty.Descriptor{ID: "host/counter", Revision: "pinned"}
				if scenario == "reserved" {
					descriptor = contexty.Descriptor{ID: "contexty/estimate/characters", Revision: "contract"}
				}
				options = append(options, contexty.WithEstimatorDescriptor(descriptor))
			}
			// Act.
			compiled, err := fixtureTruncationEngine(contexty.NewBudgetPipeline(
				contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)}, counter, options...)).
				CompileSnapshot(context.Background(), contexty.CompileRequest{CompilationID: scenario})
			// Assert.
			if scenario == "explicit" || scenario == "tool-explicit" {
				require.NoError(t, err)
				require.Equal(t, "host/counter", compiled.Manifest.Budgets[0].Estimator.Descriptor.ID)
				return
			}
			require.ErrorIs(t, err, contexty.ErrInvalidRecordingComponent)
			require.Zero(t, compiled)
			require.Zero(t, calls)
		})
	}
}
