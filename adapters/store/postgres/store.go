package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skosovsky/contexty"
)

const defaultTableName = "contexty_conversations"

// Store persists conversation segments in PostgreSQL with optimistic concurrency.
type Store struct {
	pool      *pgxpool.Pool
	codec     contexty.ConversationCodec
	tableName string
}

// New returns a PostgreSQL-backed ConversationStore.
func New(pool *pgxpool.Pool, opts ...Option) *Store {
	store := &Store{
		pool:      pool,
		codec:     contexty.ConversationCodec{Provenance: contexty.DefaultProvenanceRegistry()},
		tableName: defaultTableName,
	}
	for _, opt := range opts {
		opt(store)
	}
	return store
}

// Load returns the stored conversation snapshot.
func (s *Store) Load(ctx context.Context, conversationID string) (contexty.ConversationSnapshot, error) {
	if s.pool == nil {
		return contexty.ConversationSnapshot{}, errors.New("contexty/postgres: nil pool")
	}
	query := fmt.Sprintf(
		`SELECT version, segments FROM %s WHERE thread_id = @thread_id`,
		s.tableName,
	)
	var version int64
	var payload []byte
	err := s.pool.QueryRow(ctx, query, pgx.NamedArgs{"thread_id": conversationID}).Scan(&version, &payload)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return contexty.EmptySnapshot(), nil
		}
		return contexty.ConversationSnapshot{}, classifyPostgresErr("load", err)
	}
	snap, err := s.codec.Decode(payload)
	if err != nil {
		return contexty.ConversationSnapshot{}, fmt.Errorf("contexty/postgres: load decode: %w", err)
	}
	return snap.WithVersion(version), nil
}

// UpdateSegment replaces a segment when expectedVersion matches.
func (s *Store) UpdateSegment(
	ctx context.Context,
	conversationID string,
	expectedVersion int64,
	name contexty.SegmentName,
	msgs []contexty.Message,
) error {
	return s.mutate(
		ctx,
		conversationID,
		expectedVersion,
		func(snap contexty.ConversationSnapshot) contexty.ConversationSnapshot {
			return snap.WithSegment(name, msgs)
		},
	)
}

// AppendSegment appends messages to a segment.
func (s *Store) AppendSegment(
	ctx context.Context,
	conversationID string,
	expectedVersion int64,
	name contexty.SegmentName,
	msgs ...contexty.Message,
) error {
	if len(msgs) == 0 {
		return nil
	}
	return s.mutate(
		ctx,
		conversationID,
		expectedVersion,
		func(snap contexty.ConversationSnapshot) contexty.ConversationSnapshot {
			existing := snap.Segment(name)
			combined := make([]contexty.Message, len(existing)+len(msgs))
			copy(combined, existing)
			for i, m := range msgs {
				combined[len(existing)+i] = m
			}
			return snap.WithSegment(name, combined)
		},
	)
}

// Clear removes the conversation row.
func (s *Store) Clear(ctx context.Context, conversationID string, expectedVersion int64) error {
	if s.pool == nil {
		return errors.New("contexty/postgres: nil pool")
	}
	query := fmt.Sprintf(
		`DELETE FROM %s WHERE thread_id = @thread_id AND version = @expected_version`,
		s.tableName,
	)
	ct, err := s.pool.Exec(ctx, query, pgx.NamedArgs{
		"thread_id":        conversationID,
		"expected_version": expectedVersion,
	})
	if err != nil {
		return classifyPostgresErr("clear", err)
	}
	if ct.RowsAffected() == 0 {
		// Distinguish missing thread (expected 0) from conflict.
		var exists bool
		check := fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s WHERE thread_id = @thread_id)`, s.tableName)
		if qerr := s.pool.QueryRow(ctx, check, pgx.NamedArgs{"thread_id": conversationID}).Scan(&exists); qerr != nil {
			return classifyPostgresErr("clear check", qerr)
		}
		if !exists && expectedVersion == 0 {
			return nil
		}
		return contexty.ErrConversationVersionConflict
	}
	return nil
}

func (s *Store) mutate(
	ctx context.Context,
	conversationID string,
	expectedVersion int64,
	update func(contexty.ConversationSnapshot) contexty.ConversationSnapshot,
) error {
	if s.pool == nil {
		return errors.New("contexty/postgres: nil pool")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return classifyPostgresErr("begin tx", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var cur contexty.ConversationSnapshot
	var version int64
	loadQ := fmt.Sprintf(
		`SELECT version, segments FROM %s WHERE thread_id = @thread_id FOR UPDATE`,
		s.tableName,
	)
	var payload []byte
	loadErr := tx.QueryRow(ctx, loadQ, pgx.NamedArgs{"thread_id": conversationID}).Scan(&version, &payload)
	switch {
	case loadErr == nil:
		cur, err = s.codec.Decode(payload)
		if err != nil {
			return fmt.Errorf("contexty/postgres: decode: %w", err)
		}
		cur = cur.WithVersion(version)
	case errors.Is(loadErr, pgx.ErrNoRows):
		if expectedVersion != 0 {
			return contexty.ErrConversationVersionConflict
		}
		cur = contexty.ConversationSnapshot{}
	default:
		return classifyPostgresErr("load for update", loadErr)
	}
	if version != expectedVersion {
		return contexty.ErrConversationVersionConflict
	}

	next := update(cur)
	next = next.WithVersion(version + 1)
	encoded, err := s.codec.Encode(next)
	if err != nil {
		return fmt.Errorf("contexty/postgres: encode: %w", err)
	}

	if err := s.persistSnapshot(
		ctx, tx, conversationID, expectedVersion, next.Version(), encoded, loadErr != nil,
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return classifyPostgresErr("commit", err)
	}
	return nil
}

func (s *Store) persistSnapshot(
	ctx context.Context,
	tx pgx.Tx,
	conversationID string,
	expectedVersion int64,
	nextVersion int64,
	encoded []byte,
	insert bool,
) error {
	if insert {
		insertQ := fmt.Sprintf(
			`INSERT INTO %s (thread_id, version, segments) VALUES (@thread_id, @version, @segments)`,
			s.tableName,
		)
		if _, err := tx.Exec(ctx, insertQ, pgx.NamedArgs{
			"thread_id": conversationID,
			"version":   nextVersion,
			"segments":  encoded,
		}); err != nil {
			if isUniqueViolation(err) {
				return contexty.ErrConversationVersionConflict
			}
			return classifyPostgresErr("insert", err)
		}
		return nil
	}
	updateQ := fmt.Sprintf(
		`UPDATE %s SET version = @version, segments = @segments
		 WHERE thread_id = @thread_id AND version = @expected_version`,
		s.tableName,
	)
	ct, err := tx.Exec(ctx, updateQ, pgx.NamedArgs{
		"thread_id":        conversationID,
		"version":          nextVersion,
		"segments":         encoded,
		"expected_version": expectedVersion,
	})
	if err != nil {
		return classifyPostgresErr("update", err)
	}
	if ct.RowsAffected() == 0 {
		return contexty.ErrConversationVersionConflict
	}
	return nil
}

var _ contexty.ConversationStore = (*Store)(nil)
