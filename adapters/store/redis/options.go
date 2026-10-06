package redis

import (
	"time"

	"github.com/skosovsky/contexty"
)

// Option configures a Store.
type Option func(*Store)

// WithCodec configures a custom ConversationCodec.
func WithCodec(codec contexty.ConversationCodec) Option {
	return func(store *Store) {
		store.codec = codec
	}
}

// WithKeyPrefix supplies a logical namespace, encoded before Cluster key construction.
func WithKeyPrefix(prefix string) Option {
	return func(store *Store) {
		store.keyPrefix = prefix
	}
}

// WithTTL expires conversation payload after writes. OCC keys and empty
// tombstones do not expire; expiry advances the revision before the next read/CAS.
// Positive durations round upward to milliseconds; zero explicitly means persistent.
func WithTTL(ttl time.Duration) Option {
	if ttl < 0 {
		panic("contexty/redis: WithTTL called with negative duration")
	}
	return func(store *Store) {
		store.ttl = ttl
	}
}
