package contexty_test

import (
	"context"

	"github.com/skosovsky/contexty"
)

func loadState(
	ctx context.Context,
	store contexty.ConversationStateStore,
	conversationID string,
) (contexty.ConversationState, error) {
	return store.LoadState(ctx, conversationID)
}

func updateSegment(
	ctx context.Context,
	store contexty.ConversationStateStore,
	conversationID string,
	expectedVersion int64,
	name contexty.SegmentName,
	msgs []contexty.Message,
) error {
	return store.CommitState(ctx, conversationID, expectedVersion, contexty.ConversationDelta{
		Operation: contexty.DeltaReplaceSegment,
		Segment:   name,
		Messages:  msgs,
	})
}

func appendSegment(
	ctx context.Context,
	store contexty.ConversationStateStore,
	conversationID string,
	expectedVersion int64,
	name contexty.SegmentName,
	msgs ...contexty.Message,
) error {
	return store.CommitState(ctx, conversationID, expectedVersion, contexty.ConversationDelta{
		Operation: contexty.DeltaAppendMessages,
		Segment:   name,
		Messages:  msgs,
	})
}
