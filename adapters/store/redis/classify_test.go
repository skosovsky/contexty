package redis

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "synthetic timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return false }

func TestClassifyRedisErr(t *testing.T) {
	t.Parallel()

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, classifyRedisErr("op", nil))
	})

	t.Run("history conflict unchanged", func(t *testing.T) {
		t.Parallel()
		err := classifyRedisErr("op", contexty.ErrHistoryVersionConflict)
		require.ErrorIs(t, err, contexty.ErrHistoryVersionConflict)
		assert.Equal(t, contexty.ErrHistoryVersionConflict, err)
	})

	t.Run("context canceled unchanged", func(t *testing.T) {
		t.Parallel()
		err := classifyRedisErr("op", context.Canceled)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("deadline exceeded wraps unavailable", func(t *testing.T) {
		t.Parallel()
		cause := context.DeadlineExceeded
		err := classifyRedisErr("load", cause)
		require.ErrorIs(t, err, contexty.ErrUnavailable)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("net timeout wraps unavailable", func(t *testing.T) {
		t.Parallel()
		cause := timeoutNetError{}
		err := classifyRedisErr("get", cause)
		require.ErrorIs(t, err, contexty.ErrUnavailable)
	})

	t.Run("net op error without timeout wraps unavailable", func(t *testing.T) {
		t.Parallel()
		cause := &net.OpError{
			Op:  "read",
			Net: "tcp",
			Err: syscall.ECONNREFUSED,
		}
		err := classifyRedisErr("eval", cause)
		require.ErrorIs(t, err, contexty.ErrUnavailable)
		require.ErrorIs(t, err, syscall.ECONNREFUSED)
	})

	t.Run("err closed wraps unavailable", func(t *testing.T) {
		t.Parallel()
		err := classifyRedisErr("get", goredis.ErrClosed)
		require.ErrorIs(t, err, contexty.ErrUnavailable)
		require.ErrorIs(t, err, goredis.ErrClosed)
	})

	t.Run("err pool timeout wraps unavailable", func(t *testing.T) {
		t.Parallel()
		err := classifyRedisErr("get", goredis.ErrPoolTimeout)
		require.ErrorIs(t, err, contexty.ErrUnavailable)
		require.ErrorIs(t, err, goredis.ErrPoolTimeout)
	})

	t.Run("plain error not unavailable", func(t *testing.T) {
		t.Parallel()
		cause := errors.New("WRONGTYPE")
		err := classifyRedisErr("get", cause)
		require.ErrorIs(t, err, cause)
		assert.NotErrorIs(t, err, contexty.ErrUnavailable)
	})
}
