package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestAcceptance_CheckpointAtomicBatch(t *testing.T) {
	// Arrange: history, memory and an artifact form one working checkpoint.
	ctx := context.Background()
	store := contexty.NewMemoryConversationStateStore()
	artifact := contexty.ContextArtifact{
		ID:      "fact",
		Kind:    contexty.ArtifactKindMemoryBlock,
		Payload: contexty.TextPayload("remember"),
	}
	artifact.Lifecycle = contexty.ArtifactLifecyclePersistent
	deltas := []contexty.ConversationDelta{
		{
			Operation: contexty.DeltaAppendMessages,
			Segment:   contexty.SegmentHistory,
			Messages:  []contexty.Message{fixtureRollingText("a", "hello")},
		},
		{
			Operation: contexty.DeltaReplaceSegment,
			Segment:   contexty.SegmentMemory,
			Messages:  []contexty.Message{fixtureRollingText("memo", "memory")},
		},
		{Operation: contexty.DeltaUpsertArtifact, Artifact: &artifact},
	}
	// Act.
	err := store.CommitState(ctx, "batch", 0, deltas...)
	state, loadErr := store.LoadState(ctx, "batch")
	// Assert: all three changes share one revision.
	require.NoError(t, err)
	require.NoError(t, loadErr)
	require.EqualValues(t, 1, state.Version())
	require.Len(t, state.Segment(contexty.SegmentHistory), 1)
	require.Len(t, state.Segment(contexty.SegmentMemory), 1)
	require.Len(t, state.Artifacts(), 1)
	// Act: failure in a later operation rolls back the entire batch.
	err = store.CommitState(ctx, "batch", state.Version(), deltas[0], contexty.ConversationDelta{Operation: "invalid"})
	after, loadErr := store.LoadState(ctx, "batch")
	// Assert.
	require.Error(t, err)
	require.NoError(t, loadErr)
	require.Equal(t, state, after)
	require.ErrorIs(t, store.CommitState(ctx, "batch", state.Version()), contexty.ErrEmptyCheckpointCommit)
}

func TestAcceptance_CheckpointCodecLossless(t *testing.T) {
	// Arrange: transient content belongs to working state, not its durable projection.
	artifact := contexty.ContextArtifact{
		ID:      "transient",
		Kind:    contexty.ArtifactKindMemoryBlock,
		Payload: contexty.TextPayload("scratch"),
	}
	artifact.Lifecycle = contexty.ArtifactLifecycleEphemeral
	working := contexty.EmptyState().WithArtifact(artifact)
	codec := contexty.ConversationCodec{OpaqueProfile: contexty.Descriptor{ID: "", Revision: ""}}
	// Act.
	wire, err := codec.Encode(working)
	require.NoError(t, err)
	restored, err := codec.Decode(wire)
	checkpoint, projectionErr := contexty.ProjectCheckpoint(
		working,
		contexty.DefaultJSONSerializer(),
		contexty.Descriptor{ID: "", Revision: ""},
	)
	// Assert: filtering is explicit and encoding working state is lossless.
	require.NoError(t, err)
	require.NoError(t, projectionErr)
	require.Equal(t, working, restored)
	require.Empty(t, checkpoint.Artifacts())
	require.Len(t, working.Artifacts(), 1)
	_, err = codec.Decode([]byte(`{"schema":"unknown","version":0,"segments":{}}`))
	require.ErrorIs(t, err, contexty.ErrUnsupportedCheckpointSchema)
	_, err = codec.Decode([]byte(`{"version":0,"segments":{}}`))
	require.ErrorIs(t, err, contexty.ErrUnsupportedCheckpointSchema)
}

func TestAcceptance_CheckpointExactArtifactSet(t *testing.T) {
	// Arrange: exact state may contain distinct revisions sharing a merge origin.
	a := contexty.NewMemoryBlock("a", contexty.TextPayload("first")).ContextArtifact
	a.SourceRefs = []contexty.SourceRef{{ID: "same"}}
	a.MergePolicy = contexty.PolicyReplaceByOrigin
	b := a.Clone()
	b.ID, b.Payload = "b", contexty.TextPayload("second")
	working := contexty.EmptyState().WithArtifacts([]contexty.ContextArtifact{a, b})
	codec := contexty.ConversationCodec{OpaqueProfile: contexty.Descriptor{ID: "", Revision: ""}}
	// Act.
	wire, err := codec.Encode(working)
	require.NoError(t, err)
	restored, err := codec.Decode(wire)
	projected, projectErr := contexty.ProjectCheckpoint(
		working,
		contexty.DefaultJSONSerializer(),
		contexty.Descriptor{ID: "", Revision: ""},
	)
	// Assert: boundaries preserve the exact set; upsert/merge belongs to transitions.
	require.NoError(t, err)
	require.NoError(t, projectErr)
	require.Equal(t, working.Artifacts(), restored.Artifacts())
	require.Equal(t, working.Artifacts(), projected.Artifacts())
}
