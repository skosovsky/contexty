package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/skosovsky/contexty"
)

// classifyPostgresErr maps transient failures to errors wrapping [contexty.ErrUnavailable].
// It must not be used for [pgx.ErrNoRows] or serialization (marshal/unmarshal) errors.
func classifyPostgresErr(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, contexty.ErrHistoryVersionConflict) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return wrapPostgresUnavailable(op, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return wrapPostgresUnavailable(op, err)
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return wrapPostgresUnavailable(op, err)
	}
	return fmt.Errorf("contexty/postgres: %s: %w", op, err)
}

func wrapPostgresUnavailable(op string, cause error) error {
	return fmt.Errorf("contexty/postgres: %s: %w", op, errors.Join(cause, contexty.ErrUnavailable))
}
