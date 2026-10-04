package contexty_test

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestResource_SameIDDeduplication(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		source    string
		ephemeral bool
		want      string
	}{
		{"shared-source", "opaque-source", false, "old revision"},
		{"different-source", "different", false, "safe"},
		{"empty-source", "", false, "safe"},
		{"ephemeral-replaces", "opaque-source", true, "safe"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange: incoming dedup policy is part of the actual resource projection.
			block, reads := fixtureResourceBlockWithProjection(t, func(artifact *contexty.ContextArtifact) {
				artifact.MergePolicy = contexty.PolicyDeduplicateByLayer
				if scenario.ephemeral {
					artifact.Lifecycle = contexty.ArtifactLifecycleEphemeral
				}
			})
			old := contexty.NewMemoryBlock("projected", contexty.TextPayload("old revision")).ContextArtifact
			old.SourceRefs = []contexty.SourceRef{{ID: scenario.source}}
			engine := fixtureEngine(contexty.WithDeferredBlocks(block))
			// Act.
			compiled, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
				Artifacts: []contexty.ContextArtifact{old},
				Targets: []contexty.CompileTarget{
					{Name: "memory", Segments: []contexty.SegmentName{contexty.SegmentMemory}, IncludeArtifacts: true},
				},
			})
			// Assert: one selected revision, consistent main/target and immutable source.
			require.NoError(t, err)
			require.Len(t, compiled.Artifacts, 1)
			require.Equal(t, scenario.want, compiled.Artifacts[0].Payload.Text)
			require.Len(t, compiled.Payload.Memory, 1)
			require.Equal(t, scenario.want, compiled.Payload.Memory[0].TextContent())
			require.Equal(t, compiled.Payload.Memory, compiled.Projections["memory"].Messages)
			require.Equal(t, []contexty.ContextArtifact{old}, compiled.Source.Artifacts)
			require.Empty(t, compiled.DerivePersistenceProjection(contexty.SegmentMemory))
			require.Equal(t, 1, *reads)
		})
	}
}

