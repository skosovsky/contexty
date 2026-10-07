package contexty

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestArchitecture_NoStringHeuristicsForSemantics(t *testing.T) {
	// Arrange.
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	violations, err := findStringHeuristicViolations(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// Act / Assert: exercise the contract and check its result.
	if len(violations) > 0 {
		t.Fatalf("string heuristics forbidden in semantic core:\n%s", strings.Join(violations, "\n"))
	}
}

func TestArchitecture_DeferredResultContract(t *testing.T) {
	// Arrange: inspect the public type independently of its source filename.
	field, found := reflect.TypeFor[DeferredBlock]().FieldByName("Resolve")
	// Act / Assert: enforce the typed callback signature.
	want := reflect.TypeFor[func(context.Context) (DeferredResult, error)]()
	if !found || field.Type != want {
		t.Fatal("Resolve must return DeferredResult and error")
	}
}

func TestArchitecture_NoForbiddenExternalImports(t *testing.T) {
	// Arrange.
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	violations, err := findForbiddenImportViolations(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// Act / Assert: exercise the contract and check its result.
	if len(violations) > 0 {
		t.Fatalf("forbidden external imports in core:\n%s", strings.Join(violations, "\n"))
	}
}

func TestArchitecture_NoJSONMetadataInTextParts(t *testing.T) {
	// Arrange.
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	violations, err := findJSONMetadataInTextViolations(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// Act / Assert: exercise the contract and check its result.
	if len(violations) > 0 {
		t.Fatalf(
			"json metadata tunneling via TextContent forbidden in semantic core:\n%s",
			strings.Join(violations, "\n"),
		)
	}
}

func TestArchitecture_NoContractMetadataInAttributes(t *testing.T) {
	// Arrange.
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	violations, err := findAttributesEscapeHatchViolations(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// Act / Assert: exercise the contract and check its result.
	if len(violations) > 0 {
		t.Fatalf(
			"contract metadata must use first-class fields, SourceRefs, or Extensions, not Attributes:\n%s",
			strings.Join(violations, "\n"),
		)
	}
}

func TestArchitecture_NoBase64InCore(t *testing.T) {
	// Arrange.
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	violations, err := findBase64Violations(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// Act / Assert: exercise the contract and check its result.
	if len(violations) > 0 {
		t.Fatalf("base64 encoding forbidden in semantic core:\n%s", strings.Join(violations, "\n"))
	}
}

func TestArchitecture_FormattersUseExplicitContext(t *testing.T) {
	// Arrange.
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	violations, err := findFormatterContextViolations(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// Act / Assert: exercise the contract and check its result.
	if len(violations) > 0 {
		t.Fatalf(
			"formatters must receive request context explicitly without package-level context globals:\n%s",
			strings.Join(violations, "\n"),
		)
	}
}

func findFormatterContextViolations(root string) ([]string, error) {
	var violations []string
	if !reflect.TypeFor[SegmentFormatter]().ConvertibleTo(reflect.TypeFor[func(context.Context, []Message) ([]Message, error)]()) {
		violations = append(violations, "SegmentFormatter signature mismatch")
	}
	field, found := reflect.TypeFor[ViewConfiguration]().FieldByName("Formatter")
	if !found || field.Type != reflect.TypeFor[SegmentFormatter]() {
		violations = append(violations, "ViewConfiguration.Formatter signature mismatch")
	}
	globalViolations, err := findPackageContextGlobalViolations(root)
	if err != nil {
		return nil, err
	}
	violations = append(violations, globalViolations...)
	return violations, nil
}

func findPackageContextGlobalViolations(root string) ([]string, error) {
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
		if !isArchitectureSourceFile(path) {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		violations = append(violations, collectPackageContextGlobals(fset, file)...)
		return nil
	})
	return violations, err
}

func collectPackageContextGlobals(fset *token.FileSet, file *ast.File) []string {
	var violations []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			values, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if exprContainsContextContext(values.Type) {
				pos := fset.Position(values.Pos())
				violations = append(violations, pos.String()+": package-level context.Context variable")
				continue
			}
			for _, value := range values.Values {
				if exprContainsContextConstructor(value) {
					pos := fset.Position(value.Pos())
					violations = append(violations, pos.String()+": package-level context constructor")
				}
			}
		}
	}
	return violations
}

func exprContainsContextConstructor(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if ok && pkg.Name == "context" && (sel.Sel.Name == "Background" || sel.Sel.Name == "TODO") {
			found = true
			return false
		}
		return true
	})
	return found
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
	case "adapters", "examples", "docs", "integration":
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
	case "transform.go", "views.go", "model.go", "provenance.go", "serializer.go",
		"content_part.go", "message_origin.go":
		return true
	default:
		return false
	}
}

