package redis

import (
	"context"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/skosovsky/contexty"
)

func TestStoreIntegration(t *testing.T) {
	requireDocker(t)

	ctx := context.Background()
	container, err := tcredis.Run(ctx, "redis:7-alpine")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, testcontainers.TerminateContainer(container))
	})

	endpoint, err := container.Endpoint(ctx, "")
	require.NoError(t, err)

	client := goredis.NewClient(&goredis.Options{Addr: endpoint})
	t.Cleanup(func() {
		require.NoError(t, client.Close())
	})

	t.Run("empty load", func(t *testing.T) {
		store := New(client)
		snap, err := store.Load(ctx, "empty")
		require.NoError(t, err)
		assert.Empty(t, snap.Segment(contexty.SegmentHistory))
		assert.Equal(t, int64(0), snap.Version())
	})

	t.Run("append segment", func(t *testing.T) {
		store := New(client)
		conversationID := "thread-ordered"
		s0, err := store.Load(ctx, conversationID)
		require.NoError(t, err)
		require.NoError(t, store.AppendSegment(ctx, conversationID, s0.Version(), contexty.SegmentHistory,
			contexty.TextMessage(contexty.RoleUser, "one"),
			contexty.TextMessage(contexty.RoleAssistant, "two"),
		))
		s1, err := store.Load(ctx, conversationID)
		require.NoError(t, err)
		require.NoError(t, store.AppendSegment(ctx, conversationID, s1.Version(), contexty.SegmentHistory,
			contexty.TextMessage(contexty.RoleUser, "three")))

		snap, err := store.Load(ctx, conversationID)
		require.NoError(t, err)
		assert.Len(t, snap.Segment(contexty.SegmentHistory), 3)
		assert.Equal(t, int64(2), snap.Version())
	})

	t.Run("update segment", func(t *testing.T) {
		store := New(client)
		conversationID := "thread-save"
		s0, err := store.Load(ctx, conversationID)
		require.NoError(t, err)
		require.NoError(t, store.AppendSegment(ctx, conversationID, s0.Version(), contexty.SegmentHistory,
			contexty.TextMessage(contexty.RoleUser, "old")))
		s1, err := store.Load(ctx, conversationID)
		require.NoError(t, err)
		require.NoError(
			t,
			store.UpdateSegment(ctx, conversationID, s1.Version(), contexty.SegmentHistory, []contexty.Message{
				contexty.TextMessage(contexty.RoleAssistant, "new"),
			}),
		)
		snap, err := store.Load(ctx, conversationID)
		require.NoError(t, err)
		assert.Equal(t, "new", snap.Segment(contexty.SegmentHistory)[0].TextContent())
	})

	t.Run("ttl enabled", func(t *testing.T) {
		withTTL := New(client, WithTTL(24*time.Hour))
		s0, err := withTTL.Load(ctx, "thread-ttl")
		require.NoError(t, err)
		require.NoError(t, withTTL.AppendSegment(ctx, "thread-ttl", s0.Version(), contexty.SegmentHistory,
			contexty.TextMessage(contexty.RoleUser, "ttl")))
		ttlData, err := client.TTL(ctx, defaultKeyPrefix+"thread-ttl:data").Result()
		require.NoError(t, err)
		assert.Greater(t, ttlData, time.Duration(0))
	})

	t.Run("stale version conflict", func(t *testing.T) {
		store := New(client)
		conversationID := "thread-conflict"
		s0, err := store.Load(ctx, conversationID)
		require.NoError(t, err)
		require.NoError(t, store.AppendSegment(ctx, conversationID, s0.Version(), contexty.SegmentHistory,
			contexty.TextMessage(contexty.RoleUser, "first")))
		err = store.AppendSegment(ctx, conversationID, s0.Version(), contexty.SegmentHistory,
			contexty.TextMessage(contexty.RoleUser, "stale"))
		require.Error(t, err)
		assert.ErrorIs(t, err, contexty.ErrConversationVersionConflict)
	})

	t.Run("clear no-op on missing thread", func(t *testing.T) {
		store := New(client)
		require.NoError(t, store.Clear(ctx, "missing-thread", 0))
		snap, err := store.Load(ctx, "missing-thread")
		require.NoError(t, err)
		assert.Equal(t, int64(0), snap.Version())
	})

	t.Run("clear stale version conflict", func(t *testing.T) {
		store := New(client)
		conversationID := "thread-clear-stale"
		s0, err := store.Load(ctx, conversationID)
		require.NoError(t, err)
		require.NoError(t, store.AppendSegment(ctx, conversationID, s0.Version(), contexty.SegmentHistory,
			contexty.TextMessage(contexty.RoleUser, "data")))
		err = store.Clear(ctx, conversationID, 0)
		require.Error(t, err)
		assert.ErrorIs(t, err, contexty.ErrConversationVersionConflict)
	})

	t.Run("version without payload returns unavailable", func(t *testing.T) {
		conversationID := "thread-corrupt"
		require.NoError(t, client.Set(ctx, defaultKeyPrefix+conversationID+":ver", "2", 0).Err())
		store := New(client)
		_, err := store.Load(ctx, conversationID)
		require.Error(t, err)
		assert.ErrorIs(t, err, contexty.ErrUnavailable)
	})

	t.Run("clear and isolation", func(t *testing.T) {
		store := New(client)
		sa, err := store.Load(ctx, "thread-a")
		require.NoError(t, err)
		require.NoError(t, store.AppendSegment(ctx, "thread-a", sa.Version(), contexty.SegmentHistory,
			contexty.TextMessage(contexty.RoleUser, "A")))
		sb, err := store.Load(ctx, "thread-b")
		require.NoError(t, err)
		require.NoError(t, store.AppendSegment(ctx, "thread-b", sb.Version(), contexty.SegmentHistory,
			contexty.TextMessage(contexty.RoleUser, "B")))

		sa2, err := store.Load(ctx, "thread-a")
		require.NoError(t, err)
		require.NoError(t, store.Clear(ctx, "thread-a", sa2.Version()))

		emptyA, err := store.Load(ctx, "thread-a")
		require.NoError(t, err)
		assert.Equal(t, int64(0), emptyA.Version())
		assert.Empty(t, emptyA.Segment(contexty.SegmentHistory))

		msgsB, err := store.Load(ctx, "thread-b")
		require.NoError(t, err)
		assert.Equal(t, "B", msgsB.Segment(contexty.SegmentHistory)[0].TextContent())
	})

	t.Run("semantic round trip", func(t *testing.T) {
		store := New(client)
		conversationID := "thread-semantic"
		s0, err := store.Load(ctx, conversationID)
		require.NoError(t, err)
		require.NoError(t, store.UpdateSegment(ctx, conversationID, s0.Version(), contexty.SegmentHistory,
			[]contexty.Message{semanticFixtureMessage()}))
		assertSemanticRoundTrip(t, ctx, store, conversationID)
	})

	t.Run("expanded semantic round trip", func(t *testing.T) {
		store := New(client, WithCodec(contexty.ConversationCodec{
			Provenance: contexty.DefaultProvenanceRegistry(),
		}))
		conversationID := "thread-semantic-expanded"
		require.NoError(t, persistExpandedSemanticFixture(ctx, store, conversationID))
		assertExpandedSemanticRoundTrip(t, ctx, store, conversationID)
	})
}

