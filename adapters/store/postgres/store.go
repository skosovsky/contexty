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

const threadIDParam = "thread_id"

// Store persists conversation state in PostgreSQL with optimistic concurrency.
type Store struct {
	pool      *pgxpool.Pool
	codec     contexty.ConversationCodec
	tableName string
}

// New returns a PostgreSQL-backed ConversationStateStore.
func New(pool *pgxpool.Pool, opts ...Option) *Store {
	store := &Store{
		pool: pool,
		codec: contexty.ConversationCodec{
			Provenance:    contexty.DefaultProvenanceRegistry(),
			Extensions:    nil,
			OpaqueProfile: contexty.Descriptor{ID: "", Revision: ""},
		},
		tableName: defaultTableName,
	}
	for _, opt := range opts {
		opt(store)
	}
	return store
}

// LoadState returns the full immutable conversation state.
func (s *Store) LoadState(ctx context.Context, conversationID string) (contexty.ConversationState, error) {
	if s.pool == nil {
		return contexty.ConversationState{}, errors.New("contexty/postgres: nil pool")
	}
	query := fmt.Sprintf(
		`SELECT version, segments FROM %s WHERE thread_id = @thread_id`,
		s.tableName,
	)
	var version int64
	var payload []byte
	err := s.pool.QueryRow(ctx, query, pgx.NamedArgs{threadIDParam: conversationID}).Scan(&version, &payload)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return contexty.EmptySnapshot(), nil
		}
		return contexty.ConversationState{}, classifyPostgresErr("load state", err)
	}
	snap, err := s.codec.Decode(payload)
	if err != nil {
		return contexty.ConversationState{}, fmt.Errorf("contexty/postgres: load state decode: %w", err)
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
	return s.mutate(
		ctx,
		conversationID,
		expectedVersion,
		func(snap contexty.ConversationSnapshot) (contexty.ConversationSnapshot, error) {
			return contexty.ApplyDeltas(snap, deltas...)
		},
	)
}

// ClearState removes payload and retains an advancing OCC tombstone.
func (s *Store) ClearState(ctx context.Context, conversationID string, expectedVersion int64) error {
	return s.mutate(ctx, conversationID, expectedVersion,
		func(contexty.ConversationSnapshot) (contexty.ConversationSnapshot, error) {
			return contexty.EmptySnapshot(), nil
		})
}

func (s *Store) mutate(
	ctx context.Context,
	conversationID string,
	expectedVersion int64,
	update func(contexty.ConversationSnapshot) (contexty.ConversationSnapshot, error),
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
	loadErr := tx.QueryRow(ctx, loadQ, pgx.NamedArgs{threadIDParam: conversationID}).Scan(&version, &payload)
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
		cur = contexty.EmptySnapshot()
	default:
		return classifyPostgresErr("load for update", loadErr)
	}
	if version != expectedVersion {
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
	nextVersion, err := contexty.NextConversationVersion(version)
	if err != nil {
		return err
	}
	next = next.WithVersion(nextVersion)
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
			threadIDParam: conversationID,
			"version":     nextVersion,
			"segments":    encoded,
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
		threadIDParam:      conversationID,
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

var _ contexty.ConversationStateStore = (*Store)(nil)
