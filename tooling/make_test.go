package tooling_test

import (
	"path/filepath"
	"testing"
)

func TestMakeDiscoversModulesOutsideAdaptersAndExcludesHiddenVendor(t *testing.T) {
	// Arrange: nested modules alongside excluded directory trees.
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Makefile"), read(t, filepath.Join(repoRoot(t), "Makefile")))
	for _, module := range []string{".", "adapters/store", "integration/chat", ".peer/module", "vendor/module", "nested/.hidden/module", "nested/vendor/module"} {
		write(t, filepath.Join(dir, module, "go.mod"), []byte("module example.invalid/fixture\n\ngo 1.27.1\n"))
	}
	// Act.
	modules := command(t, dir, "make", "--no-print-directory", "-s", "modules")
	// Assert: one shared dynamic inventory, without directory-specific exclusions.
	if modules != ".\nadapters/store\nintegration/chat" {
		t.Fatalf("unexpected inventory: %s", modules)
	}
}
