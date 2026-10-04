package contexty_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestManifest_Coverage(t *testing.T) {
	// Arrange: budget evicts an old input; a history-only target does not select system.
	old := contexty.TextMessage(contexty.RoleUser, "old-long")
	old.ID = "old"
	recent := contexty.TextMessage(contexty.RoleUser, "new")
	recent.ID = "recent"
	system := contexty.TextMessage(contexty.RoleSystem, "sys")
	system.ID = "system"
	engine := fixtureEngine(contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureBindings(fixtureRecordProfile("history", "empty"),
			fixtureBinding(contexty.RecordingTargetFormatter, "empty", "", 0))),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, contexty.NewBudgetPipeline(
			contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(6)}, contexty.CharTokenEstimator{})))
	request := contexty.CompileRequest{
		CompilationID: "coverage",
		System:        []contexty.Message{system},
		History: []contexty.Message{
			old,
			recent,
		},
		Targets: []contexty.CompileTarget{
			{Segments: []contexty.SegmentName{contexty.SegmentHistory}, Name: "history"},
			{
				Segments: []contexty.SegmentName{contexty.SegmentHistory},
				Name:     "empty",
				Formatter: func(context.Context, []contexty.Message) ([]contexty.Message, error) {
					return nil, nil
				},
			},
		},
	}
	// Act.
	result, err := engine.CompileSnapshot(context.Background(), request)
	// Assert: every input and empty segment is accounted for independently per output.
	require.NoError(t, err)
	m := *result.Manifest
	require.Equal(t, contexty.CoverageIncluded, fixtureCoverage(t, m, "main", "history", "recent").Status)
	evicted := fixtureCoverage(t, m, "main", "history", "old")
	require.Equal(t, contexty.CoverageExcluded, evicted.Status)
	require.Equal(t, contexty.ReasonTokenBudgetExceeded, evicted.Reason)
	unselected := fixtureCoverage(t, m, "history", "system", "system")
	require.Equal(t, contexty.CoverageExcluded, unselected.Status)
	require.Equal(t, "not_selected", unselected.Reason)
	require.Equal(t, contexty.ReasonReplacedByFormatter,
		fixtureCoverage(t, m, "empty", "history", "recent").Reason)
	var emptyMemory bool
	for _, entry := range m.Coverage {
		if entry.OutputName == "main" && entry.Segment == "memory" {
			emptyMemory = entry.Input == nil && entry.Reason == "empty_segment"
		}
	}
	require.True(t, emptyMemory)
	wire, err := contexty.EncodeManifest(m)
	require.NoError(t, err)
	restored, err := contexty.DecodeManifest(wire)
	require.NoError(t, err)
	require.Equal(t, m.Coverage, restored.Coverage)
	// Arrange / Act / Assert: omission, duplicate and invented statuses are rejected.
	for _, mutation := range []func(*contexty.CompileManifest){
		func(v *contexty.CompileManifest) { v.Coverage = v.Coverage[1:] },
		func(v *contexty.CompileManifest) { v.Coverage = append(v.Coverage, v.Coverage[0]) },
		func(v *contexty.CompileManifest) { v.Coverage[0].Status = contexty.CoverageSummarized },
	} {
		broken, cloneErr := m.Clone()
		require.NoError(t, cloneErr)
		mutation(&broken)
		_, encodeErr := contexty.EncodeManifest(broken)
		require.ErrorIs(t, encodeErr, contexty.ErrInvalidCoverage)
		invalidWire, marshalErr := json.Marshal(broken)
		require.NoError(t, marshalErr)
		decoded, decodeErr := contexty.DecodeManifest(invalidWire)
		require.ErrorIs(t, decodeErr, contexty.ErrInvalidCoverage)
		require.Zero(t, decoded)
	}
}

