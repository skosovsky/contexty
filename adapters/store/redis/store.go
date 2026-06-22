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

// OCC keys never expire. An empty data value is a payload-free tombstone.
// Expired payload transitions to a new tombstone atomically before a CAS or read.
// Versions are compared as strings, avoiding Lua floating-point precision loss.
const luaState = `
local cur = redis.call('GET', KEYS[1]) or '0'
local data = redis.call('GET', KEYS[2])
if cur == '0' and data then return redis.error_reply('MISSING_REVISION') end
if cur ~= '0' and not data then
  if ARGV[1] ~= '1' then return redis.error_reply('MISSING_PAYLOAD') end
  if cur == '9223372036854775807' then return redis.error_reply('VERSION_EXHAUSTED') end
  redis.call('INCR', KEYS[1])
  cur = redis.call('GET', KEYS[1])
  redis.call('SET', KEYS[2], '')
  data = ''
end
`

const luaLoad = luaState + `
return {cur, data or ''}
`

const luaMutate = luaState + `
if cur ~= ARGV[2] then return redis.error_reply('CONFLICT') end
if cur == '9223372036854775807' then return redis.error_reply('VERSION_EXHAUSTED') end
redis.call('INCR', KEYS[1])
if tonumber(ARGV[4]) > 0 then
  redis.call('SET', KEYS[2], ARGV[3], 'PX', ARGV[4])
else
  redis.call('SET', KEYS[2], ARGV[3])
end
return 1
`

const luaClear = luaState + `
if cur ~= ARGV[2] then return redis.error_reply('CONFLICT') end
if cur == '9223372036854775807' then return redis.error_reply('VERSION_EXHAUSTED') end
redis.call('INCR', KEYS[1])
redis.call('SET', KEYS[2], '')
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
	values, err := s.client.Eval(ctx, luaLoad,
		[]string{s.verKey(conversationID), s.dataKey(conversationID)}, s.expiryMode()).Slice()
	if err != nil {
		return contexty.ConversationState{}, stateScriptError("load", err)
	}
	if len(values) != 2 {
		return contexty.ConversationState{}, contexty.ErrUnavailable
	}
	vstr, vok := values[0].(string)
	raw, rok := values[1].(string)
	if !vok || !rok {
		return contexty.ConversationState{}, contexty.ErrUnavailable
	}
	version, err := strconv.ParseInt(vstr, 10, 64)
	if err != nil || version < 0 {
		return contexty.ConversationState{}, fmt.Errorf("contexty/redis: invalid revision: %w", contexty.ErrUnavailable)
	}
	if raw == "" {
		return contexty.EmptySnapshot().WithVersion(version), nil
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
		s.expiryMode(),
		expectedVersion,
	); err != nil {
		return err
	}
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
	nextVersion, err := contexty.NextConversationVersion(expectedVersion)
	if err != nil {
		return err
	}
	next = next.WithVersion(nextVersion)
	encoded, err := s.codec.Encode(next)
	if err != nil {
		return fmt.Errorf("contexty/redis: encode: %w", err)
	}
	if err := s.evalConflict(
		ctx,
		luaMutate,
		[]string{s.verKey(conversationID), s.dataKey(conversationID)},
		s.expiryMode(),
		expectedVersion,
		string(encoded),
		s.ttl.Milliseconds(),
	); err != nil {
		return err
	}
	return nil
}

func (s *Store) evalConflict(ctx context.Context, script string, keys []string, args ...any) error {
	res, err := s.client.Eval(ctx, script, keys, args...).Result()
	if err != nil {
		if isRedisConflict(err) {
			return contexty.ErrConversationVersionConflict
		}
		return stateScriptError("eval", err)
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

func (s *Store) expiryMode() string {
	if s.ttl > 0 {
		return "1"
	}
	return "0"
}

func stateScriptError(op string, err error) error {
	if isRedisConflict(err) {
		return contexty.ErrConversationVersionConflict
	}
	if strings.HasSuffix(err.Error(), "VERSION_EXHAUSTED") {
		return contexty.ErrConversationVersionExhausted
	}
	if strings.HasSuffix(err.Error(), "MISSING_PAYLOAD") || strings.HasSuffix(err.Error(), "MISSING_REVISION") {
		return contexty.ErrUnavailable
	}
	return classifyRedisErr(op, err)
}

var _ contexty.ConversationStateStore = (*Store)(nil)
