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
	if errors.Is(err, contexty.ErrConversationVersionConflict) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return wrapRedisUnavailable(op, err)
	}
	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		return wrapRedisUnavailable(op, err)
	}
	if _, ok := errors.AsType[*net.OpError](err); ok {
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
