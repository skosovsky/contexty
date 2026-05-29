package testutil_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/contexty/testutil"
)

func TestMemoryConversationStore_AppendAndLoad(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewMemoryConversationStore()
	s0, err := store.Load(ctx, "t")
	require.NoError(t, err)
	require.NoError(t, store.AppendSegment(ctx, "t", s0.Version(), contexty.SegmentHistory,
		contexty.TextMessage(contexty.RoleUser, "hello"),
	))
	s1, err := store.Load(ctx, "t")
	require.NoError(t, err)
	assert.Equal(t, "hello", s1.Segment(contexty.SegmentHistory)[0].TextContent())
	assert.Equal(t, int64(1), s1.Version())
}
