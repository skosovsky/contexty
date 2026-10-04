package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestCompaction_DistinctSummarizers(t *testing.T) {
	// Arrange/Act: main and target pin different summarizers under one trace profile.
	compiled, calls := fixtureCompactionCaptureProfiles(t, false, true)
	// Assert: descriptors represent actual channel-local execution and survive replay.
	require.Equal(t, 2, calls)
	require.Len(t, compiled.Compactions, 2)
	for i, id := range []string{"host/main-summary", "host/target-summary"} {
		record := compiled.Compactions[i]
		require.Equal(t, id, record.Profile.Summarizer.ID)
		require.Equal(t, id, compiled.Manifest.Budgets[i].Summarizer.ID)
		for _, edge := range record.Lineage.Records {
			if edge.ID == compiled.Manifest.Compactions[i].Invocation {
				require.Equal(t, record.Profile.Summarizer, edge.Transform)
			}
		}
	}
	require.NoError(t, compiled.Manifest.Validate())
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(*compiled.Manifest)
	require.NoError(t, err)
	replayed, err := contexty.Replay(context.Background(), accepted, expected, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	require.Equal(t, compiled.Manifest.Budgets, replayed.Manifest.Budgets)
}

func TestCompaction_Capture(t *testing.T) {
	// Arrange and Act: actual main and target summarize stages generate proposals.
	compiled, calls := fixtureCompactionCaptureFixture(t, false)
	// Assert: protected system consumes capacity, target records are distinct.
	require.Equal(t, 2, calls)
	require.Len(t, compiled.Compactions, 2)
	main, target := compiled.Compactions[0], compiled.Compactions[1]
	require.Equal(t, contexty.EffectiveInputBudget(7), main.Budget)
	require.Equal(t, contexty.EffectiveInputBudget(3), target.Budget)
	require.Equal(t, 7, main.Execution.Summary.MaxTokens)
	require.Equal(t, 7, main.Execution.Summary.TargetTokens)
	require.Equal(t, 3, target.Execution.Summary.MaxTokens)
	require.Equal(t, main.Profile.Policy, main.Execution.Summary.Purpose)
	for _, proposal := range compiled.Compactions {
		wire, encodeErr := contexty.EncodeCompactionRecord(proposal)
		require.NoError(t, encodeErr)
		restored, decodeErr := contexty.DecodeCompactionRecord(wire)
		require.NoError(t, decodeErr)
		require.Equal(t, proposal.Execution, restored.Execution)
	}
	require.Len(t, main.Covered, 2)
	require.Len(t, target.Covered, 2)
	require.Equal(t, "a", target.Covered[0].ID)
	require.Equal(t, "b", target.Covered[1].ID)
	require.NotEqual(t, main.ID, target.ID)
	require.Len(t, compiled.Manifest.Compactions, 2)
	require.Equal(t, compiled.Manifest.Compactions, compiled.Record.Manifest.Compactions)
	for i, record := range compiled.Compactions {
		link := compiled.Manifest.Compactions[i]
		require.Equal(t, contexty.ContentRef{ID: record.ID, Digest: record.Digest}, link.Record)
	}
	require.Equal(t, contexty.ManifestMainOutput, compiled.Manifest.Compactions[0].Kind)
	require.Equal(t, "main", compiled.Manifest.Compactions[0].Target)
	require.Equal(t, contexty.ManifestTargetOutput, compiled.Manifest.Compactions[1].Kind)
	require.Equal(t, "small", compiled.Manifest.Compactions[1].Target)
	require.Equal(t, contexty.RecordProposed, main.State)
	accepted, err := main.Accept("host-accept")
	require.NoError(t, err)
	require.NotEqual(t, accepted.Digest, compiled.Manifest.Compactions[0].Record.Digest)
	message, err := contexty.ReplayCompaction(
		context.Background(),
		accepted,
		contexty.ContentRef{
			ID:     accepted.ID,
			Digest: accepted.Digest,
		},
		accepted.Profile,
		accepted.Covered,
		accepted.Budget,
		contexty.DefaultJSONSerializer(),
	)
	require.NoError(t, err)
	require.Equal(t, "safe", message.TextContent())
	require.Len(t, message.SourceRefs, 2)
	require.Equal(t, compiled.Payload.History[0], message)
	main.Result.Wire[0] = 'x'
	require.NoError(t, target.Validate())
	require.NoError(t, accepted.Validate())
}

func TestCompaction_CapturePrivacy(t *testing.T) {
	// Arrange and Act: a later output decision denies a previously kept transform.
	compiled, _ := fixtureCompactionCaptureFixture(t, true)
	// Assert: capturing compaction cannot bypass final global privacy denial.
	require.Len(t, compiled.Compactions, 2)
	require.Nil(t, compiled.Compactions[0].Result)
	accepted, err := compiled.Compactions[0].Accept("host-accept")
	require.ErrorIs(t, err, contexty.ErrMissingReplayDependency)
	require.Zero(t, accepted)
	require.NotNil(t, compiled.Compactions[1].Result)
	require.NoError(t, compiled.Compactions[0].Validate())
	require.Equal(t, compiled.Compactions[0].Digest, compiled.Manifest.Compactions[0].Record.Digest)
	require.Equal(t, compiled.Manifest.Compactions, compiled.Record.Manifest.Compactions)
}

func TestManifest_CompactionReplay(t *testing.T) {
	// Arrange: acceptance of the compile record does not accept compaction proposals.
	compiled, calls := fixtureCompactionCaptureFixture(t, false)
	accepted, err := compiled.Record.Accept("host-compile-accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(*compiled.Manifest)
	require.NoError(t, err)
	// Act: no proposal store or summarizer is supplied to exact replay.
	replayed, err := contexty.Replay(context.Background(), accepted, expected, contexty.DefaultJSONSerializer())
	// Assert: links survive serialization and remain immutable proposal references.
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Equal(t, compiled.Manifest.Compactions, replayed.Manifest.Compactions)
	replayed.Manifest.Compactions[0].Record.ID = "changed"
	require.NotEqual(t, replayed.Manifest.Compactions, compiled.Manifest.Compactions)
	require.NoError(t, accepted.Validate())
}

func TestManifest_CompactionInvalidLinks(t *testing.T) {
	for _, scenario := range []string{
		"unknown-channel", "unknown-invocation", "duplicate", "duplicate-invocation", "occurrence",
		"missing-all", "missing-one", "inherited-main-as-target", "undeclared-capture", "capture-privacy",
		"capture-estimator", "capture-encoding", "capture-summarizer",
	} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: a valid manifest independently owns its links.
			compiled, _ := fixtureCompactionCaptureFixture(t, false)
			manifest, err := compiled.Manifest.Clone()
			require.NoError(t, err)
			// Act: corrupt the reference without touching saved source bytes.
			switch scenario {
			case "unknown-channel":
				manifest.Compactions[0].Target = "absent"
			case "unknown-invocation":
				manifest.Compactions[0].Invocation = "absent"
			case "duplicate":
				manifest.Compactions = append(manifest.Compactions, manifest.Compactions[0])
			case "duplicate-invocation":
				link := manifest.Compactions[0]
				link.Record.ID = "another-record"
				manifest.Compactions = append(manifest.Compactions, link)
			case "occurrence":
				manifest.Compactions[0].Record.Occurrence = "not-a-record-ref"
			case "missing-all":
				manifest.Compactions = nil
			case "missing-one":
				manifest.Compactions = manifest.Compactions[:1]
			case "inherited-main-as-target":
				manifest.Compactions[0].Kind = contexty.ManifestTargetOutput
				manifest.Compactions[0].Target = "small"
			case "undeclared-capture":
				manifest.Budgets[0].Compaction = nil
			case "capture-privacy":
				manifest.Budgets[0].Compaction.Privacy.ID = "changed"
			case "capture-estimator":
				manifest.Budgets[0].Compaction.Estimator.ID = "changed"
			case "capture-encoding":
				manifest.Budgets[0].Compaction.Encoding.ID = "changed"
			case "capture-summarizer":
				manifest.Budgets[0].Compaction.Summarizer.ID = "changed"
			}
			// Assert: structural validation rejects before the generic digest check.
			require.ErrorIs(t, manifest.Validate(), contexty.ErrInvalidCompaction)
			require.NoError(t, compiled.Manifest.Validate())
		})
	}
}