func TestCoverage_Materialization(t *testing.T) {
	// Arrange: caller-declared safe projection and artifact body have distinct source refs.
	raw := contexty.TextMessage(contexty.RoleUser, "secret")
	raw.ID = "raw"
	prompt := contexty.TextMessage(contexty.RoleUser, "safe")
	prompt.ID = raw.ID
	turn := contexty.NewCurrentTurn(raw).WithPromptSafe(prompt)
	active := contexty.NewMemoryBlock("active", contexty.TextPayload("body")).ContextArtifact
	inactive := contexty.NewMemoryBlock("inactive", contexty.TextPayload("old")).WithTurn("old-turn")
	oversized := contexty.NewMemoryBlock("oversized", contexty.TextPayload("too long")).WithBudget(
		contexty.ArtifactBudgetPolicy{TokenLimit: 1})
	profile := fixtureTraceProfile()
	profile.RequireOrigins = true
	artifactRef, err := contexty.ArtifactContentRef(active)
	require.NoError(t, err)
	request := contexty.CompileRequest{
		CompilationID: "materialize",
		TurnID:        "now",
		CurrentTurn:   &turn,
		Artifacts:     []contexty.ContextArtifact{active, inactive, oversized},
		Origins: []contexty.ContentRef{
			fixtureRefForMessage(t, raw),
			artifactRef,
			fixtureArtifactContentRef(t, oversized),
		},
	}
	engine := fixtureEngine(
		contexty.WithTraceProfile(profile),
		contexty.WithCompileRecording(fixtureRecordProfile()),
	)
	// Act.
	result, err := engine.CompileSnapshot(context.Background(), request)
	// Assert: strict origins require raw/artifact origins, not fabricated prompt origins.
	require.NoError(t, err)
	m := *result.Manifest
	require.Equal(t, contexty.CoverageTransformed, fixtureCoverage(t, m, "main", "current/raw", "raw").Status)
	require.Equal(t, contexty.CoverageIncluded, fixtureCoverage(t, m, "main", "current/prompt", "raw").Status)
	require.Equal(t, "persistence_only", fixtureCoverage(t, m, "main", "current/persistence", "raw").Reason)
	artifactCoverage := fixtureCoverage(t, m, "main", "artifacts", "active")
	require.Equal(t, contexty.CoverageTransformed, artifactCoverage.Status)
	require.Equal(t, "artifact:active", artifactCoverage.Outputs[0].ID)
	require.Equal(t, "artifact_inactive", fixtureCoverage(t, m, "main", "artifacts", "inactive").Reason)
	require.Equal(t, contexty.ReasonTokenBudgetExceeded,
		fixtureCoverage(t, m, "main", "artifacts", "oversized").Reason)
	require.Len(t, m.ExcludedArtifacts, 2)
	broken, err := m.Clone()
	require.NoError(t, err)
	broken.ExcludedArtifacts[0].Input = artifactRef
	require.ErrorIs(t, broken.Validate(), contexty.ErrInvalidCoverage)
	// Arrange / Act / Assert: each materialization stage must have a pinned descriptor.
	for _, stage := range []string{"prompt-template", "artifact"} {
		bad := fixtureTraceProfile()
		delete(bad.Stages, stage)
		options := []contexty.EngineOption{contexty.WithTraceProfile(bad)}
		want := contexty.ErrInvalidDescriptor
		if stage == "artifact" {
			materialization := fixtureMaterialization()
			materialization.Identity = contexty.Descriptor{}
			options = append(options, contexty.WithArtifactMaterialization(*materialization))
			want = contexty.ErrInvalidArtifactMaterialization
		}
		_, compileErr := fixtureEngine(options...).CompileSnapshot(context.Background(), request)
		require.ErrorIs(t, compileErr, want)
	}
}

func TestCoverage_Summary(t *testing.T) {
	// Arrange: two source messages become one summary shared by the target.
	a := contexty.TextMessage(contexty.RoleUser, "first-long")
	a.ID = "a"
	b := contexty.TextMessage(contexty.RoleUser, "second-long")
	b.ID = "b"
	engine := fixtureEngine(contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureBindings(fixtureRecordProfile("history", "empty"),
			fixtureBinding(contexty.RecordingTargetFormatter, "empty", "", 0))),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, contexty.NewBudgetPipeline(
			contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(5), Summarizer: stubSummarizer(
				func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
					summary := contexty.TextMessage(contexty.RoleAssistant, "sum")
					summary.ID = "summary"
					return summary, nil
				})}, contexty.CharTokenEstimator{},
			contexty.WithSummarizerDescriptor(fixtureTraceProfile().Stages["summarize"]))))
	request := contexty.CompileRequest{CompilationID: "summary-coverage", History: []contexty.Message{a, b},
		Targets: []contexty.CompileTarget{
			{Segments: []contexty.SegmentName{contexty.SegmentHistory}, Name: "history"},
			{
				Segments: []contexty.SegmentName{contexty.SegmentHistory},
				Name:     "empty",
				Formatter: func(context.Context, []contexty.Message) ([]contexty.Message, error) {
					return nil, nil
				},
			},
		}}
	// Act.
	result, err := engine.CompileSnapshot(context.Background(), request)
	// Assert: each source maps to summary; a later run retaining it is not new summarization.
	require.NoError(t, err)
	for _, output := range []string{"main"} {
		for _, id := range []string{"a", "b"} {
			entry := fixtureCoverage(t, *result.Manifest, output, "history", id)
			require.Equal(t, contexty.CoverageSummarized, entry.Status)
			require.Equal(t, "summary", entry.Outputs[0].ID)
		}
	}
	for _, id := range []string{"a", "b"} {
		require.Equal(
			t,
			contexty.CoverageIncluded,
			fixtureCoverage(t, *result.Manifest, "history", "history", id).Status,
		)
	}
	for _, id := range []string{"a", "b"} {
		entry := fixtureCoverage(t, *result.Manifest, "empty", "history", id)
		require.Equal(t, contexty.CoverageExcluded, entry.Status)
		require.Equal(t, contexty.ReasonReplacedByFormatter, entry.Reason)
	}
	next := contexty.CompileRequest{CompilationID: "retained-summary", Lineage: result.Lineage,
		History: result.Payload.History, Targets: request.Targets}
	nextResult, err := engine.CompileSnapshot(context.Background(), next)
	require.NoError(t, err)
	require.Equal(t, contexty.CoverageIncluded,
		fixtureCoverage(t, *nextResult.Manifest, "main", "history", "summary").Status)
}
