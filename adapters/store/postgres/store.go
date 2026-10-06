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

// ClearState checks only the locked OCC token, without decoding old payload or
// invoking host codecs, then retains a standard empty advancing tombstone.
func (s *Store) ClearState(ctx context.Context, conversationID string, expectedVersion int64) error {
	return s.mutate(ctx, conversationID, expectedVersion, nil)
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

	cur, insert, err := s.loadLocked(ctx, tx, conversationID, expectedVersion, update == nil)
	if err != nil {
		return err
	}
	nextVersion, encoded, err := s.prepareMutation(cur, update)
	if err != nil {
		return err
	}

	if err := s.persistSnapshot(
		ctx, tx, conversationID, expectedVersion, nextVersion, encoded, insert,
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return classifyPostgresErr("commit", err)
	}
	return nil
}

func (s *Store) loadLocked(
	ctx context.Context,
	tx pgx.Tx,
	conversationID string,
	expectedVersion int64,
	clearOnly bool,
) (contexty.ConversationSnapshot, bool, error) {
	query := fmt.Sprintf(`SELECT version, segments FROM %s WHERE thread_id = @thread_id FOR UPDATE`, s.tableName)
	var version int64
	var payload []byte
	var err error
	if clearOnly {
		query = fmt.Sprintf(`SELECT version FROM %s WHERE thread_id = @thread_id FOR UPDATE`, s.tableName)
		err = tx.QueryRow(ctx, query, pgx.NamedArgs{threadIDParam: conversationID}).Scan(&version)
	} else {
		err = tx.QueryRow(ctx, query, pgx.NamedArgs{threadIDParam: conversationID}).Scan(&version, &payload)
	}
	insert := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !insert {
		return contexty.ConversationSnapshot{}, false, classifyPostgresErr("load for update", err)
	}
	if version != expectedVersion {
		return contexty.ConversationSnapshot{}, false, contexty.ErrConversationVersionConflict
	}
	cur := contexty.EmptySnapshot().WithVersion(version)
	if !clearOnly && !insert {
		cur, err = s.codec.Decode(payload)
		if err != nil {
			return contexty.ConversationSnapshot{}, false, fmt.Errorf("contexty/postgres: decode: %w", err)
		}
		cur = cur.WithVersion(version)
	}
	return cur, insert, nil
}

func (s *Store) prepareMutation(
	cur contexty.ConversationSnapshot,
	update func(contexty.ConversationSnapshot) (contexty.ConversationSnapshot, error),
) (int64, []byte, error) {
	next := contexty.EmptySnapshot()
	codec := contexty.ConversationCodec{
		Provenance:    nil,
		Extensions:    nil,
		OpaqueProfile: contexty.Descriptor{ID: "", Revision: ""},
	}
	if update != nil {
		var err error
		next, err = update(cur)
		if err != nil {
			return 0, nil, err
		}
		next, err = contexty.ProjectCheckpoint(
			next,
			contexty.JSONSerializer{Provenance: s.codec.Provenance, Extensions: s.codec.Extensions},
			s.codec.OpaqueProfile,
		)
		if err != nil {
			return 0, nil, err
		}
		codec = s.codec
	}
	version, err := contexty.NextConversationVersion(cur.Version())
	if err != nil {
		return 0, nil, err
	}
	encoded, err := codec.Encode(next.WithVersion(version))
	if err != nil {
		return 0, nil, fmt.Errorf("contexty/postgres: encode: %w", err)
	}
	return version, encoded, nil
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
