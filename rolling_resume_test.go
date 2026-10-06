package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestRolling_SummaryResume(t *testing.T) {
	// Arrange: raw current turn is private/large; only its prompt-safe form is budgeted.
	calls := 0
	engine := fixtureRollingEngine(t, &calls)
	history := []contexty.Message{fixtureRollingText("a", "aaaaaaaa"), fixtureRollingText("b", "bbbbbbbb"),
		fixtureRollingText("c", "cccccccc"), fixtureRollingText("d", "d"), fixtureRollingText("e", "e")}
	turn := contexty.NewCurrentTurn(fixtureRollingText("turn", "private raw current turn"))
	turn = turn.WithPromptSafe(fixtureRollingText("turn", "Q?"))
	request := contexty.CompileRequest{CompilationID: "rolling-first", History: history,
		System: []contexty.Message{fixtureRollingText("sys", "s")}, CurrentTurn: &turn}
	// Act.
	first, err := engine.CompileSnapshot(context.Background(), request)
	// Assert: only a/b/c are covered, and current turn keeps distinct projections.
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, history, first.Source.History)
	require.Equal(t, history[3:], first.Payload.History[1:3])
	require.Equal(t, "Q?", first.Payload.History[3].TextContent())
	require.Len(t, first.Compactions, 1)
	compaction := first.Compactions[0]
	require.Equal(t, []string{"a", "b", "c"}, fixtureRefIDs(compaction.Covered))
	require.Equal(t, contexty.EffectiveInputBudget(5), compaction.Budget)
	require.Len(t, first.Payload.History[0].SourceRefs, 3)
	persisted := fixturePersistenceSegment(t, first, contexty.SegmentHistory)
	require.Equal(t, first.Payload.History[:3], persisted[:3], "summary must precede its recent tail on resume")
	require.Equal(t, turn.Raw, persisted[len(persisted)-1])
	require.Equal(t, turn.Raw, request.CurrentTurn.Raw)
	accepted, err := compaction.Accept("host-accept-first")
	require.NoError(t, err)
	// Arrange: simulate restart by decoding an accepted compaction and saved bytes.
	wire, err := contexty.EncodeCompactionRecord(accepted)
	require.NoError(t, err)
	restored, err := contexty.DecodeCompactionRecord(wire)
	require.NoError(t, err)
	summary, err := contexty.ReplayCompaction(
		context.Background(),
		restored,
		contexty.ContentRef{
			ID:     restored.ID,
			Digest: restored.Digest,
		},
		restored.Profile,
		restored.Covered,
		restored.Budget,
		contexty.DefaultJSONSerializer(),
	)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	resumed := []contexty.Message{summary}
	resumed = append(resumed, persisted[1:]...)
	resumed = append(
		resumed,
		fixtureRollingText("f", "ffffffff"),
		fixtureRollingText("g", "g"),
		fixtureRollingText("h", "h"),
	)
	nextTurn := contexty.NewCurrentTurn(fixtureRollingText("turn-next", "next private raw turn"))
	nextTurn = nextTurn.WithPromptSafe(fixtureRollingText("turn-next", "q"))
	// Act: roll the accepted summary together with newly aged messages, not recent g/h.
	second, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{CompilationID: "rolling-second",
		History: resumed, Lineage: first.Lineage, System: request.System, CurrentTurn: &nextTurn})
	// Assert: new record coverage is exact and old ancestry remains in the graph.
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Len(t, second.Compactions, 1)
	require.Equal(t, []string{"summary-1", "d", "e", "turn", "f"}, fixtureRefIDs(second.Compactions[0].Covered))
	require.Equal(t, resumed[len(resumed)-2:], second.Payload.History[1:3])
	require.Equal(t, "q", second.Payload.History[3].TextContent())
	require.Equal(t, contexty.EffectiveInputBudget(6), second.Compactions[0].Budget)
	require.Len(t, second.Payload.History[0].SourceRefs, 7, "old summary retains all source ancestors")
	var oldSummaryEdge bool
	for _, record := range second.Compactions[0].Lineage.Records {
		for _, output := range record.Outputs {
			if output == compaction.Output {
				oldSummaryEdge = true
			}
		}
	}
	require.True(t, oldSummaryEdge)
	persisted = fixturePersistenceSegment(t, second, contexty.SegmentHistory)
	require.Equal(t, nextTurn.Raw, persisted[len(persisted)-1])
	fixtureCheckRollingReplay(t, second, &calls)
}

