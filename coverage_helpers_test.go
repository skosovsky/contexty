package contexty_test

import (
	"testing"

	"github.com/skosovsky/contexty"
)

func fixtureCoverage(
	t *testing.T,
	manifest contexty.CompileManifest,
	name, segment, id string,
) contexty.ManifestCoverage {
	t.Helper()
	for _, entry := range manifest.Coverage {
		if entry.OutputName == name && entry.Segment == segment && entry.Input != nil && entry.Input.ID == id {
			return entry
		}
	}
	t.Fatalf("missing coverage for %s/%s/%s", name, segment, id)
	return contexty.ManifestCoverage{}
}
