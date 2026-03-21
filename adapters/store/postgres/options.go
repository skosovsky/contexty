package postgres

import (
	"regexp"

	"github.com/skosovsky/contexty"
)

// Option configures a Store.
type Option func(*Store)

var tableNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`)

// WithSerializer configures a custom MessageSerializer.
// Fail-fast: panics if serializer is nil (programming error).
func WithSerializer(serializer contexty.MessageSerializer) Option {
	if serializer == nil {
		panic("contexty/postgres: WithSerializer called with nil serializer")
	}
	return func(store *Store) {
		store.serializer = serializer
	}
}

// WithTableName configures the SQL table name used by the store.
// Fail-fast: panics if table does not match the allowed pattern.
func WithTableName(table string) Option {
	if !tableNamePattern.MatchString(table) {
		panic("contexty/postgres: WithTableName called with invalid table name")
	}
	return func(store *Store) {
		store.tableName = table
	}
}