func TestRolling_SummaryTargets(t *testing.T) {
	// Arrange: different recent-tail recipes and budgets in main and named target.
	reporter, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		fixtureEstimateProfile(),
		contexty.DefaultJSONSerializer(),
	)
	require.NoError(t, err)
	mainPolicy, targetPolicy := fixtureRollingPolicy(2), fixtureRollingPolicy(1)
	targetPolicy.Descriptor.ID = "target-rolling"
	mainProfile := fixtureCompactionFixture(t).Profile
	mainProfile.Policy, mainProfile.Summarizer = mainPolicy.Descriptor, fixtureTraceProfile().Stages["summarize"]
	targetProfile := mainProfile
	targetProfile.Policy = targetPolicy.Descriptor
	calls := 0
	summarizer := stubSummarizer(func(_ context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
		calls++
		message := contexty.TextMessage(contexty.RoleSystem, "sum")
		message.ID = "main-summary"
		if request.Purpose.ID == "target-rolling" {
			message = contexty.TextMessage(contexty.RoleSystem, "t")
			message.ID = "target-summary"
		}
		return message, nil
	})
	main := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10), Summarizer: summarizer},
		reporter,
		contexty.WithRollingSummary(mainPolicy),
		contexty.WithCompactionCapture(mainProfile),
	)
	target := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(4), Summarizer: summarizer},
		reporter,
		contexty.WithRollingSummary(targetPolicy),
		contexty.WithCompactionCapture(targetProfile),
	)
	engine := fixtureEngine(
		contexty.WithBudgetPipeline(main),
		contexty.WithTraceProfile(
			fixtureTraceProfile(),
		),
		contexty.WithCompileRecording(fixtureRecordProfile("short", "unchanged")),
		contexty.WithCompileContentCapture(mainProfile.Privacy, fixtureContentPolicy(fixtureAllowContent)),
	)
	history := []contexty.Message{fixtureRollingText("a", "aaaaaaaa"), fixtureRollingText("b", "bbbbbbbb"),
		fixtureRollingText("c", "cccccccc"), fixtureRollingText("d", "d"), fixtureRollingText("e", "e")}
	turn := contexty.NewCurrentTurn(fixtureRollingText("turn", "private raw"))
	turn = turn.WithPromptSafe(fixtureRollingText("turn", "q?"))
	// Act.
	compiled, err := engine.CompileSnapshot(
		context.Background(),
		contexty.CompileRequest{
			CompilationID: "rolling-targets",
			History:       history,
			CurrentTurn:   &turn,
			Targets: []contexty.CompileTarget{
				{
					Segments:           []contexty.SegmentName{contexty.SegmentHistory},
					Name:               "short",
					Budget:             target,
					IncludeCurrentTurn: true,
				},
				{
					Segments:           []contexty.SegmentName{contexty.SegmentHistory},
					Name:               "unchanged",
					IncludeCurrentTurn: true,
				},
			},
		},
	)
	// Assert: separate records/capacities/coverage; no target mutation of main/source.
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Equal(t, []string{"main-summary", "d", "e", "turn"}, fixtureRollingMessageIDs(compiled.Payload.History))
	require.Equal(
		t,
		[]string{"target-summary", "e", "turn"},
		fixtureRollingMessageIDs(compiled.Projections["short"].Messages),
	)
	require.Equal(
		t,
		[]string{"a", "b", "c", "d", "e", "turn"},
		fixtureRollingMessageIDs(compiled.Projections["unchanged"].Messages),
	)
	require.Equal(t, history, compiled.Source.History)
	require.Len(t, compiled.Compactions, 2)
	require.Equal(t, []string{"a", "b", "c"}, fixtureRefIDs(compiled.Compactions[0].Covered))
	require.Equal(t, []string{"a", "b", "c", "d"}, fixtureRefIDs(compiled.Compactions[1].Covered))
	require.Equal(t, contexty.EffectiveInputBudget(1), compiled.Compactions[1].Budget)
	for _, budget := range compiled.Manifest.Budgets {
		if budget.Kind == contexty.ManifestMainOutput {
			require.Equal(t, mainPolicy, *budget.RollingSummary)
		} else {
			require.Equal(t, targetPolicy, *budget.RollingSummary)
		}
	}
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(accepted.Manifest)
	require.NoError(t, err)
	replayed, err := contexty.Replay(context.Background(), accepted, expected, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	require.Equal(t, compiled.Projections["short"].Messages, replayed.Outputs[1].Segments["messages"])
	require.Equal(t, 2, calls)
}
