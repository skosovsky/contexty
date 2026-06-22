package testutil_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/contexty/testutil"
)

func TestMemoryConversationStateStore_AppendAndLoad(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	store := testutil.NewMemoryConversationStateStore()
	s0, err := store.LoadState(ctx, "t")
	require.NoError(t, err)
	require.NoError(t, store.ApplyDelta(ctx, "t", s0.Version(), contexty.ConversationDelta{
		Operation: contexty.DeltaAppendMessages,
		Segment:   contexty.SegmentHistory,
		Messages:  []contexty.Message{contexty.TextMessage(contexty.RoleUser, "hello")},
	}))
	s1, err := store.LoadState(ctx, "t")
	require.NoError(t, err)
	assert.Equal(t, "hello", s1.Segment(contexty.SegmentHistory)[0].TextContent())
	// Act / Assert: exercise the contract and check its result.
	assert.Equal(t, int64(1), s1.Version())
}
