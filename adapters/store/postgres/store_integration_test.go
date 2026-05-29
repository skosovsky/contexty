package postgres

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/skosovsky/contexty"
)

const schemaTemplate = `
CREATE TABLE %s (
    thread_id VARCHAR(255) PRIMARY KEY,
    version BIGINT NOT NULL DEFAULT 0,
    segments JSONB NOT NULL DEFAULT '{}'
);
`

func TestStoreIntegration(t *testing.T) {
	requireDocker(t)

	ctx := context.Background()
	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("contexty"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, testcontainers.TerminateContainer(container))
	})

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	createTable(ctx, t, pool, "contexty_conversations")
	createTable(ctx, t, pool, "custom_contexty_conversations")

	t.Run("empty load", func(t *testing.T) {
		store := New(pool)
		snap, err := store.Load(ctx, "empty")
		require.NoError(t, err)
		assert.Empty(t, snap.Segment(contexty.SegmentHistory))
		assert.Equal(t, int64(0), snap.Version())
	})

	t.Run("append segment and OCC", func(t *testing.T) {
		store := New(pool)
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
		msgs := snap.Segment(contexty.SegmentHistory)
		require.Len(t, msgs, 3)
		assert.Equal(t, "one", msgs[0].TextContent())
		assert.Equal(t, int64(2), snap.Version())
	})

	t.Run("update segment overwrite", func(t *testing.T) {
		store := New(pool)
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

	t.Run("clear no-op on missing thread", func(t *testing.T) {
		store := New(pool)
		require.NoError(t, store.Clear(ctx, "missing-thread", 0))
		snap, err := store.Load(ctx, "missing-thread")
		require.NoError(t, err)
		assert.Equal(t, int64(0), snap.Version())
		assert.Empty(t, snap.Segment(contexty.SegmentHistory))
	})

	t.Run("clear stale version conflict", func(t *testing.T) {
		store := New(pool)
		conversationID := "thread-clear-stale"
		s0, err := store.Load(ctx, conversationID)
		require.NoError(t, err)
		require.NoError(t, store.AppendSegment(ctx, conversationID, s0.Version(), contexty.SegmentHistory,
			contexty.TextMessage(contexty.RoleUser, "data")))
		err = store.Clear(ctx, conversationID, 0)
		require.Error(t, err)
		assert.ErrorIs(t, err, contexty.ErrConversationVersionConflict)
	})

	t.Run("concurrent first write maps to version conflict", func(t *testing.T) {
		store := New(pool)
		conversationID := "thread-concurrent-create"
		var barrier sync.WaitGroup
		barrier.Add(2)
		release := make(chan struct{})
		errCh := make(chan error, 2)
		worker := func() {
			barrier.Done()
			<-release
			errCh <- store.AppendSegment(ctx, conversationID, 0, contexty.SegmentHistory,
				contexty.TextMessage(contexty.RoleUser, "race"))
		}
		go worker()
		go worker()
		barrier.Wait()
		close(release)
		var errs []error
		for range 2 {
			if err := <-errCh; err != nil {
				errs = append(errs, err)
			}
		}
		require.Len(t, errs, 1)
		require.ErrorIs(t, errs[0], contexty.ErrConversationVersionConflict)

		snap, err := store.Load(ctx, conversationID)
		require.NoError(t, err)
		assert.Equal(t, int64(1), snap.Version())
		assert.Len(t, snap.Segment(contexty.SegmentHistory), 1)
	})

	t.Run("clear and isolation", func(t *testing.T) {
		store := New(pool)
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

	t.Run("stale version returns conflict", func(t *testing.T) {
		store := New(pool)
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

	t.Run("semantic round trip", func(t *testing.T) {
		store := New(pool)
		conversationID := "thread-semantic"
		s0, err := store.Load(ctx, conversationID)
		require.NoError(t, err)
		require.NoError(t, store.UpdateSegment(ctx, conversationID, s0.Version(), contexty.SegmentHistory,
			[]contexty.Message{semanticFixtureMessage()}))
		assertSemanticRoundTrip(t, ctx, store, conversationID)
	})

	t.Run("expanded semantic round trip", func(t *testing.T) {
		store := New(pool, WithCodec(contexty.ConversationCodec{
			Provenance: contexty.DefaultProvenanceRegistry(),
		}))
		conversationID := "thread-semantic-expanded"
		require.NoError(t, persistExpandedSemanticFixture(ctx, store, conversationID))
		assertExpandedSemanticRoundTrip(t, ctx, store, conversationID)
	})

	t.Run("custom table", func(t *testing.T) {
		store := New(pool, WithTableName("custom_contexty_conversations"))
		conversationID := "thread-custom"
		s0, err := store.Load(ctx, conversationID)
		require.NoError(t, err)
		require.NoError(t, store.AppendSegment(ctx, conversationID, s0.Version(), contexty.SegmentHistory,
			contexty.TextMessage(contexty.RoleUser, "custom")))
		snap, err := store.Load(ctx, conversationID)
		require.NoError(t, err)
		assert.Equal(t, "custom", snap.Segment(contexty.SegmentHistory)[0].TextContent())
	})
}

func createTable(ctx context.Context, t *testing.T, pool *pgxpool.Pool, table string) {
	t.Helper()
	_, err := pool.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, fmt.Sprintf(schemaTemplate, table))
	require.NoError(t, err)
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
		Role: contexty.RoleUser,
		Parts: []contexty.ContentPart{
			contexty.TextPart{Text: "see image"},
			contexty.ImagePart{URL: "https://example.com/a.png", Detail: "low"},
		},
		Annotations: contexty.Annotations{Timestamp: &ts, RefID: "img-1"},
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
