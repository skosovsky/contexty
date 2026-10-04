package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestResource_ReplaceByOrigin(t *testing.T) {
	// Arrange: replacement uses a different artifact ID but the same typed origin.
	block, calls := fixtureResourceBlockWithProjection(t, func(artifact *contexty.ContextArtifact) {
		artifact.MergePolicy = contexty.PolicyReplaceByOrigin
	})
	old := contexty.NewRetrievalDocument("old", contexty.TextPayload("old document")).ContextArtifact
	old.Lifecycle = contexty.ArtifactLifecyclePersistent
	old.SourceRefs = []contexty.SourceRef{{ID: "opaque-source"}}
	other := contexty.NewRetrievalDocument("other", contexty.TextPayload("other document")).ContextArtifact
	other.Lifecycle = contexty.ArtifactLifecyclePersistent
	other.SourceRefs = []contexty.SourceRef{{ID: "other-source"}}
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(block),
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(
			fixtureBindings(fixtureRecordProfile("memory"), fixtureBinding(contexty.RecordingResolver, "", "", 0)),
		),
		contexty.WithCompileContentCapture(
			contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent),
		),
	)
	request := contexty.CompileRequest{
		CompilationID: "replace-resource",
		Artifacts:     []contexty.ContextArtifact{old, other},
		Targets: []contexty.CompileTarget{
			{Name: "memory", Segments: []contexty.SegmentName{contexty.SegmentMemory}, IncludeArtifacts: true},
		},
	}
	// Act: native resolution replaces matching origin without changing actual resolution evidence.
	compiled, err := engine.CompileSnapshot(context.Background(), request)
	// Assert: artifacts, prompt and target agree; replaced source remains only as evidence.
	require.NoError(t, err)
	require.Equal(t, 1, *calls)
	require.Len(t, compiled.Artifacts, 2)
	require.Equal(t, "other", compiled.Artifacts[0].ID)
	require.Equal(t, "projected", compiled.Artifacts[1].ID)
	require.Len(t, compiled.Payload.Memory, 2)
	require.Equal(t, "other document", compiled.Payload.Memory[0].TextContent())
	require.Equal(t, "safe", compiled.Payload.Memory[1].TextContent())
	require.Equal(t, compiled.Payload.Memory, compiled.Projections["memory"].Messages)
	require.Equal(t, []contexty.ContextArtifact{old, other}, compiled.Source.Artifacts)
	require.Len(t, compiled.Manifest.ExcludedArtifacts, 1)
	require.Equal(t, "old", compiled.Manifest.ExcludedArtifacts[0].Input.ID)
	require.Equal(t, "artifact_merge", compiled.Manifest.ExcludedArtifacts[0].Reason)
	require.Empty(t, compiled.DerivePersistenceProjection(contexty.SegmentMemory))
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(accepted.Manifest)
	require.NoError(t, err)
	replayed, err := contexty.Replay(
		context.Background(),
		accepted,
		expected,
		contexty.DefaultJSONSerializer(),
		contexty.WithReplayResourceCodecs(
			map[string]contexty.ResourceCodec{"resolve": {Messages: contexty.DefaultJSONSerializer()}},
		),
	)
	require.NoError(t, err)
	require.Equal(t, compiled.Artifacts, replayed.Artifacts)
	require.Equal(t, 1, *calls)
}

func TestResource_InactiveReplacementPreservesExisting(t *testing.T) {
	// Arrange: an inactive turn-bound result must not remove an existing active origin.
	block, _ := fixtureResourceBlockWithProjection(t, func(artifact *contexty.ContextArtifact) {
		artifact.MergePolicy = contexty.PolicyReplaceByOrigin
		artifact.Lifecycle = contexty.ArtifactLifecycleTurnBound
		artifact.BoundTurnID = "other-turn"
	})
	old := contexty.NewRetrievalDocument("old", contexty.TextPayload("still active")).ContextArtifact
	old.Lifecycle = contexty.ArtifactLifecyclePersistent
	old.SourceRefs = []contexty.SourceRef{{ID: "opaque-source"}}
	engine := contexty.NewEngine(contexty.WithDeferredBlocks(block))
	// Act / Assert: admission precedes replacement; no inactive result can evict a source.
	compiled, err := engine.CompileSnapshot(
		context.Background(),
		contexty.CompileRequest{TurnID: "current", Artifacts: []contexty.ContextArtifact{old}},
	)
	require.NoError(t, err)
	require.Equal(t, []contexty.ContextArtifact{old}, compiled.Artifacts)
	require.Len(t, compiled.Payload.Memory, 1)
	require.Equal(t, "still active", compiled.Payload.Memory[0].TextContent())
}

