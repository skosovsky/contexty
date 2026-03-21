package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skosovsky/contexty"
)

const defaultTableName = "contexty_messages"

// Store persists thread history in PostgreSQL with optimistic concurrency via a
// per-thread version row in {messagesTable}_meta.
type Store struct {
	pool       *pgxpool.Pool
	serializer contexty.MessageSerializer
	tableName  string
	queries    renderedQueries
}

// New returns a PostgreSQL-backed Store.
func New(pool *pgxpool.Pool, opts ...Option) *Store {
	store := &Store{
		pool:       pool,
		serializer: contexty.DefaultJSONSerializer{},
		tableName:  defaultTableName,
	}
	for _, opt := range opts {
		opt(store)
	}
	store.queries = renderQueries(store.tableName)
	return store
}

// Load returns all stored messages for threadID ordered by insertion ID and the current version.
func (s *Store) Load(ctx context.Context, threadID string) (contexty.HistorySnapshot, error) {
	if s.pool == nil {
		return contexty.HistorySnapshot{}, fmt.Errorf("contexty/postgres: nil pool")
	}

	var version int64
	err := s.pool.QueryRow(ctx, s.queries.loadVersion, pgx.NamedArgs{"thread_id": threadID}).Scan(&version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			version = 0
		} else {
			return contexty.HistorySnapshot{}, fmt.Errorf("contexty/postgres: load version: %w", err)
		}
	}

	rows, err := s.pool.Query(ctx, s.queries.load, pgx.NamedArgs{"thread_id": threadID})
	if err != nil {
		return contexty.HistorySnapshot{}, fmt.Errorf("contexty/postgres: load query: %w", err)
	}
	defer rows.Close()

	var msgs []contexty.Message
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return contexty.HistorySnapshot{}, fmt.Errorf("contexty/postgres: load scan: %w", err)
		}

		var msg contexty.Message
		if err := s.serializer.Unmarshal(payload, &msg); err != nil {
			return contexty.HistorySnapshot{}, fmt.Errorf("contexty/postgres: load decode: %w", err)
		}
		msgs = append(msgs, msg)
	}
	if err := rows.Err(); err != nil {
		return contexty.HistorySnapshot{}, fmt.Errorf("contexty/postgres: load rows: %w", err)
	}
	if len(msgs) == 0 {
		return contexty.HistorySnapshot{Messages: []contexty.Message{}, Version: version}, nil
	}
	return contexty.HistorySnapshot{Messages: msgs, Version: version}, nil
}

// Append appends messages when expectedVersion matches the stored version.
func (s *Store) Append(ctx context.Context, threadID string, expectedVersion int64, msgs ...contexty.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	if s.pool == nil {
		return fmt.Errorf("contexty/postgres: nil pool")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("contexty/postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := bumpOrConflict(ctx, tx, s.queries, threadID, expectedVersion); err != nil {
		return err
	}
	if err := execAppend(ctx, s.serializer, s.queries.append, tx, threadID, msgs); err != nil {
		return fmt.Errorf("contexty/postgres: append: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("contexty/postgres: commit append: %w", err)
	}
	return nil
}

// Save replaces the full stored history when expectedVersion matches.
func (s *Store) Save(ctx context.Context, threadID string, expectedVersion int64, msgs []contexty.Message) error {
	if s.pool == nil {
		return fmt.Errorf("contexty/postgres: nil pool")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("contexty/postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := bumpOrConflict(ctx, tx, s.queries, threadID, expectedVersion); err != nil {
		return err
	}
	if err := execClear(ctx, s.queries.clear, tx, threadID); err != nil {
		return fmt.Errorf("contexty/postgres: clear before save: %w", err)
	}
	if len(msgs) > 0 {
		if err := execAppend(ctx, s.serializer, s.queries.append, tx, threadID, msgs); err != nil {
			return fmt.Errorf("contexty/postgres: append during save: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("contexty/postgres: commit save: %w", err)
	}
	return nil
}

// Clear removes all stored history when expectedVersion matches.
func (s *Store) Clear(ctx context.Context, threadID string, expectedVersion int64) error {
	if s.pool == nil {
		return fmt.Errorf("contexty/postgres: nil pool")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("contexty/postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := bumpOrConflict(ctx, tx, s.queries, threadID, expectedVersion); err != nil {
		return err
	}
	if err := execClear(ctx, s.queries.clear, tx, threadID); err != nil {
		return fmt.Errorf("contexty/postgres: clear: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("contexty/postgres: commit clear: %w", err)
	}
	return nil
}

func bumpOrConflict(ctx context.Context, tx pgx.Tx, q renderedQueries, threadID string, expectedVersion int64) error {
	if _, err := tx.Exec(ctx, q.ensureMeta, pgx.NamedArgs{"thread_id": threadID}); err != nil {
		return fmt.Errorf("contexty/postgres: ensure meta: %w", err)
	}
	ct, err := tx.Exec(ctx, q.bumpVersion, pgx.NamedArgs{
		"thread_id":        threadID,
		"expected_version": expectedVersion,
	})
	if err != nil {
		return fmt.Errorf("contexty/postgres: bump version: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return contexty.ErrHistoryVersionConflict
	}
	return nil
}

type queryExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func execAppend(ctx context.Context, serializer contexty.MessageSerializer, query string, exec queryExecutor, threadID string, msgs []contexty.Message) error {
	payloads := make([]string, len(msgs))
	for i, msg := range msgs {
		payload, err := serializer.Marshal(msg)
		if err != nil {
			return fmt.Errorf("marshal message %d: %w", i, err)
		}
		payloads[i] = string(payload)
	}
	if _, err := exec.Exec(ctx, query, pgx.NamedArgs{
		"thread_id": threadID,
		"payloads":  payloads,
	}); err != nil {
		return err
	}
	return nil
}

func execClear(ctx context.Context, query string, exec queryExecutor, threadID string) error {
	if _, err := exec.Exec(ctx, query, pgx.NamedArgs{"thread_id": threadID}); err != nil {
		return err
	}
	return nil
}

var _ contexty.HistoryStore = (*Store)(nil)
