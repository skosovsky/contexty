package redis

import (
	"context"

	"github.com/skosovsky/contexty"
)

func updateSegment(
	ctx context.Context,
	store contexty.ConversationStateStore,
	conversationID string,
	expectedVersion int64,
	name contexty.SegmentName,
	msgs []contexty.Message,
) error {
	return store.ApplyDelta(ctx, conversationID, expectedVersion, contexty.ConversationDelta{
		Operation: contexty.DeltaReplaceSegment,
		Segment:   name,
		Messages:  msgs,
	})
}

func appendHistory(
	ctx context.Context,
	store contexty.ConversationStateStore,
	conversationID string,
	expectedVersion int64,
	msgs ...contexty.Message,
) error {
	return store.ApplyDelta(ctx, conversationID, expectedVersion, contexty.ConversationDelta{
		Operation: contexty.DeltaAppendMessages,
		Segment:   contexty.SegmentHistory,
		Messages:  msgs,
	})
}
