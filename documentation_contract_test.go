package contexty

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
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
		file     string
		typeName string
		field    string
		target   string
		mapValue bool
	}{
		{file: "pipeline.go", typeName: "BudgetConfig", field: "Budget", target: "BudgetRequest", mapValue: false},
		{file: "transform_record.go", typeName: "CompileResult", field: "Transformations", target: "TransformChain", mapValue: true},
		{file: "compile_target.go", typeName: "CompileProjection", field: "Transformations", target: "TransformChain", mapValue: true},
	} {
		// Arrange: inspect declarations rather than relying on compatible assignment.
		parsed, err := parser.ParseFile(token.NewFileSet(), contract.file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		spec := findTypeSpec(parsed, contract.typeName)
		if spec == nil {
			t.Fatalf("missing %s", contract.typeName)
		}
		structure, ok := spec.Type.(*ast.StructType)
		if !ok {
			t.Fatalf("%s must be an explicit struct, not a compatibility alias", contract.typeName)
		}
		// Act / Assert.
		fixtureAssertStructFields(t, structure, contract.typeName, contract.field, contract.target, contract.mapValue)
	}
}

func fixtureAssertStructFields(
	t *testing.T,
	structure *ast.StructType,
	typeName, fieldName, target string,
	mapValue bool,
) {
	t.Helper()
	found := false
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			if typeName == "BudgetConfig" && name.Name == "TokenLimit" {
				t.Fatal("removed TokenLimit must not coexist with Budget")
			}
			if name.Name == fieldName {
				found = true
				fixtureAssertPublicField(t, field.Type, target, mapValue)
			}
		}
	}
	if !found {
		t.Fatalf("%s.%s is missing", typeName, fieldName)
	}
}

func fixtureAssertPublicField(t *testing.T, expression ast.Expr, target string, mapValue bool) {
	t.Helper()
	if mapValue {
		mapping, ok := expression.(*ast.MapType)
		if !ok || !exprIsIdent(mapping.Key, "string") {
			t.Fatal("transformations must be a string-keyed map")
		}
		expression = mapping.Value
	}
	if !exprIsIdent(expression, target) {
		t.Fatalf("public field must use %s, not an alias/legacy representation", target)
	}
}
