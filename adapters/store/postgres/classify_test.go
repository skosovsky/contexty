package postgres

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "synthetic timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return false }

func TestClassifyPostgresErr(t *testing.T) {
	t.Parallel()

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, classifyPostgresErr("op", nil))
	})

	t.Run("history conflict unchanged", func(t *testing.T) {
		t.Parallel()
		err := classifyPostgresErr("op", contexty.ErrHistoryVersionConflict)
		require.ErrorIs(t, err, contexty.ErrHistoryVersionConflict)
		assert.Equal(t, contexty.ErrHistoryVersionConflict, err)
	})

	t.Run("context canceled unchanged", func(t *testing.T) {
		t.Parallel()
		err := classifyPostgresErr("op", context.Canceled)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("deadline exceeded wraps unavailable", func(t *testing.T) {
		t.Parallel()
		cause := context.DeadlineExceeded
		err := classifyPostgresErr("load", cause)
		require.ErrorIs(t, err, contexty.ErrUnavailable)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("net timeout wraps unavailable", func(t *testing.T) {
		t.Parallel()
		cause := timeoutNetError{}
		err := classifyPostgresErr("query", cause)
		require.ErrorIs(t, err, contexty.ErrUnavailable)
	})

	t.Run("net op error without timeout wraps unavailable", func(t *testing.T) {
		t.Parallel()
		cause := &net.OpError{
			Op:  "read",
			Net: "tcp",
			Err: syscall.ECONNRESET,
		}
		err := classifyPostgresErr("query", cause)
		require.ErrorIs(t, err, contexty.ErrUnavailable)
		require.ErrorIs(t, err, syscall.ECONNRESET)
	})

	t.Run("plain error not unavailable", func(t *testing.T) {
		t.Parallel()
		cause := errors.New("unique constraint violation")
		err := classifyPostgresErr("append", cause)
		require.ErrorIs(t, err, cause)
		assert.NotErrorIs(t, err, contexty.ErrUnavailable)
	})
}
