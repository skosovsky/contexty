package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestBudget_PendingRounds(t *testing.T) {
	for _, strategy := range []struct {
		name string
		port contexty.EvictionStrategy
	}{
		{name: "head", port: contexty.NewDropHeadStrategy(contexty.DropHeadConfig{})},
		{name: "tail", port: contexty.NewDropTailStrategy()},
		{name: "block", port: contexty.NewDropStrategy()},
	} {
		t.Run(strategy.name, func(t *testing.T) {
			// Arrange: a pending round protects itself and subsequent chronological context.
			messages := append(fixtureProtectedHistory(), contexty.Message{ID: "recent", Role: contexty.RoleUser})
			baseline := make([]contexty.Message, len(messages))
			for i := range messages {
				baseline[i] = messages[i].Clone()
			}
			pipeline := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(20),
				TruncateStrategy: strategy.port}, &contexty.FixedEstimator{TokensPerMessage: 5})
			// Act.
			outBudget, err := pipeline.Apply(context.Background(), messages)
			out := outBudget.Messages
			// Assert: the suffix is exact, still pending and source remains intact.
			require.NoError(t, err)
			require.LessOrEqual(t, len(out)*5, 20)
			require.Equal(t, baseline[3:], out[len(out)-3:])
			require.Equal(t, baseline, messages)
			rounds, err := contexty.InspectToolRoundStates(out, nil)
			require.NoError(t, err)
			require.Len(t, rounds, 1)
			require.Equal(t, contexty.ToolRoundPending, rounds[0].State)
			require.Equal(t, []string{"second"}, rounds[0].MissingCallIDs)
		})
	}
}

func TestBudget_PendingSummarization(t *testing.T) {
	// Arrange: summarizer never sees the pending block or the recent suffix.
	messages := fixtureProtectedHistory()
	calls := 0
	summarizer := stubSummarizer(func(_ context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
		inputs := request.Messages
		calls++
		require.Len(t, inputs, 3)
		for _, input := range inputs {
			require.False(t, input.HasToolCalls())
		}
		return contexty.Message{ID: "summary", Role: contexty.RoleSystem,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "compressed prefix"}}}, nil
	})
	pipeline := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(15),
		Summarizer: summarizer}, &contexty.FixedEstimator{TokensPerMessage: 5})
	// Act.
	outBudget, err := pipeline.Apply(context.Background(), messages)
	out := outBudget.Messages
	// Assert.
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Len(t, out, 3)
	require.Equal(t, "summary", out[0].ID)
	require.Equal(t, messages[3:], out[1:])
	// Arrange: suffix alone exceeds budget; no compression can fix that safely.
	pipeline = contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(9),
		Summarizer: summarizer}, &contexty.FixedEstimator{TokensPerMessage: 5})
	// Act.
	outBudget, err = pipeline.Apply(context.Background(), messages)
	out = outBudget.Messages
	// Assert.
	require.ErrorIs(t, err, contexty.ErrPendingExceedsBudget)
	require.Nil(t, out)
	require.Equal(t, 1, calls, "oversized pending is rejected before summarizer")
}

func TestBudget_RoundFailuresBeforeCallbacks(t *testing.T) {
	for _, messages := range [][]contexty.Message{
		fixtureRoundMessages()[1:],
		append(fixtureRoundMessages(), fixtureRoundMessages()[1]),
	} {
		// Arrange: malformed source, even if a counter would report zero cost.
		calls := 0
		estimator := fixtureEvidenceEstimator{total: func(context.Context, []contexty.Message) (int, error) {
			calls++
			return 0, nil
		}}
		pipeline := contexty.NewBudgetPipeline(
			contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10)},
			estimator,
		)
		// Act.
		outBudget, err := pipeline.Apply(context.Background(), messages)
		out := outBudget.Messages
		// Assert.
		require.ErrorIs(t, err, contexty.ErrInvalidToolRound)
		require.Nil(t, out)
		require.Zero(t, calls)
	}
}

func TestBudget_PendingCombinedCost(t *testing.T) {
	// Arrange: combined prefix+suffix cost exceeds the sum of isolated costs.
	estimator := fixtureEvidenceEstimator{
		per: func(_ context.Context, messages []contexty.Message) ([]int, error) {
			weights := make([]int, len(messages))
			for i := range weights {
				weights[i] = 5
			}
			return weights, nil
		},
		total: func(_ context.Context, messages []contexty.Message) (int, error) {
			if len(messages) == 3 && messages[0].ID == "keep" && messages[1].ID == "assistant" {
				return 100, nil
			}
			return len(messages) * 5, nil
		},
	}
	pipeline := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(15)}, estimator)
	// Act.
	outBudget, err := pipeline.Apply(context.Background(), fixtureProtectedHistory())
	out := outBudget.Messages
	// Assert: never successful overflow, and no silent pending deletion to fit.
	require.ErrorIs(t, err, contexty.ErrBudgetExceeded)
	require.Nil(t, out)
}

