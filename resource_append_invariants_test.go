package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestResourceAppend_FinalMainBudget(t *testing.T) {
	// Arrange: incoming and old each fit, but their merged projection exceeds final main budget.
	block, _ := fixtureResourceBlockWithProjection(
		t,
		func(artifact *contexty.ContextArtifact) { artifact.MergePolicy = contexty.PolicyAppend },
	)
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(7)},
		contexty.CharTokenEstimator{},
	)
	engine := fixtureEngine(
		contexty.WithDeferredBlocks(block),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
	)
	old := contexty.NewMemoryBlock("projected", contexty.TextPayload("old")).ContextArtifact
	// Act.
	compiled, err := engine.CompileSnapshot(
		context.Background(),
		contexty.CompileRequest{Artifacts: []contexty.ContextArtifact{old}},
	)
	// Assert: no successful final eight-character projection above the hard seven-character limit.
	require.ErrorIs(t, err, contexty.ErrBudgetExceeded)
	require.Zero(t, compiled)
	require.Equal(t, "old", old.Payload.Text)
}

func TestResourceAppend_TargetFinalBudget(t *testing.T) {
	// Arrange: a target formatter expands a merged message after target admission.
	block, _ := fixtureResourceBlockWithProjection(
		t,
		func(artifact *contexty.ContextArtifact) { artifact.MergePolicy = contexty.PolicyAppend },
	)
	old := contexty.NewMemoryBlock("projected", contexty.TextPayload("old")).ContextArtifact
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(8)},
		contexty.CharTokenEstimator{},
	)
	request := contexty.CompileRequest{Artifacts: []contexty.ContextArtifact{old}, Targets: []contexty.CompileTarget{
		{
			Name:             "expanded",
			Segments:         []contexty.SegmentName{contexty.SegmentMemory},
			IncludeArtifacts: true,
			Budget:           pipe,
			Formatter: func(_ context.Context, messages []contexty.Message) ([]contexty.Message, error) {
				messages[0].Parts = []contexty.ContentPart{contexty.TextPart{Text: "old\nsafe!"}}
				return messages, nil
			},
		},
	}}
	engine := fixtureEngine(contexty.WithDeferredBlocks(block))
	// Act / Assert: post-format target overflow fails atomically.
	compiled, err := engine.CompileSnapshot(context.Background(), request)
	require.ErrorIs(t, err, contexty.ErrBudgetExceeded)
	require.Zero(t, compiled)
	// Arrange / Act / Assert: within-budget target formatting remains target-local.
	request.Targets[0].Formatter = func(_ context.Context, messages []contexty.Message) ([]contexty.Message, error) {
		messages[0].Parts = []contexty.ContentPart{contexty.TextPart{Text: "target"}}
		return messages, nil
	}
	request.Targets = append(
		request.Targets,
		contexty.CompileTarget{
			Name:             "other",
			Segments:         []contexty.SegmentName{contexty.SegmentMemory},
			IncludeArtifacts: true,
		},
	)
	compiled, err = engine.CompileSnapshot(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, "old\nsafe", compiled.Payload.Memory[0].TextContent())
	require.Equal(t, "old\nsafe", compiled.Projections["other"].Messages[0].TextContent())
	require.Equal(t, "target", compiled.Projections["expanded"].Messages[0].TextContent())
}

