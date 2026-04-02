package redis

import (
	"context"
	"errors"
	"fmt"
	"net"

	goredis "github.com/redis/go-redis/v9"

	"github.com/skosovsky/contexty"
)

// classifyRedisErr maps transient failures to errors wrapping [contexty.ErrUnavailable].
// It must not be used for a missing key (go-redis Nil) or serialization (marshal/unmarshal) errors.
func classifyRedisErr(op string, err error) error {
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
		return wrapRedisUnavailable(op, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return wrapRedisUnavailable(op, err)
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return wrapRedisUnavailable(op, err)
	}
	if errors.Is(err, goredis.ErrClosed) ||
		errors.Is(err, goredis.ErrPoolTimeout) ||
		errors.Is(err, goredis.ErrPoolExhausted) {
		return wrapRedisUnavailable(op, err)
	}
	return fmt.Errorf("contexty/redis: %s: %w", op, err)
}

func wrapRedisUnavailable(op string, cause error) error {
	return fmt.Errorf("contexty/redis: %s: %w", op, errors.Join(cause, contexty.ErrUnavailable))
}