func semanticFixtureMessage() contexty.Message {
	ts := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	return contexty.Message{
		Role: contexty.RoleAssistant,
		Parts: []contexty.ContentPart{
			contexty.ToolCallPart{ID: "tc-1", Name: "search", Arguments: `{"q":"go"}`},
			contexty.ToolResultPart{ToolCallID: "tc-1", Content: "result"},
		},
		Annotations: contexty.Annotations{Timestamp: &ts, RefID: "turn-1"},
		Provenance:  contexty.UserProvenance{Channel: "api", UserID: "u1"},
	}
}

//nolint:revive // testing.T must precede context in test helpers
func assertSemanticRoundTrip(t *testing.T, ctx context.Context, store *Store, conversationID string) {
	t.Helper()
	snap, err := store.Load(ctx, conversationID)
	require.NoError(t, err)
	msgs := snap.Segment(contexty.SegmentHistory)
	require.Len(t, msgs, 1)
	got := msgs[0]
	require.Len(t, got.ToolCallParts(), 1)
	require.Len(t, got.ToolResultParts(), 1)
	prov, ok := got.Provenance.(contexty.UserProvenance)
	require.True(t, ok)
	assert.Equal(t, "api", prov.Channel)
	assert.Equal(t, "turn-1", got.Annotations.RefID)
}