func findAttributesEscapeHatchViolations(root string) ([]string, error) {
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
		if !isArchitectureSourceFile(path) {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		content := string(data)
		for _, needle := range []string{"type Attributes", "Attributes map", ".Attributes", " Attributes `"} {
			if strings.Contains(content, needle) {
				violations = append(violations, path+": contains "+needle)
			}
		}
		return nil
	})
	return violations, err
}

func findJSONMetadataInTextViolations(root string) ([]string, error) {
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
		violations = append(violations, collectJSONMetadataInTextViolations(fset, file)...)
		return nil
	})
	return violations, err
}

func collectJSONMetadataInTextViolations(fset *token.FileSet, file *ast.File) []string {
	var violations []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		if !isJSONUnmarshalCall(call) {
			return true
		}
		if !exprReferencesTextContent(call.Args[0]) {
			return true
		}
		pos := fset.Position(call.Pos())
		violations = append(violations, pos.String())
		return true
	})
	return violations
}

func isJSONUnmarshalCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "json" && sel.Sel.Name == "Unmarshal"
}

func exprReferencesTextContent(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == "TextContent" {
			found = true
			return false
		}
		return true
	})
	return found
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

var forbiddenImportSubstrings = []string{ //nolint:gochecknoglobals // test allowlist constant
	"github.com/skosovsky/kosmify",
	"github.com/skosovsky/metry",
	"github.com/skosovsky/langfuse",
	"go.opentelemetry.io",
	"kosmify",
	"langfuse",
	"metry",
}

func findForbiddenImportViolations(root string) ([]string, error) {
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
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		isTest := strings.HasSuffix(path, "_test.go")
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, imp := range file.Imports {
			pathVal := strings.Trim(imp.Path.Value, `"`)
			if isForbiddenImportPath(pathVal, isTest) {
				pos := fset.Position(imp.Path.Pos())
				violations = append(violations, pos.String()+": "+pathVal)
			}
		}
		return nil
	})
	return violations, err
}

func isForbiddenImportPath(path string, isTest bool) bool {
	for _, banned := range forbiddenImportSubstrings {
		if strings.Contains(path, banned) {
			return true
		}
	}
	if isTest {
		switch path {
		case "github.com/stretchr/testify/assert", "github.com/stretchr/testify/require":
			return false
		}
	}
	if strings.Contains(path, ".") && !isAllowedContextyImport(path) {
		if isTest && strings.HasPrefix(path, "github.com/stretchr/testify") {
			return false
		}
		return !isStdLibImport(path)
	}
	return false
}

func isAllowedContextyImport(path string) bool {
	return path == "github.com/skosovsky/contexty" ||
		strings.HasPrefix(path, "github.com/skosovsky/contexty/")
}

func isStdLibImport(path string) bool {
	return !strings.Contains(path, ".")
}

func findBase64Violations(root string) ([]string, error) {
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
		if !isArchitectureSourceFile(path) {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		violations = append(violations, collectBase64Violations(fset, file)...)
		return nil
	})
	return violations, err
}

func collectBase64Violations(fset *token.FileSet, file *ast.File) []string {
	var violations []string
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, `"`) == "encoding/base64" {
			pos := fset.Position(imp.Path.Pos())
			violations = append(violations, pos.String())
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "base64" {
			return true
		}
		pos := fset.Position(sel.Pos())
		violations = append(violations, pos.String())
		return true
	})
	return violations
}

func exprContainsContextContext(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if ok && pkg.Name == "context" && sel.Sel.Name == "Context" {
			found = true
			return false
		}
		return true
	})
	return found
}
