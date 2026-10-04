package contexty_test

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/contexty/testutil"
)

func TestStore_Conformance(t *testing.T) {
	// Arrange / Act / Assert: run the shared store lifecycle contract on a fresh instance.
	testutil.CheckStateStore(t, contexty.NewMemoryConversationStateStore(), "fixture")
}

func TestRevision_Exhaustion(t *testing.T) {
	// Arrange: the last usable token or an invalid negative token.
	for _, token := range []int64{math.MaxInt64, -1} {
		// Act.
		_, err := contexty.NextConversationVersion(token)
		// Assert: identity must never wrap/reuse.
		require.ErrorIs(t, err, contexty.ErrConversationVersionExhausted)
	}
}

func TestClear_MissingIdentity(t *testing.T) {
	// Arrange: even an absent ID can have a stale writer holding token zero.
	ctx := context.Background()
	store := contexty.NewMemoryConversationStateStore()
	// Act.
	require.NoError(t, store.ClearState(ctx, "absent", 0))
	state, err := store.LoadState(ctx, "absent")
	// Assert: clear consumes that token and returns a payload-free tombstone.
	require.NoError(t, err)
	require.Equal(t, int64(1), state.Version())
	require.Empty(t, state.AllSegments())
	require.ErrorIs(t, store.CommitState(ctx, "absent", 0, contexty.ConversationDelta{}),
		contexty.ErrConversationVersionConflict)
}

func TestJSONSerializer_RoundTrip(t *testing.T) {
	// Arrange.
	serializer := contexty.DefaultJSONSerializer()
	msg := contexty.Message{
		Role: contexty.RoleAssistant,
		Parts: []contexty.ContentPart{
			contexty.TextPart{Text: "summary"},
			contexty.ImagePart{URL: "https://example.com/image.png", Detail: "low"},
			contexty.ToolCallPart{ID: "tc1", Name: "fn", Arguments: contexty.JSONPayload(`{"a":1}`)},
		},
		SourceRefs: []contexty.SourceRef{{
			Namespace: "messages",
			Kind:      "external",
			ID:        "ref-1",
		}},
		Provenance: contexty.SystemProvenance{Component: "test"},
	}
	// Act.
	data, err := serializer.Marshal(msg)
	// Assert.
	require.NoError(t, err)
	var out contexty.Message
	require.NoError(t, serializer.Unmarshal(data, &out))
	assert.True(t, contexty.MessageEqual(msg, out))
}

func TestConversationCodec_RoundTrip(t *testing.T) {
	// Arrange.
	codec := contexty.ConversationCodec{Provenance: contexty.DefaultProvenanceRegistry()}
	snap := contexty.EmptySnapshot().WithVersion(3).WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "hi"),
	})
	// Act.
	data, err := codec.Encode(snap)
	// Assert.
	require.NoError(t, err)
	out, err := codec.Decode(data)
	require.NoError(t, err)
	assert.Equal(t, int64(3), out.Version())
	assert.Equal(t, "hi", out.Segment(contexty.SegmentHistory)[0].TextContent())
}