func persistExpandedSemanticFixture(ctx context.Context, store *Store, conversationID string) error {
	s0, err := store.Load(ctx, conversationID)
	if err != nil {
		return err
	}
	if err = store.UpdateSegment(ctx, conversationID, s0.Version(), contexty.SegmentSystem,
		[]contexty.Message{expandedSystemMessage()}); err != nil {
		return err
	}
	s1, err := store.Load(ctx, conversationID)
	if err != nil {
		return err
	}
	if err = store.UpdateSegment(ctx, conversationID, s1.Version(), contexty.SegmentHistory,
		[]contexty.Message{expandedHistoryMessage()}); err != nil {
		return err
	}
	s2, err := store.Load(ctx, conversationID)
	if err != nil {
		return err
	}
	return store.UpdateSegment(ctx, conversationID, s2.Version(), contexty.SegmentTools,
		[]contexty.Message{semanticFixtureMessage()})
}

func expandedSystemMessage() contexty.Message {
	return contexty.Message{
		Role:       contexty.RoleSystem,
		Parts:      []contexty.ContentPart{contexty.TextPart{Text: "system prompt"}},
		Provenance: contexty.SystemProvenance{Component: "bootstrap"},
	}
}

func expandedHistoryMessage() contexty.Message {
	ts := time.Date(2025, 6, 2, 9, 0, 0, 0, time.UTC)
	return contexty.Message{
		ID:   "msg-expanded-hist",
		Role: contexty.RoleUser,
		Parts: []contexty.ContentPart{
			contexty.TextPart{Text: "see image"},
			contexty.ImagePart{URL: "https://example.com/a.png", Detail: "low"},
		},
		Annotations: contexty.Annotations{Timestamp: &ts, RefID: "img-1"},
		Attributes:  contexty.Attributes{"tier": "premium", "count": float64(2)},
		Provenance:  contexty.UserProvenance{Channel: "web", UserID: "u2"},
	}
}

//nolint:revive // testing.T must precede context in test helpers
func assertExpandedSemanticRoundTrip(t *testing.T, ctx context.Context, store *Store, conversationID string) {
	t.Helper()
	snap, err := store.Load(ctx, conversationID)
	require.NoError(t, err)
	sys := snap.Segment(contexty.SegmentSystem)
	require.Len(t, sys, 1)
	sysProv, ok := sys[0].Provenance.(contexty.SystemProvenance)
	require.True(t, ok)
	assert.Equal(t, "bootstrap", sysProv.Component)

	history := snap.Segment(contexty.SegmentHistory)
	require.Len(t, history, 1)
	assert.Equal(t, "msg-expanded-hist", history[0].ID)
	assert.Equal(t, "premium", history[0].Attributes["tier"])
	assert.InEpsilon(t, float64(2), history[0].Attributes["count"], 0)
	require.Len(t, history[0].Parts, 2)
	_, hasImage := history[0].Parts[1].(contexty.ImagePart)
	require.True(t, hasImage)
	userProv, ok := history[0].Provenance.(contexty.UserProvenance)
	require.True(t, ok)
	assert.Equal(t, "web", userProv.Channel)

	tools := snap.Segment(contexty.SegmentTools)
	require.Len(t, tools, 1)
	require.Len(t, tools[0].ToolCallParts(), 1)
}
