package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/skosovsky/contexty"
)

const defaultKeyPrefix = "contexty:conv:"

// Lua: set conversation blob if version matches, then bump.
const luaMutate = `
local expected = tonumber(ARGV[1])
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
if cur ~= expected then return redis.error_reply('CONFLICT') end
redis.call('SET', KEYS[2], ARGV[2])
redis.call('INCR', KEYS[1])
return 1
`

// Lua: clear conversation when version matches; no-op when thread absent and expected=0.
const luaClear = `
local expected = tonumber(ARGV[1])
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
if cur ~= expected then return redis.error_reply('CONFLICT') end
local hasData = redis.call('EXISTS', KEYS[2])
if expected == 0 and hasData == 0 then
  return 1
end
redis.call('DEL', KEYS[2])
redis.call('DEL', KEYS[1])
return 1
`

// Store persists conversation state in Redis.
type Store struct {
	client    goredis.UniversalClient
	codec     contexty.ConversationCodec
	keyPrefix string
	ttl       time.Duration
}

// New returns a Redis-backed ConversationStateStore.
func New(client goredis.UniversalClient, opts ...Option) *Store {
	store := &Store{
		client:    client,
		codec:     contexty.ConversationCodec{Provenance: contexty.DefaultProvenanceRegistry(), Extensions: nil},
		keyPrefix: defaultKeyPrefix,
		ttl:       0,
	}
	for _, opt := range opts {
		opt(store)
	}
	return store
}

func (s *Store) verKey(conversationID string) string {
	return s.keyPrefix + conversationID + ":ver"
}

func (s *Store) dataKey(conversationID string) string {
	return s.keyPrefix + conversationID + ":data"
}

// LoadState returns the full immutable conversation state.
func (s *Store) LoadState(ctx context.Context, conversationID string) (contexty.ConversationState, error) {
	if s.client == nil {
		return contexty.ConversationState{}, errors.New("contexty/redis: nil client")
	}
	vstr, err := s.client.Get(ctx, s.verKey(conversationID)).Result()
	switch {
	case err == nil:
		v, parseErr := strconv.ParseInt(vstr, 10, 64)
		if parseErr != nil {
			return contexty.ConversationState{}, fmt.Errorf("contexty/redis: parse version: %w", parseErr)
		}
		if v == 0 {
			return contexty.EmptySnapshot(), nil
		}
		return s.loadState(ctx, conversationID, v)
	case errors.Is(err, goredis.Nil):
		return contexty.EmptySnapshot(), nil
	default:
		return contexty.ConversationState{}, classifyRedisErr("load version", err)
	}
}

func (s *Store) loadState(
	ctx context.Context,
	conversationID string,
	version int64,
) (contexty.ConversationState, error) {
	raw, err := s.client.Get(ctx, s.dataKey(conversationID)).Result()
	if errors.Is(err, goredis.Nil) {
		if version > 0 {
			return contexty.ConversationState{}, fmt.Errorf(
				"contexty/redis: version %d without payload for thread %q: %w",
				version,
				conversationID,
				contexty.ErrUnavailable,
			)
		}
		return contexty.EmptySnapshot(), nil
	}
	if err != nil {
		return contexty.ConversationState{}, classifyRedisErr("load data", err)
	}
	snap, err := s.codec.Decode([]byte(raw))
	if err != nil {
		return contexty.ConversationState{}, fmt.Errorf("contexty/redis: decode: %w", err)
	}
	return snap.WithVersion(version), nil
}

// ApplyDelta applies an immutable state transition when expectedVersion matches.
func (s *Store) ApplyDelta(
	ctx context.Context,
	conversationID string,
	expectedVersion int64,
	delta contexty.ConversationDelta,
) error {
	return s.mutate(ctx, conversationID, expectedVersion,
		func(snap contexty.ConversationSnapshot) (contexty.ConversationSnapshot, error) {
			return contexty.ApplyDelta(snap, delta)
		})
}

// ClearState removes stored conversation state.
func (s *Store) ClearState(ctx context.Context, conversationID string, expectedVersion int64) error {
	if s.client == nil {
		return errors.New("contexty/redis: nil client")
	}
	if err := s.evalConflict(
		ctx,
		luaClear,
		[]string{s.verKey(conversationID), s.dataKey(conversationID)},
		expectedVersion,
	); err != nil {
		return err
	}
	s.maybeExpire(ctx, conversationID)
	return nil
}

func (s *Store) mutate(
	ctx context.Context,
	conversationID string,
	expectedVersion int64,
	update func(contexty.ConversationSnapshot) (contexty.ConversationSnapshot, error),
) error {
	if s.client == nil {
		return errors.New("contexty/redis: nil client")
	}
	cur, err := s.LoadState(ctx, conversationID)
	if err != nil {
		return err
	}
	if cur.Version() != expectedVersion {
		return contexty.ErrConversationVersionConflict
	}
	next, err := update(cur)
	if err != nil {
		return err
	}
	next = next.WithVersion(expectedVersion + 1)
	encoded, err := s.codec.Encode(next)
	if err != nil {
		return fmt.Errorf("contexty/redis: encode: %w", err)
	}
	if err := s.evalConflict(
		ctx,
		luaMutate,
		[]string{s.verKey(conversationID), s.dataKey(conversationID)},
		expectedVersion,
		string(encoded),
	); err != nil {
		return err
	}
	s.maybeExpire(ctx, conversationID)
	return nil
}

func (s *Store) evalConflict(ctx context.Context, script string, keys []string, args ...any) error {
	res, err := s.client.Eval(ctx, script, keys, args...).Result()
	if err != nil {
		if isRedisConflict(err) {
			return contexty.ErrConversationVersionConflict
		}
		return classifyRedisErr("eval", err)
	}
	_ = res
	return nil
}

func isRedisConflict(err error) bool {
	if err == nil {
		return false
	}
	// go-redis surfaces redis.error_reply('CONFLICT') as "ERR CONFLICT".
	msg := err.Error()
	return msg == "CONFLICT" || strings.HasSuffix(msg, " CONFLICT")
}

func (s *Store) maybeExpire(ctx context.Context, conversationID string) {
	if s.ttl <= 0 {
		return
	}
	vk, dk := s.verKey(conversationID), s.dataKey(conversationID)
	pipe := s.client.TxPipeline()
	pipe.Expire(ctx, vk, s.ttl)
	pipe.Expire(ctx, dk, s.ttl)
	_, _ = pipe.Exec(ctx)
}

var _ contexty.ConversationStateStore = (*Store)(nil)
