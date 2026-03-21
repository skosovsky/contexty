// Package redis provides a Redis-backed HistoryStore for contexty.
//
// Messages are stored as serialized list entries. TTL, if configured, applies to
// the Redis key only and does not affect context budgeting or eviction behavior.
package redis