func TestResource_DedupAcceptedReplay(t *testing.T) {
	// Arrange: excluded resource still has complete frozen resolution dependencies.
	block, reads := fixtureResourceBlockWithProjection(t, func(artifact *contexty.ContextArtifact) {
		artifact.MergePolicy = contexty.PolicyDeduplicateByLayer
		artifact.Budget = &contexty.ArtifactBudgetPolicy{TokenLimit: 4}
	})
	old := contexty.NewMemoryBlock("projected", contexty.TextPayload("old revision")).ContextArtifact
	old.SourceRefs = []contexty.SourceRef{{ID: "opaque-source"}}
	compiled, err := fixtureDedupRecordingEngine(block).CompileSnapshot(context.Background(), contexty.CompileRequest{
		CompilationID: "dedup-replay",
		Artifacts:     []contexty.ContextArtifact{old},
		Targets: []contexty.CompileTarget{
			{Name: "memory", Segments: []contexty.SegmentName{contexty.SegmentMemory}, IncludeArtifacts: true},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []contexty.ContextArtifact{old}, compiled.Artifacts)
	require.Len(t, compiled.Manifest.ExcludedArtifacts, 1)
	require.Equal(t, compiled.Manifest.Resources[0].Artifact, compiled.Manifest.ExcludedArtifacts[0].Input)
	require.Equal(t, "artifact_merge", compiled.Manifest.ExcludedArtifacts[0].Reason)
	require.Empty(t, compiled.ArtifactEstimates)
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(accepted.Manifest)
	require.NoError(t, err)
	codecs := contexty.WithReplayResourceCodecs(map[string]contexty.ResourceCodec{
		"resolve": {Messages: contexty.DefaultJSONSerializer()},
	})
	// Act: accepted replay retains the chosen old revision without reading again.
	replayed, err := contexty.Replay(context.Background(), accepted, expected, contexty.DefaultJSONSerializer(), codecs)
	// Assert: missing excluded-resource body is still an error, never a refetch/fallback.
	require.NoError(t, err)
	require.Equal(t, compiled.Artifacts, replayed.Artifacts)
	require.Equal(t, "old revision", replayed.Outputs[0].Segments["memory"][0].TextContent())
	body := accepted.Manifest.Resources[0].Selection.Resource.Content
	accepted.Content = slices.DeleteFunc(
		accepted.Content,
		func(content contexty.SavedContent) bool { return content.Ref == body },
	)
	failed, err := contexty.Replay(context.Background(), accepted, expected, contexty.DefaultJSONSerializer(), codecs)
	require.ErrorIs(t, err, contexty.ErrMissingReplayDependency)
	require.Zero(t, failed)
	require.Equal(t, 1, *reads)
}

func TestResource_DedupIdenticalRevisionBudget(t *testing.T) {
	// Arrange: the input is exactly the incoming typed revision, including local budget.
	block, reads := fixtureResourceBlockWithProjection(t, func(artifact *contexty.ContextArtifact) {
		artifact.MergePolicy = contexty.PolicyDeduplicateByLayer
		artifact.Budget = &contexty.ArtifactBudgetPolicy{TokenLimit: 4}
	})
	resolved, err := block.Resolve(context.Background())
	require.NoError(t, err)
	old := resolved.Resources[0].Artifact.Clone()
	// Act.
	compiled, err := fixtureDedupRecordingEngine(block).CompileSnapshot(context.Background(), contexty.CompileRequest{
		CompilationID: "same-revision",
		Artifacts:     []contexty.ContextArtifact{old},
		Targets: []contexty.CompileTarget{
			{Name: "memory", Segments: []contexty.SegmentName{contexty.SegmentMemory}, IncludeArtifacts: true},
		},
	})
	// Assert: repeated evidence cannot create duplicate budget requests, estimates or prompt messages.
	require.NoError(t, err)
	require.Len(t, compiled.Payload.Memory, 1)
	require.Equal(t, []contexty.ContextArtifact{old}, compiled.Artifacts)
	require.Len(t, compiled.Manifest.ArtifactBudgets, 1)
	require.Len(t, compiled.ArtifactEstimates, 1)
	require.Empty(t, compiled.Manifest.ExcludedArtifacts)
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	require.NoError(t, accepted.Validate())
	require.Equal(t, 2, *reads)
}

func TestResource_DedupExcludedAdmission(t *testing.T) {
	for _, inactive := range []bool{false, true} {
		t.Run(map[bool]string{false: "over-budget", true: "inactive"}[inactive], func(t *testing.T) {
			// Arrange: disjoint sources would select incoming, but admission excludes it first.
			block, _ := fixtureResourceBlockWithProjection(t, func(artifact *contexty.ContextArtifact) {
				artifact.MergePolicy = contexty.PolicyDeduplicateByLayer
				artifact.Budget = &contexty.ArtifactBudgetPolicy{TokenLimit: 0}
				if inactive {
					artifact.Lifecycle = contexty.ArtifactLifecycleTurnBound
					artifact.BoundTurnID = "other"
				}
			})
			old := contexty.NewMemoryBlock("projected", contexty.TextPayload("active")).ContextArtifact
			old.SourceRefs = []contexty.SourceRef{{ID: "different"}}
			// Act.
			compiled, err := fixtureDedupRecordingEngine(
				block,
			).CompileSnapshot(context.Background(), contexty.CompileRequest{
				CompilationID: "excluded",
				TurnID:        "current",
				Artifacts:     []contexty.ContextArtifact{old},
				Targets: []contexty.CompileTarget{
					{Name: "memory", Segments: []contexty.SegmentName{contexty.SegmentMemory}, IncludeArtifacts: true},
				},
			})
			// Assert: preparation resolves merge before output-local admission; no hidden fallback.
			require.NoError(t, err)
			if inactive {
				require.Equal(t, []contexty.ContextArtifact{old}, compiled.Artifacts)
				require.Len(t, compiled.Payload.Memory, 1)
				require.Equal(t, "active", compiled.Payload.Memory[0].TextContent())
			} else {
				require.Empty(t, compiled.Artifacts)
				require.Empty(t, compiled.Payload.Memory)
				require.Contains(
					t,
					fixtureExclusionReasons(compiled.Manifest.ExcludedArtifacts),
					contexty.ReasonTokenBudgetExceeded,
				)
			}
			require.Equal(t, compiled.Payload.Memory, compiled.Projections["memory"].Messages)
		})
	}
}

func fixtureExclusionReasons(exclusions []contexty.ArtifactExclusion) []string {
	var reasons []string
	for _, exclusion := range exclusions {
		reasons = append(reasons, exclusion.Reason)
	}
	return reasons
}
