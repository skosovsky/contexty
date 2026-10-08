//go:build integration

package tooling_test

import (
	"path/filepath"
	"testing"
)

func TestIntegrationPublishedBaseline(t *testing.T) {
	// Arrange: isolated published modules, with no development replacement.
	dir := t.TempDir()
	write(
		t,
		filepath.Join(dir, "go.mod"),
		[]byte(
			"module example.invalid/baseline\n\ngo 1.27.1\n\nrequire (\n github.com/skosovsky/contexty v0.12.0\n github.com/skosovsky/prompty v0.15.0\n)\n",
		),
	)
	write(
		t,
		filepath.Join(dir, "baseline_test.go"),
		read(t, filepath.Join(repoRoot(t), "integration/baseline_test.go.txt")),
	)
	// Act: resolve the older supported subset independently of current-source tests.
	command(t, dir, "go", "mod", "tidy")
	command(t, dir, "go", "mod", "verify")
	// Assert: the original semantic assertions pass against published dependencies.
	command(t, dir, "go", "test", "-race", "-count=1", "./...")
	command(t, dir, "go", "vet", "./...")
}
