//go:build e2e

package chat

import (
	"os"
	"os/exec"
	"testing"
)

func TestE2ERecipe(t *testing.T) {
	// Arrange: execute the real offline recipe with the development core.
	cmd := exec.CommandContext(t.Context(), "go", "run", "./cmd/recipe")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	// Act.
	output, err := cmd.CombinedOutput()
	// Assert: every recipe validation and terminal commit succeeds.
	if err != nil {
		t.Fatalf("recipe failed: %v\n%s", err, output)
	}
}
