package redis

import (
	"context"
	"math"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

type ttlCaptureClient struct {
	goredis.UniversalClient

	args []any
}

func (c *ttlCaptureClient) Eval(ctx context.Context, script string, _ []string, args ...any) *goredis.Cmd {
	command := goredis.NewCmd(ctx)
	if script == luaLoad {
		command.SetVal([]any{"0", ""})
	} else {
		c.args = append([]any(nil), args...)
		command.SetVal(int64(1))
	}
	return command
}

func TestRemediation_TTLWireCeiling(t *testing.T) {
	for _, scenario := range []struct {
		ttl  time.Duration
		want int64
	}{{0, 0}, {time.Nanosecond, 1}, {999999 * time.Nanosecond, 1}, {time.Millisecond, 1}, {1500 * time.Microsecond, 2}, {time.Duration(math.MaxInt64), 9223372036855}} {
		// Arrange: real store mutation with only the transport replaced.
		client := &ttlCaptureClient{}
		store := New(client, WithTTL(scenario.ttl))
		// Act.
		err := store.CommitState(context.Background(), "ttl", 0, contexty.ConversationDelta{})
		// Assert: positive values never select no-expiry and rounding cannot overflow.
		require.NoError(t, err)
		require.Equal(t, scenario.want, client.args[3])
	}
	// Arrange / Act / Assert: invalid configuration is rejected before any client can run.
	require.Panics(t, func() { WithTTL(-time.Nanosecond) })
}
