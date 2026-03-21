package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/skosovsky/contexty"
)

const schemaTemplate = `
CREATE TABLE %s (
    id BIGSERIAL PRIMARY KEY,
    thread_id VARCHAR(255) NOT NULL,
    message_data JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX %s ON %s(thread_id, id);
CREATE TABLE %s (
    thread_id VARCHAR(255) PRIMARY KEY,
    version BIGINT NOT NULL DEFAULT 0
);
`

func TestStoreIntegration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

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

	createTable(ctx, t, pool, "contexty_messages", "idx_contexty_thread")
	createTable(ctx, t, pool, "custom_contexty_messages", "idx_custom_contexty_thread")

	t.Run("empty load", func(t *testing.T) {
		store := New(pool)
		snap, err := store.Load(ctx, "empty")
		require.NoError(t, err)
		assert.Empty(t, snap.Messages)
		assert.Equal(t, int64(0), snap.Version)
	})

	t.Run("ordered append and repeated append", func(t *testing.T) {
		store := New(pool)
		threadID := "thread-ordered"
		s0, err := store.Load(ctx, threadID)
		require.NoError(t, err)
		require.NoError(t, store.Append(ctx, threadID, s0.Version,
			contexty.TextMessage(contexty.RoleUser, "one"),
			contexty.TextMessage(contexty.RoleAssistant, "two"),
		))
		s1, err := store.Load(ctx, threadID)
		require.NoError(t, err)
		require.NoError(t, store.Append(ctx, threadID, s1.Version, contexty.TextMessage(contexty.RoleUser, "three")))

		snap, err := store.Load(ctx, threadID)
		require.NoError(t, err)
		assert.Equal(t, []contexty.Message{
			contexty.TextMessage(contexty.RoleUser, "one"),
			contexty.TextMessage(contexty.RoleAssistant, "two"),
			contexty.TextMessage(contexty.RoleUser, "three"),
		}, snap.Messages)
		assert.Equal(t, int64(2), snap.Version)
	})

	t.Run("save overwrite and save empty", func(t *testing.T) {
		store := New(pool)
		threadID := "thread-save"
		s0, err := store.Load(ctx, threadID)
		require.NoError(t, err)
		require.NoError(t, store.Append(ctx, threadID, s0.Version, contexty.TextMessage(contexty.RoleUser, "old")))

		s1, err := store.Load(ctx, threadID)
		require.NoError(t, err)
		require.NoError(t, store.Save(ctx, threadID, s1.Version, []contexty.Message{
			contexty.TextMessage(contexty.RoleAssistant, "new"),
		}))
		snap, err := store.Load(ctx, threadID)
		require.NoError(t, err)
		assert.Equal(t, []contexty.Message{contexty.TextMessage(contexty.RoleAssistant, "new")}, snap.Messages)

		s2, err := store.Load(ctx, threadID)
		require.NoError(t, err)
		require.NoError(t, store.Save(ctx, threadID, s2.Version, nil))
		snap, err = store.Load(ctx, threadID)
		require.NoError(t, err)
		assert.Empty(t, snap.Messages)
	})

	t.Run("clear and isolation", func(t *testing.T) {
		store := New(pool)
		sa, err := store.Load(ctx, "thread-a")
		require.NoError(t, err)
		require.NoError(t, store.Append(ctx, "thread-a", sa.Version, contexty.TextMessage(contexty.RoleUser, "A")))
		sb, err := store.Load(ctx, "thread-b")
		require.NoError(t, err)
		require.NoError(t, store.Append(ctx, "thread-b", sb.Version, contexty.TextMessage(contexty.RoleUser, "B")))

		sa2, err := store.Load(ctx, "thread-a")
		require.NoError(t, err)
		require.NoError(t, store.Clear(ctx, "thread-a", sa2.Version))

		msgsA, err := store.Load(ctx, "thread-a")
		require.NoError(t, err)
		assert.Empty(t, msgsA.Messages)

		msgsB, err := store.Load(ctx, "thread-b")
		require.NoError(t, err)
		assert.Equal(t, []contexty.Message{contexty.TextMessage(contexty.RoleUser, "B")}, msgsB.Messages)
	})

	t.Run("custom serializer and table", func(t *testing.T) {
		serializer := &countingSerializer{}
		store := New(pool, WithTableName("custom_contexty_messages"), WithSerializer(serializer))
		threadID := "thread-custom"
		s0, err := store.Load(ctx, threadID)
		require.NoError(t, err)
		require.NoError(t, store.Append(ctx, threadID, s0.Version, contexty.TextMessage(contexty.RoleUser, "custom")))

		snap, err := store.Load(ctx, threadID)
		require.NoError(t, err)
		assert.Equal(t, []contexty.Message{contexty.TextMessage(contexty.RoleUser, "custom")}, snap.Messages)
		assert.Positive(t, serializer.marshalCalls)
		assert.Positive(t, serializer.unmarshalCalls)
	})

	t.Run("stale version returns conflict", func(t *testing.T) {
		store := New(pool)
		threadID := "thread-conflict"
		s0, err := store.Load(ctx, threadID)
		require.NoError(t, err)
		require.NoError(t, store.Append(ctx, threadID, s0.Version, contexty.TextMessage(contexty.RoleUser, "first")))
		err = store.Append(ctx, threadID, s0.Version, contexty.TextMessage(contexty.RoleUser, "stale"))
		require.Error(t, err)
		assert.ErrorIs(t, err, contexty.ErrHistoryVersionConflict)
	})

	t.Run("corrupted row returns unmarshal error", func(t *testing.T) {
		threadID := "thread-corrupted"
		_, err := pool.Exec(ctx,
			"INSERT INTO contexty_messages (thread_id, message_data) VALUES ($1, '1'::jsonb)",
			threadID,
		)
		require.NoError(t, err)

		store := New(pool)
		_, err = store.Load(ctx, threadID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode")
	})
}

func createTable(ctx context.Context, t *testing.T, pool *pgxpool.Pool, table string, index string) {
	t.Helper()

	meta := table + "_meta"
	_, err := pool.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", meta))
	require.NoError(t, err)

	_, err = pool.Exec(ctx, fmt.Sprintf(schemaTemplate, table, index, table, meta))
	require.NoError(t, err)
}

type countingSerializer struct {
	marshalCalls   int
	unmarshalCalls int
}

func (s *countingSerializer) Marshal(msg contexty.Message) ([]byte, error) {
	s.marshalCalls++
	return json.Marshal(msg)
}

func (s *countingSerializer) Unmarshal(data []byte, msg *contexty.Message) error {
	s.unmarshalCalls++
	return json.Unmarshal(data, msg)
}
