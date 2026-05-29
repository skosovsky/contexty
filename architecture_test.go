package contexty

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchitecture_NoStringHeuristicsForSemantics(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	violations, err := findStringHeuristicViolations(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("string heuristics forbidden in semantic core:\n%s", strings.Join(violations, "\n"))
	}
}

func findStringHeuristicViolations(root string) ([]string, error) {
	fset := token.NewFileSet()
	var violations []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if shouldSkipArchitectureDir(filepath.Base(path)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isArchitectureSourceFile(path) || isArchitectureAllowlisted(path) {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		violations = append(violations, collectStringHeuristicCalls(fset, file)...)
		return nil
	})
	return violations, err
}

func shouldSkipArchitectureDir(name string) bool {
	switch name {
	case "adapters", "examples", "docs":
		return true
	default:
		return strings.HasPrefix(name, ".")
	}
}

func isArchitectureSourceFile(path string) bool {
	return strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
}

func isArchitectureAllowlisted(path string) bool {
	switch filepath.Base(path) {
	case "transform.go", "views.go", "model.go":
		return true
	default:
		return false
	}
}

func collectStringHeuristicCalls(fset *token.FileSet, file *ast.File) []string {
	var violations []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "strings" {
			return true
		}
		if !isBannedStringHeuristic(sel.Sel.Name) {
			return true
		}
		pos := fset.Position(call.Pos())
		violations = append(violations, pos.String())
		return true
	})
	return violations
}

func isBannedStringHeuristic(method string) bool {
	switch method {
	case "HasPrefix", "Contains", "HasSuffix":
		return true
	default:
		return false
	}
}
