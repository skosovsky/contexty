package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestResourceArtifact_Lifecycle(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		lifecycle   contexty.ArtifactLifecycle
		boundTurn   string
		persistence contexty.ArtifactPersistencePolicy
		visible     bool
		stored      bool
	}{
		{"persistent", contexty.ArtifactLifecyclePersistent, "", contexty.ArtifactPersistenceDefault, true, true},
		{"persistent-skip", contexty.ArtifactLifecyclePersistent, "", contexty.ArtifactPersistenceSkip, true, false},
		{"ephemeral", contexty.ArtifactLifecycleEphemeral, "", contexty.ArtifactPersistenceDefault, true, false},
		{"ephemeral-store", contexty.ArtifactLifecycleEphemeral, "", contexty.ArtifactPersistenceStore, true, true},
		{"turn-current", contexty.ArtifactLifecycleTurnBound, "turn", contexty.ArtifactPersistenceDefault, true, false},
		{"turn-other", contexty.ArtifactLifecycleTurnBound, "other", contexty.ArtifactPersistenceDefault, false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange: host declares artifact lifecycle, independently of the reader.
			block, calls := fixtureResourceBlockWithProjection(t, func(artifact *contexty.ContextArtifact) {
				artifact.Lifecycle, artifact.BoundTurnID, artifact.Persistence = scenario.lifecycle, scenario.boundTurn, scenario.persistence
			})
			engine := fixtureEngine(contexty.WithDeferredBlocks(block))
			// Act: compile then encode the exact admitted artifact set as a checkpoint.
			result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{TurnID: "turn"})
			require.NoError(t, err)
			projected, err := contexty.ProjectCheckpoint(contexty.EmptyState().WithArtifacts(result.Artifacts))
			require.NoError(t, err)
			wire, err := (contexty.ConversationCodec{}).Encode(projected)
			require.NoError(t, err)
			checkpoint, err := (contexty.ConversationCodec{}).Decode(wire)
			// Assert: resource projection obeys normal admission and artifact persistence.
			require.NoError(t, err)
			require.Equal(t, 1, *calls)
			require.Equal(t, scenario.visible, len(result.Payload.Memory) == 1)
			require.Equal(t, scenario.visible, len(result.Artifacts) == 1)
			require.Equal(t, scenario.stored, len(checkpoint.Artifacts()) == 1)
			require.Empty(t, result.DerivePersistenceProjection(contexty.SegmentMemory))
			require.Empty(t, result.Source.Artifacts)
		})
	}
}

func TestResourceArtifact_AdmissionBudget(t *testing.T) {
	for _, limit := range []int{0, 3, 4} {
		t.Run(map[int]string{0: "zero", 3: "excluded", 4: "exact"}[limit], func(t *testing.T) {
			// Arrange: selected body projects to four characters under a local artifact bound.
			block, calls := fixtureResourceBlockWithProjection(t, func(artifact *contexty.ContextArtifact) {
				artifact.Budget = &contexty.ArtifactBudgetPolicy{TokenLimit: limit}
			})
			engine := fixtureEngine(
				contexty.WithDeferredBlocks(block),
				contexty.WithTraceProfile(fixtureTraceProfile()),
				contexty.WithCompileRecording(
					fixtureBindings(fixtureRecordProfile(), fixtureBinding(contexty.RecordingResolver, "", "", 0)),
				),
			)
			// Act: normal artifact estimator supplies actual admission evidence once.
			result, err := engine.CompileSnapshot(
				context.Background(),
				contexty.CompileRequest{CompilationID: "admission"},
			)
			// Assert: exclusion never becomes prompt content; manifest retains exact reason/cost.
			require.NoError(t, err)
			require.Equal(t, 1, *calls)
			require.Equal(t, limit >= 4, len(result.Payload.Memory) == 1)
			require.Len(t, result.ArtifactEstimates, 1)
			require.Equal(t, 4, result.ArtifactEstimates[0].Tokens)
			require.Equal(t, limit, result.ArtifactEstimates[0].TokenLimit)
			if limit < 4 {
				require.Len(t, result.Manifest.ExcludedArtifacts, 1)
				require.Equal(t, contexty.ReasonTokenBudgetExceeded, result.Manifest.ExcludedArtifacts[0].Reason)
			}
			require.NoError(t, result.Manifest.Validate())
		})
	}
}

func TestResourceArtifact_IdentityAndOperationIsolation(t *testing.T) {
	// Arrange: selection is reusable; explicit same-ID replacement owns a new revision.
	block, calls := fixtureResourceBlock(t)
	engine := fixtureEngine(contexty.WithDeferredBlocks(block))
	input := contexty.NewMemoryBlock("projected", contexty.TextPayload("existing")).ContextArtifact
	// Act: replace an input revision, then run two independent compiles.
	replaced, err := engine.CompileSnapshot(
		context.Background(),
		contexty.CompileRequest{Artifacts: []contexty.ContextArtifact{input}},
	)
	require.NoError(t, err)
	require.Len(t, replaced.Artifacts, 1)
	require.Equal(t, "safe", replaced.Artifacts[0].Payload.Text)
	require.Equal(t, []contexty.ContextArtifact{input}, replaced.Source.Artifacts)
	first, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{})
	require.NoError(t, err)
	second, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{})
	// Assert: operation-local IDs/evidence cannot contaminate another compile invocation.
	require.NoError(t, err)
	require.Equal(t, 3, *calls)
	require.Equal(t, first.Artifacts, second.Artifacts)
	require.Equal(t, "safe", second.Payload.Memory[0].TextContent())
	first.Artifacts[0].Payload.Text = "caller-mutated"
	require.Equal(t, "safe", second.Artifacts[0].Payload.Text)
	require.Equal(t, "existing", input.Payload.Text)
}
