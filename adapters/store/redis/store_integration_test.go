package redis

import (
	"context"
	"math"
	"strconv"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/contexty/testutil"
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
	t.Run("exhausted revision", func(t *testing.T) {
		// Arrange: a live namespace has consumed its final int64 token.
		store := New(client)
		id := "exhausted"
		require.NoError(
			t,
			client.MSet(ctx, store.verKey(id), strconv.FormatInt(math.MaxInt64, 10), store.dataKey(id), "").Err(),
		)
		before, loadErr := store.LoadState(ctx, id)
		require.NoError(t, loadErr)
		// Act: neither commit nor clear may wrap or partially erase its identity.
		require.ErrorIs(
			t,
			store.CommitState(ctx, id, before.Version(), contexty.ConversationDelta{}),
			contexty.ErrConversationVersionExhausted,
		)
		require.ErrorIs(t, store.ClearState(ctx, id, before.Version()), contexty.ErrConversationVersionExhausted)
		// Assert.
		after, loadErr := store.LoadState(ctx, id)
		require.NoError(t, loadErr)
		require.Equal(t, before, after)
	})
	t.Run("write permission failure is atomic", func(t *testing.T) {
		for _, rule := range []string{"-mset", "-pexpire"} {
			t.Run(rule, func(t *testing.T) {
				// Arrange: EVAL and reads work; a required write is forbidden.
				user := "checkpoint-denied-" + rule[1:]
				require.NoError(
					t,
					client.Do(ctx, "ACL", "SETUSER", user, "on", ">fixture-password", "~*", "+eval", "+get", "+mset", "+pexpire", rule).
						Err(),
				)
				limited := goredis.NewClient(
					&goredis.Options{Addr: endpoint, Username: user, Password: "fixture-password"},
				)
				t.Cleanup(func() { require.NoError(t, limited.Close()) })
				store := New(limited, WithTTL(time.Hour))
				id := user
				// Act: the failed write must not consume a revision or publish payload.
				err := store.CommitState(ctx, id, 0, contexty.ConversationDelta{
					Operation: contexty.DeltaAppendMessages,
					Segment:   contexty.SegmentHistory,
					Messages:  []contexty.Message{contexty.TextMessage(contexty.RoleUser, "not published")},
				})
				require.Error(t, err)
				// Assert via the privileged reader, independently of the restricted connection.
				exists, countErr := client.Exists(ctx, store.verKey(id), store.dataKey(id)).Result()
				require.NoError(t, countErr)
				require.Zero(t, exists)
			})
		}
	})

	t.Run("fixture OCC conformance", func(t *testing.T) {
		testutil.CheckStateStore(t, New(client), "fixture-conformance")
	})
	t.Run("fixture expiry conformance", func(t *testing.T) {
		// Arrange: a live writer holds the token before payload expiration.
		store := New(client, WithTTL(time.Hour))
		id := "fixture-expiry"
		delta := contexty.ConversationDelta{Operation: contexty.DeltaAppendMessages, Segment: contexty.SegmentHistory,
			Messages: []contexty.Message{contexty.TextMessage(contexty.RoleUser, "old private data")}}
		require.NoError(t, store.CommitState(ctx, id, 0, delta))
		stale, err := store.LoadState(ctx, id)
		require.NoError(t, err)
		// Act: actual Redis expiry, deterministically triggered without sleeping.
		require.NoError(t, client.PExpire(ctx, store.dataKey(id), -time.Millisecond).Err())
		// Assert: CAS itself notices expiry, even without a preceding LoadState.
		require.ErrorIs(t, store.CommitState(ctx, id, stale.Version(), delta), contexty.ErrConversationVersionConflict)
		require.ErrorIs(t, store.ClearState(ctx, id, stale.Version()), contexty.ErrConversationVersionConflict)
		empty, err := store.LoadState(ctx, id)
		require.NoError(t, err)
		require.Greater(t, empty.Version(), stale.Version())
		require.Empty(t, empty.Segment(contexty.SegmentHistory))
		repeated, err := store.LoadState(ctx, id)
		require.NoError(t, err)
		require.Equal(t, empty.Version(), repeated.Version())
		require.NoError(t, store.CommitState(ctx, id, empty.Version(), delta))
		ttl, err := client.TTL(ctx, store.verKey(id)).Result()
		require.NoError(t, err)
		require.Equal(t, -time.Nanosecond, ttl)
	})

	t.Run("fixture exact large OCC revision", func(t *testing.T) {
		// Arrange: integers beyond Lua's exact floating-point range.
		store := New(client)
		id := "fixture-large-revision"
		const version = int64(9007199254740993)
		snapshot := contexty.EmptySnapshot().WithVersion(version)
		wire, err := store.codec.Encode(snapshot)
		require.NoError(t, err)
		require.NoError(t, client.Set(ctx, store.verKey(id), version, 0).Err())
		require.NoError(t, client.Set(ctx, store.dataKey(id), wire, 0).Err())
		// Act: adjacent tokens must still be distinct to CAS.
		require.ErrorIs(t, store.ClearState(ctx, id, version-1), contexty.ErrConversationVersionConflict)
		require.NoError(t, store.ClearState(ctx, id, version))
		current, err := store.LoadState(ctx, id)
		// Assert: no float rounding and no loss of monotonicity.
		require.NoError(t, err)
		require.Equal(t, version+1, current.Version())
	})

	t.Run("empty load", func(t *testing.T) {
		store := New(client)
		snap, err := store.LoadState(ctx, "empty")
		require.NoError(t, err)
		assert.Empty(t, snap.Segment(contexty.SegmentHistory))
		assert.Equal(t, int64(0), snap.Version())
	})

	t.Run("append segment", func(t *testing.T) {
		store := New(client)
		conversationID := "thread-ordered"
		s0, err := store.LoadState(ctx, conversationID)
		require.NoError(t, err)
		require.NoError(t, appendHistory(ctx, store, conversationID, s0.Version(),
			contexty.TextMessage(contexty.RoleUser, "one"),
			contexty.TextMessage(contexty.RoleAssistant, "two"),
		))
		s1, err := store.LoadState(ctx, conversationID)
		require.NoError(t, err)
		require.NoError(t, appendHistory(ctx, store, conversationID, s1.Version(),
			contexty.TextMessage(contexty.RoleUser, "three")))

		snap, err := store.LoadState(ctx, conversationID)
		require.NoError(t, err)
		assert.Len(t, snap.Segment(contexty.SegmentHistory), 3)
		assert.Equal(t, int64(2), snap.Version())
	})

	t.Run("update segment", func(t *testing.T) {
		store := New(client)
		conversationID := "thread-save"
		s0, err := store.LoadState(ctx, conversationID)
		require.NoError(t, err)
		require.NoError(t, appendHistory(ctx, store, conversationID, s0.Version(),
			contexty.TextMessage(contexty.RoleUser, "old")))
		s1, err := store.LoadState(ctx, conversationID)
		require.NoError(t, err)
		require.NoError(
			t,
			updateSegment(ctx, store, conversationID, s1.Version(), contexty.SegmentHistory, []contexty.Message{
				contexty.TextMessage(contexty.RoleAssistant, "new"),
			}),
		)
		snap, err := store.LoadState(ctx, conversationID)
		require.NoError(t, err)
		assert.Equal(t, "new", snap.Segment(contexty.SegmentHistory)[0].TextContent())
	})

	t.Run("apply delta state and OCC", func(t *testing.T) {
		store := New(client)
		conversationID := "thread-delta"
		require.NoError(t, store.CommitState(ctx, conversationID, 0, contexty.ConversationDelta{
			Operation: contexty.DeltaReplaceSegment,
			Segment:   contexty.SegmentHistory,
			Messages: []contexty.Message{
				contexty.TextMessage(contexty.RoleUser, "delta"),
			},
		}))
		state, err := store.LoadState(ctx, conversationID)
		require.NoError(t, err)
		assert.Equal(t, int64(1), state.Version())
		assert.Equal(t, "delta", state.Segment(contexty.SegmentHistory)[0].TextContent())

		artifact := contexty.NewMemoryBlock(
			"memory-1",
			contexty.TextPayload("memory"),
		).ContextArtifact
		require.NoError(t, store.CommitState(ctx, conversationID, state.Version(), contexty.ConversationDelta{
			Operation: contexty.DeltaUpsertArtifact,
			Artifact:  &artifact,
		}))
		state, err = store.LoadState(ctx, conversationID)
		require.NoError(t, err)
		require.Len(t, state.Artifacts(), 1)
		assert.Equal(t, "memory-1", state.Artifacts()[0].ID)

		err = store.CommitState(ctx, conversationID, 1, contexty.ConversationDelta{
			Operation: contexty.DeltaAppendMessages,
			Segment:   contexty.SegmentHistory,
			Messages:  []contexty.Message{contexty.TextMessage(contexty.RoleUser, "stale")},
		})
		require.ErrorIs(t, err, contexty.ErrConversationVersionConflict)
	})

	t.Run("ttl enabled", func(t *testing.T) {
		withTTL := New(client, WithTTL(24*time.Hour))
		s0, err := withTTL.LoadState(ctx, "thread-ttl")
		require.NoError(t, err)
		require.NoError(t, appendHistory(ctx, withTTL, "thread-ttl", s0.Version(),
			contexty.TextMessage(contexty.RoleUser, "ttl")))
		ttlData, err := client.TTL(ctx, withTTL.dataKey("thread-ttl")).Result()
		require.NoError(t, err)
		assert.Greater(t, ttlData, time.Duration(0))
	})

	t.Run("stale version conflict", func(t *testing.T) {
		store := New(client)
		conversationID := "thread-conflict"
		s0, err := store.LoadState(ctx, conversationID)
		require.NoError(t, err)
		require.NoError(t, appendHistory(ctx, store, conversationID, s0.Version(),
			contexty.TextMessage(contexty.RoleUser, "first")))
		err = appendHistory(ctx, store, conversationID, s0.Version(),
			contexty.TextMessage(contexty.RoleUser, "stale"))
		require.Error(t, err)
		assert.ErrorIs(t, err, contexty.ErrConversationVersionConflict)
	})

	t.Run("clear creates empty OCC tombstone on missing thread", func(t *testing.T) {
		store := New(client)
		require.NoError(t, store.ClearState(ctx, "missing-thread", 0))
		snap, err := store.LoadState(ctx, "missing-thread")
		require.NoError(t, err)
		assert.Equal(t, int64(1), snap.Version())
	})

	t.Run("clear stale version conflict", func(t *testing.T) {
		store := New(client)
		conversationID := "thread-clear-stale"
		s0, err := store.LoadState(ctx, conversationID)
		require.NoError(t, err)
		require.NoError(t, appendHistory(ctx, store, conversationID, s0.Version(),
			contexty.TextMessage(contexty.RoleUser, "data")))
		err = store.ClearState(ctx, conversationID, 0)
		require.Error(t, err)
		assert.ErrorIs(t, err, contexty.ErrConversationVersionConflict)
	})

	t.Run("version without payload returns unavailable", func(t *testing.T) {
		conversationID := "thread-corrupt"
		store := New(client)
		require.NoError(t, client.Set(ctx, store.verKey(conversationID), "2", 0).Err())
		_, err := store.LoadState(ctx, conversationID)
		require.Error(t, err)
		assert.ErrorIs(t, err, contexty.ErrUnavailable)
	})

	t.Run("clear and isolation", func(t *testing.T) {
		store := New(client)
		sa, err := store.LoadState(ctx, "thread-a")
		require.NoError(t, err)
		require.NoError(t, appendHistory(ctx, store, "thread-a", sa.Version(),
			contexty.TextMessage(contexty.RoleUser, "A")))
		sb, err := store.LoadState(ctx, "thread-b")
		require.NoError(t, err)
		require.NoError(t, appendHistory(ctx, store, "thread-b", sb.Version(),
			contexty.TextMessage(contexty.RoleUser, "B")))

		sa2, err := store.LoadState(ctx, "thread-a")
		require.NoError(t, err)
		require.NoError(t, store.ClearState(ctx, "thread-a", sa2.Version()))

		emptyA, err := store.LoadState(ctx, "thread-a")
		require.NoError(t, err)
		assert.Equal(t, sa2.Version()+1, emptyA.Version())
		assert.Empty(t, emptyA.Segment(contexty.SegmentHistory))

		msgsB, err := store.LoadState(ctx, "thread-b")
		require.NoError(t, err)
		assert.Equal(t, "B", msgsB.Segment(contexty.SegmentHistory)[0].TextContent())
	})

	t.Run("semantic round trip", func(t *testing.T) {
		store := New(client)
		conversationID := "thread-semantic"
		s0, err := store.LoadState(ctx, conversationID)
		require.NoError(t, err)
		require.NoError(t, updateSegment(ctx, store, conversationID, s0.Version(), contexty.SegmentHistory,
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
			contexty.ToolCallPart{ID: "tc-1", Name: "search", Arguments: contexty.JSONPayload(`{"q":"go"}`)},
			contexty.ToolResultPart{ToolCallID: "tc-1", Payload: contexty.TextPayload("result")},
		},
		Annotations: contexty.Annotations{Timestamp: &ts},
		SourceRefs: []contexty.SourceRef{{
			Namespace: "messages",
			Kind:      "external",
			ID:        "turn-1",
		}},
		Provenance: contexty.UserProvenance{Channel: "api", UserID: "u1"},
	}
}

//nolint:revive // testing.T must precede context in test helpers
func assertSemanticRoundTrip(t *testing.T, ctx context.Context, store *Store, conversationID string) {
	t.Helper()
	snap, err := store.LoadState(ctx, conversationID)
	require.NoError(t, err)
	msgs := snap.Segment(contexty.SegmentHistory)
	require.Len(t, msgs, 1)
	got := msgs[0]
	require.Len(t, got.ToolCallParts(), 1)
	require.Len(t, got.ToolResultParts(), 1)
	prov, ok := got.Provenance.(contexty.UserProvenance)
	require.True(t, ok)
	assert.Equal(t, "api", prov.Channel)
	require.Len(t, got.SourceRefs, 1)
	assert.Equal(t, "turn-1", got.SourceRefs[0].ID)
}

func persistExpandedSemanticFixture(ctx context.Context, store *Store, conversationID string) error {
	s0, err := store.LoadState(ctx, conversationID)
	if err != nil {
		return err
	}
	if err = updateSegment(ctx, store, conversationID, s0.Version(), contexty.SegmentSystem,
		[]contexty.Message{expandedSystemMessage()}); err != nil {
		return err
	}
	s1, err := store.LoadState(ctx, conversationID)
	if err != nil {
		return err
	}
	if err = updateSegment(ctx, store, conversationID, s1.Version(), contexty.SegmentHistory,
		[]contexty.Message{expandedHistoryMessage()}); err != nil {
		return err
	}
	s2, err := store.LoadState(ctx, conversationID)
	if err != nil {
		return err
	}
	return updateSegment(ctx, store, conversationID, s2.Version(), contexty.SegmentTools,
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
		Annotations: contexty.Annotations{Timestamp: &ts},
		SourceRefs: []contexty.SourceRef{{
			Namespace:    "tenant",
			Kind:         "profile",
			ID:           "premium",
			CheckpointID: "2",
		}},
		Origin:     &contexty.MessageOrigin{TemplateID: "agents/sales", LayerID: "persona"},
		LLMCache:   &contexty.CachePolicyRef{Type: "ephemeral"},
		Provenance: contexty.UserProvenance{Channel: "web", UserID: "u2"},
	}
}

//nolint:revive // testing.T must precede context in test helpers
func assertExpandedSemanticRoundTrip(t *testing.T, ctx context.Context, store *Store, conversationID string) {
	t.Helper()
	snap, err := store.LoadState(ctx, conversationID)
	require.NoError(t, err)
	sys := snap.Segment(contexty.SegmentSystem)
	require.Len(t, sys, 1)
	sysProv, ok := sys[0].Provenance.(contexty.SystemProvenance)
	require.True(t, ok)
	assert.Equal(t, "bootstrap", sysProv.Component)

	history := snap.Segment(contexty.SegmentHistory)
	require.Len(t, history, 1)
	assert.Equal(t, "msg-expanded-hist", history[0].ID)
	require.Len(t, history[0].SourceRefs, 1)
	assert.Equal(t, "tenant", history[0].SourceRefs[0].Namespace)
	assert.Equal(t, "profile", history[0].SourceRefs[0].Kind)
	assert.Equal(t, "premium", history[0].SourceRefs[0].ID)
	assert.Equal(t, "2", history[0].SourceRefs[0].CheckpointID)
	require.NotNil(t, history[0].Origin)
	assert.Equal(t, "agents/sales", history[0].Origin.TemplateID)
	assert.Equal(t, "persona", history[0].Origin.LayerID)
	require.NotNil(t, history[0].LLMCache)
	assert.Equal(t, "ephemeral", history[0].LLMCache.Type)
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