func TestCompaction_CaptureIdleProfile(t *testing.T) {
	// Arrange: capture remains part of compile intent even when nothing needs summarizing.
	profile := fixtureCompactionFixture(t).Profile
	profile.Summarizer = fixtureTraceProfile().Stages["summarize"]
	reporter, err := contexty.NewEstimateReporter(contexty.CharTokenEstimator{},
		fixtureEstimateProfile(), contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	calls := 0
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10),
		Summarizer: stubSummarizer(func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
			calls++
			return contexty.Message{}, nil
		})}, reporter, contexty.WithCompactionCapture(profile))
	engine := contexty.NewEngine(
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureRecordProfile()),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
		contexty.WithCompileContentCapture(profile.Privacy, fixtureContentPolicy(
			func(context.Context, contexty.CaptureCandidate) (bool, error) { return true, nil })),
	)
	// Act: compile an empty under-budget request and separately change replay intent.
	compiled, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{CompilationID: "idle"})
	require.NoError(t, err)
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(*compiled.Manifest)
	require.NoError(t, err)
	expected.Budgets[0].Compaction.Policy.Revision = "different"
	replayed, err := contexty.Replay(context.Background(), accepted, expected, contexty.DefaultJSONSerializer())
	// Assert: no proposal is required, but changed capture configuration is not silently reused.
	require.Zero(t, calls)
	require.Empty(t, compiled.Manifest.Compactions)
	require.Equal(t, profile, *compiled.Manifest.Budgets[0].Compaction)
	require.ErrorIs(t, err, contexty.ErrReplayMismatch)
	require.Zero(t, replayed)
	require.NoError(t, accepted.Validate())
}

