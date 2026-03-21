package redis

import (
	"context"
	"encoding/json"
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
	testcontainers.SkipIfProviderIsNotHealthy(t)

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
		assert.Empty(t, snap.Messages)
		assert.Equal(t, int64(0), snap.Version)
	})

	t.Run("ordered append and repeated append", func(t *testing.T) {
		store := New(client)
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
		store := New(client)
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
		store := New(client)
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

	t.Run("custom serializer and key prefix", func(t *testing.T) {
		serializer := &countingSerializer{}
		store := New(client, WithKeyPrefix("custom:"), WithSerializer(serializer))
		threadID := "thread-custom"
		s0, err := store.Load(ctx, threadID)
		require.NoError(t, err)
		require.NoError(t, store.Append(ctx, threadID, s0.Version, contexty.TextMessage(contexty.RoleUser, "custom")))

		snap, err := store.Load(ctx, threadID)
		require.NoError(t, err)
		assert.Equal(t, []contexty.Message{contexty.TextMessage(contexty.RoleUser, "custom")}, snap.Messages)
		assert.Positive(t, serializer.marshalCalls)
		assert.Positive(t, serializer.unmarshalCalls)

		raw, err := client.LRange(ctx, "custom:"+threadID, 0, -1).Result()
		require.NoError(t, err)
		require.Len(t, raw, 1)
		ver, err := client.Get(ctx, "custom:"+threadID+":ver").Result()
		require.NoError(t, err)
		assert.Equal(t, "1", ver)
	})

	t.Run("ttl enabled and disabled", func(t *testing.T) {
		withTTL := New(client, WithTTL(24*time.Hour))
		s0, err := withTTL.Load(ctx, "thread-ttl")
		require.NoError(t, err)
		require.NoError(
			t,
			withTTL.Append(ctx, "thread-ttl", s0.Version, contexty.TextMessage(contexty.RoleUser, "ttl")),
		)
		ttlList, err := client.TTL(ctx, defaultKeyPrefix+"thread-ttl").Result()
		require.NoError(t, err)
		assert.Greater(t, ttlList, time.Duration(0))
		ttlVer, err := client.TTL(ctx, defaultKeyPrefix+"thread-ttl"+":ver").Result()
		require.NoError(t, err)
		assert.Greater(t, ttlVer, time.Duration(0))

		withoutTTL := New(client)
		s1, err := withoutTTL.Load(ctx, "thread-no-ttl")
		require.NoError(t, err)
		require.NoError(
			t,
			withoutTTL.Append(ctx, "thread-no-ttl", s1.Version, contexty.TextMessage(contexty.RoleUser, "no ttl")),
		)
		ttl, err := client.TTL(ctx, defaultKeyPrefix+"thread-no-ttl").Result()
		require.NoError(t, err)
		assert.Equal(t, time.Duration(-1), ttl)
		ttlV, err := client.TTL(ctx, defaultKeyPrefix+"thread-no-ttl"+":ver").Result()
		require.NoError(t, err)
		assert.Equal(t, time.Duration(-1), ttlV)
	})

	t.Run("stale version returns conflict", func(t *testing.T) {
		store := New(client)
		threadID := "thread-conflict"
		s0, err := store.Load(ctx, threadID)
		require.NoError(t, err)
		require.NoError(t, store.Append(ctx, threadID, s0.Version, contexty.TextMessage(contexty.RoleUser, "first")))
		err = store.Append(ctx, threadID, s0.Version, contexty.TextMessage(contexty.RoleUser, "stale"))
		require.Error(t, err)
		assert.ErrorIs(t, err, contexty.ErrHistoryVersionConflict)
	})

	t.Run("corrupted payload returns unmarshal error", func(t *testing.T) {
		threadID := "thread-corrupted"
		require.NoError(t, client.RPush(ctx, defaultKeyPrefix+threadID, "1").Err())

		store := New(client)
		_, err := store.Load(ctx, threadID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode")
	})
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
