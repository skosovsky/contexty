package redis

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/skosovsky/contexty"
)

const defaultKeyPrefix = "default"

// OCC keys never expire. MSET publishes revision and payload in one command.
// All write permissions are checked before any write, including expiry cleanup.
// Decimal carry preserves every int64 revision without Lua floating-point loss.
const luaPrelude = `
local function nextRevision(value)
  if value == '9223372036854775807' then error('VERSION_EXHAUSTED') end
  local suffix = ''
  for i = #value, 1, -1 do
    local digit = tonumber(string.sub(value, i, i))
    if digit < 9 then return string.sub(value, 1, i - 1) .. tostring(digit + 1) .. suffix end
    suffix = '0' .. suffix
  end
  return '1' .. suffix
end
local function checkWrites(payload, ttl)
  if not redis.acl_check_cmd('MSET', KEYS[1], '0', KEYS[2], payload) then
    error('CHECKPOINT_WRITE_DENIED')
  end
  if ttl > 0 and not redis.acl_check_cmd('PEXPIRE', KEYS[2], tostring(ttl)) then
    error('CHECKPOINT_WRITE_DENIED')
  end
end
`

const luaState = `
local cur = redis.call('GET', KEYS[1]) or '0'
local data = redis.call('GET', KEYS[2])
if cur == '0' and data then return redis.error_reply('MISSING_REVISION') end
if cur ~= '0' and not data then
  if ARGV[1] ~= '1' then return redis.error_reply('MISSING_PAYLOAD') end
  if cur == '9223372036854775807' then return redis.error_reply('VERSION_EXHAUSTED') end
  checkWrites('', 0)
  cur = nextRevision(cur)
  redis.call('MSET', KEYS[1], cur, KEYS[2], '')
  data = ''
end
`

const luaLoad = luaPrelude + luaState + `
return {cur, data or ''}
`

const luaMutate = luaPrelude + `
local ttl = tonumber(ARGV[4])
checkWrites(ARGV[3], ttl)
` + luaState + `
if cur ~= ARGV[2] then return redis.error_reply('CONFLICT') end
if cur == '9223372036854775807' then return redis.error_reply('VERSION_EXHAUSTED') end
redis.call('MSET', KEYS[1], nextRevision(cur), KEYS[2], ARGV[3])
if ttl > 0 then redis.call('PEXPIRE', KEYS[2], ARGV[4]) end
return 1
`

const luaClear = luaPrelude + `
checkWrites('', 0)
` + luaState + `
if cur ~= ARGV[2] then return redis.error_reply('CONFLICT') end
if cur == '9223372036854775807' then return redis.error_reply('VERSION_EXHAUSTED') end
redis.call('MSET', KEYS[1], nextRevision(cur), KEYS[2], '')
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
		client: client,
		codec: contexty.ConversationCodec{
			Provenance:    contexty.DefaultProvenanceRegistry(),
			Extensions:    nil,
			OpaqueProfile: contexty.Descriptor{ID: "", Revision: ""},
		},
		keyPrefix: defaultKeyPrefix,
		ttl:       0,
	}
	for _, opt := range opts {
		opt(store)
	}
	return store
}

func (s *Store) verKey(conversationID string) string {
	return s.conversationKey(conversationID) + ":ver"
}

func (s *Store) dataKey(conversationID string) string {
	return s.conversationKey(conversationID) + ":data"
}

func (s *Store) conversationKey(id string) string {
	return "contexty:checkpoint:" + hex.EncodeToString(
		[]byte(s.keyPrefix),
	) + ":{c" + hex.EncodeToString(
		[]byte(id),
	) + "}"
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

// CommitState atomically applies a nonempty batch when expectedVersion matches.
func (s *Store) CommitState(
	ctx context.Context,
	conversationID string,
	expectedVersion int64,
	deltas ...contexty.ConversationDelta,
) error {
	if len(deltas) == 0 {
		return contexty.ErrEmptyCheckpointCommit
	}
	return s.mutate(ctx, conversationID, expectedVersion,
		func(snap contexty.ConversationSnapshot) (contexty.ConversationSnapshot, error) {
			return contexty.ApplyDeltas(snap, deltas...)
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
	next, err = contexty.ProjectCheckpoint(
		next,
		contexty.JSONSerializer{Provenance: s.codec.Provenance, Extensions: s.codec.Extensions},
		s.codec.OpaqueProfile,
	)
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
		ttlMilliseconds(s.ttl),
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

// Duration's int64 nanosecond range leaves ample room for millisecond ceiling.
func ttlMilliseconds(ttl time.Duration) int64 {
	milliseconds := int64(ttl / time.Millisecond)
	if ttl%time.Millisecond > 0 {
		milliseconds++
	}
	return milliseconds
}
