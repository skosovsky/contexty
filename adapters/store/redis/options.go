package redis

import (
	"time"

	"github.com/skosovsky/contexty"
)

// Option configures a Store.
type Option func(*Store)

// WithSerializer configures a custom MessageSerializer.
// Fail-fast: panics if serializer is nil (programming error).
func WithSerializer(serializer contexty.MessageSerializer) Option {
	if serializer == nil {
		panic("contexty/redis: WithSerializer called with nil serializer")
	}
	return func(store *Store) {
		store.serializer = serializer
	}
}

// WithKeyPrefix configures the thread key prefix.
func WithKeyPrefix(prefix string) Option {
	return func(store *Store) {
		store.keyPrefix = prefix
	}
}

// WithTTL configures key expiration for Append and Save writes.
// Fail-fast: panics if ttl is negative.
func WithTTL(ttl time.Duration) Option {
	if ttl < 0 {
		panic("contexty/redis: WithTTL called with negative duration")
	}
	return func(store *Store) {
		store.ttl = ttl
	}
}