func TestBudget_RoundCompilePersistenceReplay(t *testing.T) {
	// Arrange: main and target use different capacities; pending is protected in both.
	messages := fixtureProtectedHistory()
	main := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(15)},
		&contexty.FixedEstimator{TokensPerMessage: 5})
	target := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10),
		TruncateStrategy: contexty.NewDropStrategy()}, &contexty.FixedEstimator{TokensPerMessage: 5})
	engine := contexty.NewEngine(contexty.WithBudgetPipeline(contexty.SegmentHistory, main),
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureRecordProfile("pending")),
		contexty.WithCompileContentCapture(contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent)))
	request := contexty.CompileRequest{CompilationID: "pending-compile", History: messages,
		Targets: []contexty.CompileTarget{{Name: "pending", Budget: target}}}
	// Act.
	compiled, err := engine.CompileSnapshot(context.Background(), request)
	// Assert: original missing call remains unresolved through persistence and replay.
	require.NoError(t, err)
	require.Equal(t, messages[3:], compiled.Payload.History[1:])
	require.Equal(t, messages[3:], compiled.Projections["pending"].Messages)
	assistantFound, resultFound := false, false
	for _, persisted := range compiled.DerivePersistenceProjection(contexty.SegmentHistory) {
		if persisted.ID == "assistant" {
			assistantFound = true
			require.Equal(t, messages[3], persisted)
		}
		if persisted.ID == "result" {
			resultFound = true
			require.Equal(t, messages[4], persisted)
		}
	}
	require.True(t, assistantFound)
	require.True(t, resultFound)
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(accepted.Manifest)
	require.NoError(t, err)
	replayed, err := contexty.Replay(context.Background(), accepted, expected, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	require.Equal(t, compiled.Payload.History, replayed.Outputs[0].Segments["history"])
	require.Equal(t, compiled.Projections["pending"].Messages, replayed.Outputs[1].Segments["messages"])
	rounds, err := contexty.InspectToolRoundStates(replayed.Outputs[1].Segments["messages"], nil)
	require.NoError(t, err)
	require.Equal(t, contexty.ToolRoundPending, rounds[0].State)
	require.Equal(t, messages, request.History)
}

func TestInterrupted_RepairCompileReplay(t *testing.T) {
	// Arrange: host chooses repair before compilation and supplies its actual graph.
	messages := fixtureRoundMessages()
	codec := contexty.DefaultJSONSerializer()
	projection, err := contexty.RepairInterruptedToolRounds(context.Background(), messages,
		map[string]contexty.ToolRoundState{"assistant": contexty.ToolRoundInterrupted}, fixtureRepairPolicy(), codec)
	require.NoError(t, err)
	engine := contexty.NewEngine(contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithBudgetPipeline(contexty.SegmentHistory,
			contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(15)},
				&contexty.FixedEstimator{TokensPerMessage: 5})),
		contexty.WithCompileRecording(fixtureRecordProfile()),
		contexty.WithCompileContentCapture(contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent)))
	request := contexty.CompileRequest{CompilationID: "repaired-compile", History: projection.Messages,
		Lineage: projection.Lineage}
	// Act.
	compiled, err := engine.CompileSnapshot(context.Background(), request)
	// Assert: replay retains explicit synthetic evidence and the host decision, no re-repair.
	require.NoError(t, err)
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(accepted.Manifest)
	require.NoError(t, err)
	replayed, err := contexty.Replay(context.Background(), accepted, expected, codec)
	require.NoError(t, err)
	require.Equal(t, compiled.Payload.History, replayed.Outputs[0].Segments["history"])
	var syntheticEdge *contexty.LineageRecord
	for i := range replayed.Outputs[0].Lineage.Records {
		if replayed.Outputs[0].Lineage.Records[i].Stage == "round_repair" {
			syntheticEdge = &replayed.Outputs[0].Lineage.Records[i]
		}
	}
	require.NotNil(t, syntheticEdge)
	require.Equal(t, "host-decision", syntheticEdge.DecisionRef)
	require.Equal(t, projection.Repairs[0].Output, syntheticEdge.Outputs[0])
	parts := replayed.Outputs[0].Segments["history"][2].ToolResultParts()
	require.Contains(t, parts[0].Payload.Text, "external outcome is unknown")
	require.Contains(t, string(parts[0].Payload.Data), `"kind":"interrupted_projection"`)
	require.Len(t, messages, 2)
}

func TestBudget_PendingEstimatorMutation(t *testing.T) {
	// Arrange: hostile counter modifies the slices it receives, including call parts.
	messages := fixtureRoundMessages()
	estimator := fixtureEvidenceEstimator{total: func(_ context.Context, input []contexty.Message) (int, error) {
		for i := range input {
			input[i].Parts = nil
		}
		return len(input), nil
	}}
	pipeline := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(2)}, estimator)
	// Act.
	outBudget, err := pipeline.Apply(context.Background(), messages)
	out := outBudget.Messages
	// Assert: pending is protected even on the early within-budget path.
	require.NoError(t, err)
	require.Equal(t, messages, out)
	require.True(t, out[0].HasToolCalls())
	require.Len(t, out[1].ToolResultParts(), 1)
}
