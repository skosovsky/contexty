package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestSummarizer_Identity(t *testing.T) {
	// Arrange: explicit local identity, different from the generic trace stage.
	descriptor := contexty.Descriptor{ID: "host/summary", Revision: "pinned"}
	calls := 0
	summarizer := stubSummarizer(func(context.Context, []contexty.Message) (contexty.Message, error) {
		calls++
		return fixtureRollingText("summary", "ok"), nil
	})
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(2),
		Summarizer: summarizer}, contexty.CharTokenEstimator{}, contexty.WithSummarizerDescriptor(descriptor))
	descriptor.Revision = "caller-mutated"
	// Act.
	compiled, err := fixtureTruncationEngine(pipe).CompileSnapshot(context.Background(),
		contexty.CompileRequest{CompilationID: "local-summary", History: []contexty.Message{
			fixtureRollingText("source", "long-input"),
		}})
	// Assert: actual lineage and configuration agree; replay never calls the summarizer.
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, "pinned", compiled.Manifest.Budgets[0].Summarizer.Revision)
	found := false
	for _, edge := range compiled.Lineage.Records {
		if edge.Stage == "summarize" && len(edge.Outputs) > 0 {
			found = true
			require.Equal(t, *compiled.Manifest.Budgets[0].Summarizer, edge.Transform)
		}
	}
	require.True(t, found)
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(*compiled.Manifest)
	require.NoError(t, err)
	expected.Budgets[0].Summarizer.Revision = "changed"
	replayed, err := contexty.Replay(context.Background(), accepted, expected, contexty.DefaultJSONSerializer())
	require.ErrorIs(t, err, contexty.ErrReplayMismatch)
	require.Zero(t, replayed)
	require.Equal(t, 1, calls)
	malformed, err := compiled.Manifest.Clone()
	require.NoError(t, err)
	malformed.Budgets[0].Summarizer = nil
	require.ErrorIs(t, malformed.Validate(), contexty.ErrInvalidRecordingComponent)
	require.NoError(t, compiled.Manifest.Validate())
}

func TestSummarizer_BindingFailures(t *testing.T) {
	for _, scenario := range []string{"missing", "surplus", "invalid"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: invalid configuration fails even when no summarization is needed.
			calls := 0
			cfg := contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)}
			if scenario != "surplus" {
				cfg.Summarizer = stubSummarizer(func(context.Context, []contexty.Message) (contexty.Message, error) {
					calls++
					return contexty.Message{}, nil
				})
			}
			var options []contexty.BudgetPipelineOption
			if scenario != "missing" {
				descriptor := contexty.Descriptor{ID: "summary", Revision: "pinned"}
				if scenario == "invalid" {
					descriptor.Revision = ""
				}
				options = append(options, contexty.WithSummarizerDescriptor(descriptor))
			}
			// Act.
			compiled, err := fixtureTruncationEngine(contexty.NewBudgetPipeline(cfg,
				contexty.CharTokenEstimator{}, options...)).CompileSnapshot(context.Background(),
				contexty.CompileRequest{CompilationID: scenario})
			// Assert.
			require.Error(t, err)
			require.Zero(t, compiled)
			require.Zero(t, calls)
		})
	}
}
