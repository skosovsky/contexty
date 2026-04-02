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

const defaultKeyPrefix = "contexty:thread:"

// Lua: bump version if matches expected, then RPUSH payloads (ARGV[2..]).
const luaAppend = `
local expected = tonumber(ARGV[1])
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
if cur ~= expected then return redis.error_reply('CONFLICT') end
for i = 2, #ARGV do
	redis.call('RPUSH', KEYS[2], ARGV[i])
end
redis.call('INCR', KEYS[1])
return 1
`

// Lua: bump version if matches, replace list contents.
const luaSave = `
local expected = tonumber(ARGV[1])
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
if cur ~= expected then return redis.error_reply('CONFLICT') end
redis.call('DEL', KEYS[2])
for i = 2, #ARGV do
	redis.call('RPUSH', KEYS[2], ARGV[i])
end
redis.call('INCR', KEYS[1])
return 1
`

// Lua: bump version if matches, delete list.
const luaClear = `
local expected = tonumber(ARGV[1])
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
if cur ~= expected then return redis.error_reply('CONFLICT') end
redis.call('DEL', KEYS[2])
redis.call('INCR', KEYS[1])
return 1
`

// Store persists thread history in Redis lists with a separate version key per thread.
type Store struct {
	client     goredis.UniversalClient
	serializer contexty.MessageSerializer
	keyPrefix  string
	ttl        time.Duration
}

// New returns a Redis-backed Store.
func New(client goredis.UniversalClient, opts ...Option) *Store {
	store := &Store{
		client:     client,
		serializer: contexty.DefaultJSONSerializer{},
		keyPrefix:  defaultKeyPrefix,
	}
	for _, opt := range opts {
		opt(store)
	}
	return store
}

func (s *Store) verKey(threadID string) string {
	return s.keyPrefix + threadID + ":ver"
}

func (s *Store) listKey(threadID string) string {
	return s.keyPrefix + threadID
}

// Load returns all stored messages for threadID in append order and the current version.
func (s *Store) Load(ctx context.Context, threadID string) (contexty.HistorySnapshot, error) {
	if s.client == nil {
		return contexty.HistorySnapshot{}, errors.New("contexty/redis: nil client")
	}

	vstr, err := s.client.Get(ctx, s.verKey(threadID)).Result()
	var version int64
	switch {
	case err == nil:
		v, parseErr := strconv.ParseInt(vstr, 10, 64)
		if parseErr != nil {
			return contexty.HistorySnapshot{}, fmt.Errorf("contexty/redis: parse version: %w", parseErr)
		}
		version = v
	case errors.Is(err, goredis.Nil):
		version = 0
	default:
		return contexty.HistorySnapshot{}, classifyRedisErr("load version", err)
	}

	values, err := s.client.LRange(ctx, s.listKey(threadID), 0, -1).Result()
	if err != nil {
		return contexty.HistorySnapshot{}, classifyRedisErr("load range", err)
	}
	if len(values) == 0 {
		return contexty.HistorySnapshot{Messages: []contexty.Message{}, Version: version}, nil
	}

	msgs := make([]contexty.Message, len(values))
	for i, value := range values {
		if err := s.serializer.Unmarshal([]byte(value), &msgs[i]); err != nil {
			return contexty.HistorySnapshot{}, fmt.Errorf("contexty/redis: load decode: %w", err)
		}
	}
	return contexty.HistorySnapshot{Messages: msgs, Version: version}, nil
}

// Append appends messages when expectedVersion matches.
func (s *Store) Append(ctx context.Context, threadID string, expectedVersion int64, msgs ...contexty.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	if s.client == nil {
		return errors.New("contexty/redis: nil client")
	}

	payloads, err := s.serializeMessages(msgs)
	if err != nil {
		return fmt.Errorf("contexty/redis: append: %w", err)
	}

	args := make([]any, 0, 1+len(payloads))
	args = append(args, expectedVersion)
	for _, p := range payloads {
		args = append(args, p)
	}

	if err := s.evalConflict(ctx, luaAppend, []string{s.verKey(threadID), s.listKey(threadID)}, args...); err != nil {
		return err
	}
	s.maybeExpire(ctx, threadID)
	return nil
}

// Save replaces the full stored history when expectedVersion matches.
func (s *Store) Save(ctx context.Context, threadID string, expectedVersion int64, msgs []contexty.Message) error {
	if s.client == nil {
		return errors.New("contexty/redis: nil client")
	}

	var args []any
	args = append(args, expectedVersion)
	if len(msgs) > 0 {
		payloads, err := s.serializeMessages(msgs)
		if err != nil {
			return fmt.Errorf("contexty/redis: save: %w", err)
		}
		for _, p := range payloads {
			args = append(args, p)
		}
	}

	if err := s.evalConflict(ctx, luaSave, []string{s.verKey(threadID), s.listKey(threadID)}, args...); err != nil {
		return err
	}
	s.maybeExpire(ctx, threadID)
	return nil
}

// Clear removes all stored history when expectedVersion matches.
func (s *Store) Clear(ctx context.Context, threadID string, expectedVersion int64) error {
	if s.client == nil {
		return errors.New("contexty/redis: nil client")
	}
	if err := s.evalConflict(
		ctx,
		luaClear,
		[]string{s.verKey(threadID), s.listKey(threadID)},
		expectedVersion,
	); err != nil {
		return err
	}
	s.maybeExpire(ctx, threadID)
	return nil
}

func (s *Store) evalConflict(ctx context.Context, script string, keys []string, args ...any) error {
	res, err := s.client.Eval(ctx, script, keys, args...).Result()
	if err != nil {
		if isRedisConflict(err) {
			return contexty.ErrHistoryVersionConflict
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
	msg := err.Error()
	return strings.Contains(msg, "CONFLICT")
}

func (s *Store) maybeExpire(ctx context.Context, threadID string) {
	if s.ttl <= 0 {
		return
	}
	vk, lk := s.verKey(threadID), s.listKey(threadID)
	pipe := s.client.TxPipeline()
	pipe.Expire(ctx, vk, s.ttl)
	pipe.Expire(ctx, lk, s.ttl)
	_, _ = pipe.Exec(ctx)
}

func (s *Store) serializeMessages(msgs []contexty.Message) ([]string, error) {
	payloads := make([]string, len(msgs))
	for i, msg := range msgs {
		payload, err := s.serializer.Marshal(msg)
		if err != nil {
			return nil, fmt.Errorf("marshal message %d: %w", i, err)
		}
		payloads[i] = string(payload)
	}
	return payloads, nil
}

var _ contexty.HistoryStore = (*Store)(nil)
