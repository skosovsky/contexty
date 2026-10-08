//go:build integration

package redis

import (
	"context"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
)

func requireDocker(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	docker, err := client.New(client.FromEnv)
	require.NoError(t, err, "Docker prerequisite: configure a reachable daemon")
	t.Cleanup(func() { require.NoError(t, docker.Close()) })
	_, err = docker.Ping(ctx, client.PingOptions{})
	require.NoError(t, err, "Docker prerequisite: selected daemon must be reachable")
}
