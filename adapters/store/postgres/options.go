package postgres

import (
	"regexp"

	"github.com/skosovsky/contexty"
)

// Option configures a Store.
type Option func(*Store)

var tableNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`)

// WithCodec configures a custom ConversationCodec.
func WithCodec(codec contexty.ConversationCodec) Option {
	return func(store *Store) {
		store.codec = codec
	}
}

// WithTableName configures the SQL table name used by the store.
func WithTableName(table string) Option {
	if !tableNamePattern.MatchString(table) {
		panic("contexty/postgres: WithTableName called with invalid table name")
	}
	return func(store *Store) {
		store.tableName = table
	}
}
