// Package redis provides a Redis-backed ConversationStateStore for contexty.
// Redis 7+ is required for ACL preflight. The caller must permit EVAL, GET, MSET
// and, for TTL writes, PEXPIRE; active revision keys must not expire or be evicted.
package redis