func TestResourceAppend_CheckpointLifecycle(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		lifecycle   contexty.ArtifactLifecycle
		persistence contexty.ArtifactPersistencePolicy
		stored      bool
		want        string
	}{
		{"persistent", contexty.ArtifactLifecyclePersistent, contexty.ArtifactPersistenceDefault, true, "old\nsafe"},
		{"skip", contexty.ArtifactLifecyclePersistent, contexty.ArtifactPersistenceSkip, false, "old\nsafe"},
		{"ephemeral", contexty.ArtifactLifecycleEphemeral, contexty.ArtifactPersistenceDefault, false, "safe"},
		{"ephemeral-store", contexty.ArtifactLifecycleEphemeral, contexty.ArtifactPersistenceStore, true, "safe"},
		{"turn-bound", contexty.ArtifactLifecycleTurnBound, contexty.ArtifactPersistenceDefault, false, "old\nsafe"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange: lifecycle/persistence belong to incoming projection, not to its previous revision.
			block, _ := fixtureResourceBlockWithProjection(t, func(artifact *contexty.ContextArtifact) {
				artifact.MergePolicy = contexty.PolicyAppend
				artifact.Lifecycle = scenario.lifecycle
				artifact.Persistence = scenario.persistence
				if scenario.lifecycle == contexty.ArtifactLifecycleTurnBound {
					artifact.BoundTurnID = "turn"
				}
			})
			old := contexty.NewMemoryBlock("projected", contexty.TextPayload("old")).ContextArtifact
			engine := fixtureEngine(contexty.WithDeferredBlocks(block))
			// Act: persist the final artifact set rather than introduced ordinary messages.
			compiled, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
				TurnID: "turn", Artifacts: []contexty.ContextArtifact{old},
			})
			require.NoError(t, err)
			projected, err := contexty.ProjectCheckpoint(
				contexty.EmptyState().WithArtifacts(compiled.Artifacts),
				contexty.DefaultJSONSerializer(),
				contexty.Descriptor{ID: "", Revision: ""},
			)
			require.NoError(t, err)
			wire, err := (contexty.ConversationCodec{OpaqueProfile: contexty.Descriptor{ID: "", Revision: ""}}).Encode(
				projected,
			)
			require.NoError(t, err)
			state, err := (contexty.ConversationCodec{OpaqueProfile: contexty.Descriptor{ID: "", Revision: ""}}).Decode(
				wire,
			)
			// Assert: checkpoint policy and lifecycle survive, without duplicate prompt-message persistence.
			require.NoError(t, err)
			require.Equal(t, scenario.want, compiled.Artifacts[0].Payload.Text)
			require.Equal(t, scenario.lifecycle, compiled.Artifacts[0].Lifecycle)
			require.Equal(t, scenario.stored, len(state.Artifacts()) == 1)
			require.Empty(t, fixturePersistenceSegment(t, compiled, contexty.SegmentMemory))
		})
	}
}

func TestResourceAppend_EvidenceOwnership(t *testing.T) {
	// Arrange: derived metadata, proposal, actual graph and outputs have separate owners.
	block, _ := fixtureResourceBlockWithProjection(
		t,
		func(artifact *contexty.ContextArtifact) { artifact.MergePolicy = contexty.PolicyAppend },
	)
	old := contexty.NewMemoryBlock("projected", contexty.TextPayload("old")).ContextArtifact
	compiled, err := fixtureDedupRecordingEngine(block).CompileSnapshot(context.Background(), contexty.CompileRequest{
		CompilationID: "append-owned",
		Artifacts:     []contexty.ContextArtifact{old},
		Targets: []contexty.CompileTarget{
			{Name: "memory", Segments: []contexty.SegmentName{contexty.SegmentMemory}, IncludeArtifacts: true},
		},
	})
	require.NoError(t, err)
	// Act: mutate one caller-visible graph and typed artifact without changing saved evidence.
	compiled.Manifest.Resources[0].Merge.Inputs[0].ID = "caller mutation"
	compiled.Artifacts[0].Payload.Text = "caller mutation"
	projection := compiled.Projections["memory"]
	projection.Messages[0].Parts = []contexty.ContentPart{contexty.TextPart{Text: "target mutation"}}
	// Assert: main, original source and independently saved proposal retain the original derivation.
	require.Equal(t, "old\nsafe", compiled.Payload.Memory[0].TextContent())
	require.Equal(t, "old", old.Payload.Text)
	require.Equal(t, "projected", compiled.Record.Manifest.Resources[0].Merge.Inputs[0].ID)
	require.NoError(t, compiled.Record.Validate())
}
