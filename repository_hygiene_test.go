package contexty

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestArchitecture_NoDevelopmentMarkers(t *testing.T) {
	// Arrange: public code and documentation must describe contracts, not work items or audit roles.
	forbidden := regexp.MustCompile(`(?i)task[0-9]+|Audit` + `Completeness|Audit` + `Correctness`)
	// Act.
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != "." && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !isPublicRepositoryFile(path) {
			return nil
		}
		wire, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		// Assert.
		if forbidden.MatchString(path) || forbidden.Match(wire) {
			t.Errorf("development marker in %s", path)
		}
		if strings.HasSuffix(path, ".md") && strings.Contains(string(wire), ".cursor") {
			t.Errorf("public documentation references private work notes: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func isPublicRepositoryFile(path string) bool {
	return strings.HasSuffix(path, ".go") || path == "README.md" ||
		(strings.HasPrefix(path, "docs/") && strings.HasSuffix(path, ".md"))
}

func TestArchitecture_TestDoublesOutsideCore(t *testing.T) {
	// Arrange.
	exports := fixtureRootPublicNames(t)
	// Act.
	_, failing := exports["FailingEstimator"]
	fixed := exports["FixedEstimator"]
	// Assert.
	if failing || !fixed {
		t.Fatal("core must retain FixedEstimator without exporting the failing test double")
	}
}

func TestArchitecture_PublicDocumentationLinks(t *testing.T) {
	// Arrange: relative Markdown links must point to shipped files, not removed work-item documents.
	files, err := filepath.Glob("docs/*.md")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "README.md")
	link := regexp.MustCompile(`\]\(([^)]+)\)`)
	code := regexp.MustCompile("(?s)```.*?```|`[^`]*`")
	// Act.
	for _, path := range files {
		wire, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		prose := code.ReplaceAll(wire, nil)
		for _, match := range link.FindAllStringSubmatch(string(prose), -1) {
			target := strings.Trim(match[1], "<>")
			if strings.Contains(target, "://") || strings.HasPrefix(target, "#") {
				continue
			}
			target, _, _ = strings.Cut(target, "#")
			// Assert.
			if _, statErr := os.Stat(filepath.Join(filepath.Dir(path), target)); statErr != nil {
				t.Errorf("broken link in %s: %s: %v", path, target, statErr)
			}
		}
	}
}
