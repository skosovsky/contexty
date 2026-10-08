//go:build integration

package redis

import (
	"context"
	"net"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/skosovsky/contexty/testutil"
)

// Three actual Redis nodes share a container network; the host dialer maps their
// advertised ports through Docker Desktop. All key/slot routing stays real.
func TestIntegrationStoreCluster(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{

		Image:        "redis:7-alpine",
		ExposedPorts: []string{"7000/tcp", "7001/tcp", "7002/tcp"},
		Cmd: []string{"sh", "-c", `
for port in 7000 7001 7002; do
  mkdir -p /tmp/node-$port
  redis-server --port $port --bind 0.0.0.0 --protected-mode no --cluster-enabled yes --cluster-config-file /tmp/node-$port/nodes.conf --appendonly no --daemonize yes
done
until redis-cli -p 7002 ping >/dev/null 2>&1; do sleep 0.1; done
redis-cli --cluster create 127.0.0.1:7000 127.0.0.1:7001 127.0.0.1:7002 --cluster-replicas 0 --cluster-yes
until redis-cli -p 7000 cluster info | grep -q cluster_state:ok; do sleep 0.1; done
echo cluster-ready
tail -f /dev/null`},
		WaitingFor: wait.ForAll(
			wait.ForLog("cluster-ready"),
			wait.ForMappedPort("7000/tcp"),
			wait.ForMappedPort("7001/tcp"),
			wait.ForMappedPort("7002/tcp"),
		).WithDeadline(time.Minute),
		Started: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, testcontainers.TerminateContainer(container)) })
	host, err := container.Host(ctx)
	require.NoError(t, err)
	addresses := make(map[string]string)
	var seeds []string
	for _, port := range []string{"7000", "7001", "7002"} {
		mapped, mapErr := container.MappedPort(ctx, port+"/tcp")
		require.NoError(t, mapErr)
		addresses[port] = net.JoinHostPort(host, mapped.Port())
		seeds = append(seeds, addresses[port])
	}
	client := goredis.NewClusterClient(&goredis.ClusterOptions{Addrs: seeds,
		Dialer: func(ctx context.Context, network, address string) (net.Conn, error) {
			_, port, splitErr := net.SplitHostPort(address)
			if splitErr != nil {
				return nil, splitErr
			}
			if mapped, ok := addresses[port]; ok {
				address = mapped
			}
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	store := New(client, WithKeyPrefix("host:{untrusted-prefix}"))
	slots := make(map[int64]bool)
	keys := make(map[string]bool)
	for _, id := range []string{"ordinary", "{ordinary}", "用户:Δ", "a:b", "a}{b", ""} {
		t.Run(id, func(t *testing.T) {
			// Arrange: hostile identifiers still generate two keys in one slot.
			versionSlot, slotErr := client.ClusterKeySlot(ctx, store.verKey(id)).Result()
			require.NoError(t, slotErr)
			payloadSlot, slotErr := client.ClusterKeySlot(ctx, store.dataKey(id)).Result()
			require.NoError(t, slotErr)
			// Act / Assert: conformance performs real multi-key Lua, CAS and atomic batches.
			require.Equal(t, versionSlot, payloadSlot)
			require.False(t, keys[store.verKey(id)])
			keys[store.verKey(id)] = true
			slots[versionSlot] = true
			testutil.CheckStateStore(t, store, id)
		})
	}
	require.Greater(t, len(slots), 1, "conversations must not be forced into one slot")
}
