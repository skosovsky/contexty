package contexty

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"regexp"
	"testing"
)

func TestMigration_APIReferences(t *testing.T) {
	// Arrange: documentation must name actual current public contracts.
	exports := fixtureRootPublicNames(t)
	wire, err := os.ReadFile("docs/migration.md")
	if err != nil {
		t.Fatal(err)
	}
	removed := map[string]bool{"WithEphemeralPatch": true, "MessageSelector": true, "PositionLast": true,
		"FailingEstimator": true}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`contexty\.([A-Z][A-Za-z0-9_]*)`),
		regexp.MustCompile(`\b(Err[A-Z][A-Za-z0-9_]*)\b`),
	}
	// Act / Assert: historical before examples are allowed, fabricated replacements/errors are not.
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringSubmatch(string(wire), -1) {
			if !exports[match[1]] && !removed[match[1]] {
				t.Errorf("migration references nonexistent public symbol %s", match[1])
			}
		}
	}
}

func fixtureRootPublicNames(t *testing.T) map[string]bool {
	t.Helper()
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, file := range files {
		if file.IsDir() || !isArchitectureSourceFile(file.Name()) {
			continue
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), file.Name(), nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch declaration := node.(type) {
			case *ast.TypeSpec:
				names[declaration.Name.Name] = ast.IsExported(declaration.Name.Name)
			case *ast.FuncDecl:
				if declaration.Recv == nil {
					names[declaration.Name.Name] = ast.IsExported(declaration.Name.Name)
				}
			case *ast.ValueSpec:
				for _, name := range declaration.Names {
					names[name.Name] = ast.IsExported(name.Name)
				}
			}
			return true
		})
	}
	return names
}

func TestArchitecture_PublicContractShapes(t *testing.T) {
	for _, contract := range []struct {
		owner    reflect.Type
		field    string
		expected reflect.Type
	}{
		{reflect.TypeFor[BudgetConfig](), "Budget", reflect.TypeFor[BudgetRequest]()},
		{reflect.TypeFor[CompileResult](), "Transformations", reflect.TypeFor[map[string]TransformChain]()},
		{reflect.TypeFor[CompileProjection](), "Transformations", reflect.TypeFor[map[string]TransformChain]()},
	} {
		// Arrange: inspect the actual runtime type independently of source filenames.
		field, found := contract.owner.FieldByName(contract.field)
		// Act / Assert: the public field keeps its typed contract.
		if !found || field.Type != contract.expected {
			t.Fatalf("%s.%s contract mismatch", contract.owner.Name(), contract.field)
		}
	}
}
