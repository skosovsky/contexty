package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func fixtureCompactionCaptureFixture(t *testing.T, denyOutput bool) (contexty.CompileResult, int) {
	t.Helper()
	return fixtureCompactionCaptureProfiles(t, denyOutput, false)
}

func fixtureCompactionCaptureProfiles(t *testing.T, denyOutput, distinct bool) (contexty.CompileResult, int) {
	t.Helper()
	profile := fixtureCompactionFixture(t).Profile
	profile.Summarizer = fixtureTraceProfile().Stages["summarize"]
	targetProfile := profile
	if distinct {
		profile.Summarizer = contexty.Descriptor{ID: "host/main-summary", Revision: "pinned"}
		targetProfile.Summarizer = contexty.Descriptor{ID: "host/target-summary", Revision: "pinned"}
	}
	reporter, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		fixtureEstimateProfile(),
		contexty.DefaultJSONSerializer(),
	)
	require.NoError(t, err)
	calls := 0
	summarizer := stubSummarizer(func(_ context.Context, inputs []contexty.Message) (contexty.Message, error) {
		calls++
		output := contexty.TextMessage(contexty.RoleSystem, "safe")
		output.ID = "main-summary"
		if len(inputs) == 1 {
			output = contexty.TextMessage(contexty.RoleSystem, "ok")
			output.ID = "target-summary"
		}
		return output, nil
	})
	main := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10), Summarizer: summarizer},
		reporter,
		contexty.WithCompactionCapture(profile),
	)
	target := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(3), Summarizer: summarizer},
		reporter,
		contexty.WithCompactionCapture(targetProfile),
	)
	policy := fixtureContentPolicy(func(_ context.Context, candidate contexty.CaptureCandidate) (bool, error) {
		return !denyOutput || candidate.Purpose != contexty.CaptureOutput ||
			candidate.Content.Ref.ID != "main-summary", nil
	})
	engine := contexty.NewEngine(
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureRecordProfile("small")),
		contexty.WithCompileContentCapture(
			profile.Privacy,
			policy,
		),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, main),
	)
	a := contexty.TextMessage(contexty.RoleUser, "12345678")
	a.ID = "a"
	a.SourceRefs = []contexty.SourceRef{{ID: "one"}}
	b := contexty.TextMessage(contexty.RoleUser, "abcdefgh")
	b.ID = "b"
	b.SourceRefs = []contexty.SourceRef{{ID: "two"}}
	system := contexty.TextMessage(contexty.RoleSystem, "sys")
	system.ID = "sys"
	result, err := engine.CompileSnapshot(
		context.Background(),
		contexty.CompileRequest{CompilationID: "capture", System: []contexty.Message{system},
			History: []contexty.Message{a, b}, Targets: []contexty.CompileTarget{{Name: "small", Budget: target}}},
	)
	require.NoError(t, err)
	return result, calls
}

func fixtureCompactionFixture(t *testing.T) contexty.CompactionRecord {
	t.Helper()
	inputs := []contexty.ContentRef{
		fixtureRef(t, "a", "private-source-secret"),
		fixtureRef(t, "b", "other private source"),
	}
	summary := contexty.TextMessage(contexty.RoleSystem, "safe")
	summary.ID = "summary"
	codec := contexty.DefaultJSONSerializer()
	output, err := contexty.MessageContentRef(summary, codec)
	require.NoError(t, err)
	profile := contexty.CompactionProfile{Model: contexty.Descriptor{ID: "model", Revision: "pinned"},
		Summarizer: contexty.Descriptor{ID: "summarizer", Revision: "pinned"},
		Estimator:  contexty.Descriptor{ID: "estimator", Revision: "pinned"},
		Policy:     contexty.Descriptor{ID: "compaction", Revision: "pinned"},
		Encoding:   contexty.Descriptor{ID: "json", Revision: "pinned"},
		Privacy:    contexty.Descriptor{ID: "privacy", Revision: "pinned"}}
	graph := contexty.Lineage{Records: []contexty.LineageRecord{{ID: "summary-transform", Stage: "summarize",
		Transform: profile.Summarizer, Inputs: inputs, Outputs: []contexty.ContentRef{output}}}}
	wire, err := codec.Marshal(summary)
	require.NoError(t, err)
	content := contexty.SavedContent{Ref: output, Kind: contexty.SavedMessage, Encoding: profile.Encoding, Wire: wire}
	budget := contexty.EffectiveInputBudget(10)
	reporter, err := contexty.NewEstimateReporter(contexty.CharTokenEstimator{}, fixtureEstimateProfile(), codec)
	require.NoError(t, err)
	report, err := reporter.Report(context.Background(), contexty.EstimateRequest{Budget: budget,
		Segments: []contexty.EstimateSegment{{Name: "summary", Messages: []contexty.Message{summary}}}})
	require.NoError(t, err)
	record, err := contexty.NewCompactionRecord("compaction", profile, inputs, output, graph, budget, &content, &report)
	require.NoError(t, err)
	return record
}
