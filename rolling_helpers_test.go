package contexty_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func fixtureRollingText(id, text string) contexty.Message {
	message := contexty.TextMessage(contexty.RoleUser, text)
	message.ID = id
	message.SourceRefs = []contexty.SourceRef{{ID: id + "-source"}}
	return message
}

func fixtureRollingEngine(t *testing.T, calls *int) *contexty.Engine {
	t.Helper()
	policy := fixtureRollingPolicy(2)
	profile := fixtureCompactionFixture(t).Profile
	profile.Policy = policy.Descriptor
	profile.Summarizer = fixtureTraceProfile().Stages["summarize"]
	reporter, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		fixtureEstimateProfile(),
		contexty.DefaultJSONSerializer(),
	)
	require.NoError(t, err)
	pipeline := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10),
		Summarizer: stubSummarizer(func(_ context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
			inputs := request.Messages
			*calls++
			for _, input := range inputs {
				require.NotEqual(t, "turn-next", input.ID, "current turn cannot enter compaction")
			}
			message := contexty.TextMessage(contexty.RoleSystem, "sum")
			message.ID = "summary-" + strconv.Itoa(*calls)
			return message, nil
		})}, reporter, contexty.WithRollingSummary(policy), contexty.WithCompactionCapture(profile))
	return fixtureEngine(contexty.WithBudgetPipeline(pipeline),
		contexty.WithTraceProfile(fixtureTraceProfile()), contexty.WithCompileRecording(fixtureRecordProfile()),
		contexty.WithCompileContentCapture(profile.Privacy, fixtureContentPolicy(fixtureAllowContent)))
}

func fixtureRefIDs(refs []contexty.ContentRef) []string {
	ids := make([]string, len(refs))
	for i, ref := range refs {
		ids[i] = ref.ID
	}
	return ids
}

func fixtureCheckRollingReplay(t *testing.T, compiled contexty.CompileResult, calls *int) {
	t.Helper()
	accepted, err := compiled.Record.Accept("host-accept-compile")
	require.NoError(t, err)
	wire, err := contexty.EncodeSavedRecord(accepted)
	require.NoError(t, err)
	restored, err := contexty.DecodeSavedRecord(wire)
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(restored.Manifest)
	require.NoError(t, err)
	require.Equal(t, fixtureRollingPolicy(2), *expected.Budgets[0].RollingSummary)
	before := *calls
	// Act: exact restart replay without executing either earlier summarization.
	replayed, err := contexty.Replay(context.Background(), restored, expected, contexty.DefaultJSONSerializer())
	// Assert: original IDs/wire/lineage, no callback calls or stale-recipe acceptance.
	require.NoError(t, err)
	require.Equal(t, before, *calls)
	require.Equal(t, compiled.Payload.History, replayed.Outputs[0].Segments["history"])
	require.Equal(t, compiled.Lineage, replayed.Outputs[0].Lineage)
	expected.Budgets[0].RollingSummary.RecentMessages = 1
	require.Equal(t, 2, restored.Manifest.Budgets[0].RollingSummary.RecentMessages)
	failed, err := contexty.Replay(context.Background(), restored, expected, contexty.DefaultJSONSerializer())
	require.ErrorIs(t, err, contexty.ErrReplayMismatch)
	require.Zero(t, failed)
	require.Equal(t, before, *calls)
}

func fixtureRollingMessageIDs(messages []contexty.Message) []string {
	ids := make([]string, len(messages))
	for i, message := range messages {
		ids[i] = message.ID
	}
	return ids
}

func fixtureRollingPolicy(recent int) contexty.RollingSummaryPolicy {
	return contexty.RollingSummaryPolicy{
		Descriptor:     contexty.Descriptor{ID: "rolling", Revision: "pinned"},
		RecentMessages: recent,
	}
}
