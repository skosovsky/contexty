package redis

import (
	"os"
	"testing"

	"github.com/testcontainers/testcontainers-go"
)

func requireDocker(t *testing.T) {
	t.Helper()
	if os.Getenv("CI") == "true" {
		return
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)
}
