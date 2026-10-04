package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestLostAcknowledgementAndReconciliation(t *testing.T) {
	// Arrange: the host owns a unique message identity for this mutation.
	ctx := context.Background()
	base := contexty.NewMemoryConversationStateStore()
	store := &lostAcknowledgementStore{ConversationStateStore: base, loseNext: true}
	before, err := store.LoadState(ctx, "lost-ack")
	require.NoError(t, err)
	message := contexty.TextMessage(contexty.RoleUser, "owned content")
	message.ID = "host-owned-write/message"
	delta := contexty.ConversationDelta{
		Operation: contexty.DeltaAppendMessages,
		Segment:   contexty.SegmentHistory,
		Messages:  []contexty.Message{message},
	}
	// Act: the server commits, but the caller gets an unavailable response.
	err = store.CommitState(ctx, "lost-ack", before.Version(), delta)
	require.ErrorIs(t, err, contexty.ErrUnavailable)
	// Assert: old-token retry cannot duplicate; conflict alone is insufficient.
	require.ErrorIs(
		t,
		store.CommitState(ctx, "lost-ack", before.Version(), delta),
		contexty.ErrConversationVersionConflict,
	)
	require.NoError(t, reconcileOwnedCheckpoint(ctx, base, "lost-ack", before, delta))
	after, err := base.LoadState(ctx, "lost-ack")
	require.NoError(t, err)
	require.Len(t, after.Segment(contexty.SegmentHistory), 1)
	require.EqualValues(t, 1, after.Version())
}

func TestCompetingCheckpointRemainsUnknown(t *testing.T) {
	// Arrange: an unavailable operation could have been replaced by another writer.
	ctx := context.Background()
	store := contexty.NewMemoryConversationStateStore()
	before, err := store.LoadState(ctx, "competing")
	require.NoError(t, err)
	owned := contexty.TextMessage(contexty.RoleUser, "mine")
	owned.ID = "host-owned/message"
	foreign := owned.Clone()
	foreign.ID = "other-host/message"
	delta := contexty.ConversationDelta{
		Operation: contexty.DeltaAppendMessages,
		Segment:   contexty.SegmentHistory,
		Messages:  []contexty.Message{owned},
	}
	foreignDelta := delta
	foreignDelta.Messages = []contexty.Message{foreign}
	// Act: a foreign commit consumes the token; the same retry conflict is observed.
	require.NoError(t, store.CommitState(ctx, "competing", before.Version(), foreignDelta))
	require.ErrorIs(
		t,
		store.CommitState(ctx, "competing", before.Version(), delta),
		contexty.ErrConversationVersionConflict,
	)
	// Assert: reconciliation never claims this commit as the host's own.
	require.ErrorIs(t, reconcileOwnedCheckpoint(ctx, store, "competing", before, delta), ErrUnknownOutcome)
}

func TestNoWitnessRemainsUnknown(t *testing.T) {
	// Arrange: a no-op has no durable unique identity proving which writer committed.
	ctx := context.Background()
	store := contexty.NewMemoryConversationStateStore()
	before, err := store.LoadState(ctx, "no-witness")
	require.NoError(t, err)
	// Act: content and revision alone could otherwise match a foreign no-op.
	require.NoError(t, store.CommitState(ctx, "no-witness", before.Version(), contexty.ConversationDelta{}))
	// Assert: absence of a new persisted witness is explicitly unknown.
	require.ErrorIs(
		t,
		reconcileOwnedCheckpoint(ctx, store, "no-witness", before, contexty.ConversationDelta{}),
		ErrUnknownOutcome,
	)
}