func TestCompaction_CaptureRequiresConfiguration(t *testing.T) {
	// Arrange: missing trace/privacy configuration fails before summarizer execution.
	profile := fixtureCompactionFixture(t).Profile
	calls := 0
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10),
		Summarizer: stubSummarizer(func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
			calls++
			return contexty.Message{}, nil
		})},
		contexty.CharTokenEstimator{}, contexty.WithCompactionCapture(profile))
	// Act.
	compiled, err := contexty.NewEngine(contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe)).
		CompileSnapshot(context.Background(), contexty.CompileRequest{})
	// Assert.
	require.ErrorIs(t, err, contexty.ErrMissingRecordPolicy)
	require.Zero(t, compiled)
	require.Zero(t, calls)
}

func TestCompaction_CaptureProfileMismatch(t *testing.T) {
	for _, field := range []string{"summarizer", "privacy"} {
		t.Run(field, func(t *testing.T) {
			// Arrange: descriptors must match actual executing configuration.
			profile := fixtureCompactionFixture(t).Profile
			profile.Summarizer = fixtureTraceProfile().Stages["summarize"]
			if field == "summarizer" {
				profile.Summarizer.ID = "wrong"
			} else {
				profile.Privacy.ID = "wrong"
			}
			reporter, err := contexty.NewEstimateReporter(
				contexty.CharTokenEstimator{},
				fixtureEstimateProfile(),
				contexty.DefaultJSONSerializer(),
			)
			require.NoError(t, err)
			calls := 0
			pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10),
				Summarizer: stubSummarizer(func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
					calls++
					return contexty.Message{}, nil
				})},
				reporter, contexty.WithCompactionCapture(profile),
				contexty.WithSummarizerDescriptor(fixtureTraceProfile().Stages["summarize"]))
			engine := contexty.NewEngine(
				contexty.WithTraceProfile(fixtureTraceProfile()),
				contexty.WithCompileRecording(fixtureRecordProfile()),
				contexty.WithCompileContentCapture(
					contexty.Descriptor{ID: "privacy", Revision: "pinned"},
					fixtureContentPolicy(
						func(context.Context, contexty.CaptureCandidate) (bool, error) { calls++; return true, nil },
					),
				),
				contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
			)
			// Act.
			compiled, err := engine.CompileSnapshot(
				context.Background(),
				contexty.CompileRequest{CompilationID: "mismatch"},
			)
			// Assert: neither summarizer nor privacy callback executes.
			require.ErrorIs(t, err, contexty.ErrInvalidCompaction)
			require.Zero(t, compiled)
			require.Zero(t, calls)
		})
	}
}