func TestResource_SameIDReplacementEvidence(t *testing.T) {
	// Arrange: same display/occurrence identity does not mean the old content revision is retained.
	block, calls := fixtureResourceBlock(t)
	old := contexty.NewMemoryBlock("projected", contexty.TextPayload("old revision")).ContextArtifact
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(block),
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(
			fixtureBindings(fixtureRecordProfile(), fixtureBinding(contexty.RecordingResolver, "", "", 0)),
		),
		contexty.WithCompileContentCapture(
			contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent),
		),
	)
	// Act.
	compiled, err := engine.CompileSnapshot(
		context.Background(),
		contexty.CompileRequest{CompilationID: "same-id", Artifacts: []contexty.ContextArtifact{old}},
	)
	// Assert: old revision is excluded despite matching artifact ID; final prompt/checkpoint/replay agree.
	require.NoError(t, err)
	require.Len(t, compiled.Payload.Memory, 1)
	require.Equal(t, "safe", compiled.Payload.Memory[0].TextContent())
	require.Len(t, compiled.Manifest.ExcludedArtifacts, 1)
	oldRef, err := contexty.ArtifactContentRef(old)
	require.NoError(t, err)
	require.Equal(t, oldRef, compiled.Manifest.ExcludedArtifacts[0].Input)
	require.Equal(t, "artifact_merge", compiled.Manifest.ExcludedArtifacts[0].Reason)
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(accepted.Manifest)
	require.NoError(t, err)
	replayed, err := contexty.Replay(
		context.Background(),
		accepted,
		expected,
		contexty.DefaultJSONSerializer(),
		contexty.WithReplayResourceCodecs(
			map[string]contexty.ResourceCodec{"resolve": {Messages: contexty.DefaultJSONSerializer()}},
		),
	)
	require.NoError(t, err)
	require.Equal(t, compiled.Artifacts, replayed.Artifacts)
	require.Equal(t, 1, *calls)
}

func TestResource_SequentialOriginReplacement(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		sameBlock bool
		sharedID  bool
	}{
		{"separate-blocks", false, false},
		{"same-block", true, false},
		{"same-id-separate-blocks", false, true},
		{"same-id-same-block", true, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange: both actual resolutions remain evidence; the last matching origin wins admission.
			first := fixtureNamedReplacementBlock(t, "first")
			second := fixtureNamedReplacementBlock(t, "second")
			expectedID := "second"
			if scenario.sharedID {
				first = fixtureNamedReplacementBlock(t, "first", "shared")
				second = fixtureNamedReplacementBlock(t, "second", "shared")
				expectedID = "shared"
			}
			blocks := []contexty.DeferredBlock{first, second}
			if scenario.sameBlock {
				combined := first
				combined.Resources = append(combined.Resources, second.Resources...)
				combined.Resolve = func(ctx context.Context) (contexty.DeferredResult, error) {
					left, err := first.Resolve(ctx)
					if err != nil {
						return contexty.DeferredResult{}, err
					}
					right, err := second.Resolve(ctx)
					return contexty.DeferredResult{Resources: append(left.Resources, right.Resources...)}, err
				}
				blocks = []contexty.DeferredBlock{combined}
			}
			engine := contexty.NewEngine(contexty.WithDeferredBlocks(blocks...))
			// Act / Assert: stale projections cannot remain in the collected callback messages or snapshot.
			compiled, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{})
			require.NoError(t, err)
			require.Len(t, compiled.Artifacts, 1)
			require.Equal(t, expectedID, compiled.Artifacts[0].ID)
			require.Len(t, compiled.Payload.Memory, 1)
			require.Equal(t, "artifact:"+expectedID, compiled.Payload.Memory[0].ID)
			require.Len(t, compiled.Source.DeferredResources, 2)
			require.Empty(t, compiled.DerivePersistenceProjection(contexty.SegmentMemory))
		})
	}
}

func TestResource_GenericMessageCollision(t *testing.T) {
	// Arrange: generic callback content may not impersonate resource materialization.
	block, _ := fixtureResourceBlock(t)
	resolve := block.Resolve
	block.Resolve = func(ctx context.Context) (contexty.DeferredResult, error) {
		result, err := resolve(ctx)
		if err != nil {
			return contexty.DeferredResult{}, err
		}
		result.Messages = []contexty.Message{result.Resources[0].Message.Clone()}
		return result, nil
	}
	engine := contexty.NewEngine(contexty.WithDeferredBlocks(block))
	// Act.
	compiled, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{})
	// Assert: fail atomically, never silently discard host content by ID.
	require.ErrorIs(t, err, contexty.ErrInvalidResource)
	require.Equal(t, contexty.CompileResult{}, compiled)
}

func TestResource_ReplacementSurvivesLaterDeferredBlock(t *testing.T) {
	// Arrange: cleanup must consume only the revisions replaced by this block.
	block, _ := fixtureResourceBlock(t)
	later := contexty.DeferredBlock{
		Name: "later", Segment: contexty.SegmentMemory,
		Resolve: func(context.Context) (contexty.DeferredResult, error) {
			return contexty.DeferredResult{Messages: []contexty.Message{
				{
					ID:    "unrelated",
					Role:  contexty.RoleSystem,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "later"}},
				},
			}}, nil
		},
	}
	old := contexty.NewMemoryBlock("projected", contexty.TextPayload("old")).ContextArtifact
	engine := contexty.NewEngine(contexty.WithDeferredBlocks(block, later))
	// Act.
	compiled, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		Artifacts: []contexty.ContextArtifact{old},
	})
	// Assert: a later unrelated callback cannot remove the already admitted replacement.
	require.NoError(t, err)
	require.Len(t, compiled.Artifacts, 1)
	require.Len(t, compiled.Payload.Memory, 2)
	require.Equal(t, "safe", compiled.Payload.Memory[0].TextContent())
	require.Equal(t, "later", compiled.Payload.Memory[1].TextContent())
}
