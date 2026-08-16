package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPhase5GenericDriverLifecycleRetirementRatchet closes the final MM-2
// compatibility lane on Loom's side. Aggregate output models retain
// ActiveVersionID, but generic create/update inputs and their FleetDB
// serializers may never carry it. Non-active status and aggregate trust remain
// deliberate generic administrative fields.
func TestPhase5GenericDriverLifecycleRetirementRatchet(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	storeSource := parseGenericDriverLifecycleFile(t, root, "internal/modules/workflowcatalog/record_store.go")
	for _, structName := range []string{"DriverCreate", "DriverUpdate"} {
		fields := namedStructFields(t, storeSource, structName)
		if fields["ActiveVersionID"] {
			t.Errorf("store.%s exposes lifecycle-owned ActiveVersionID", structName)
		}
		for _, retained := range []string{"Status", "TrustLevel", "Metadata"} {
			if !fields[retained] {
				t.Errorf("store.%s lost legitimate generic administrative field %s", structName, retained)
			}
		}
	}

	fleetSource := parseGenericDriverLifecycleFile(t, root, "internal/infra/fleetdb/platform.go")
	for _, functionName := range []string{"Create", "driverUpdateBody"} {
		body := namedFunctionBody(t, fleetSource, functionName)
		ast.Inspect(body, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if ok && literal.Kind == token.STRING && strings.Contains(literal.Value, "active_version_id") {
				t.Errorf("generic FleetDB serializer %s emits active_version_id", functionName)
			}
			return true
		})
	}

	for _, violation := range genericProductionActivationLiterals(t, root) {
		t.Error(violation)
	}
}

func parseGenericDriverLifecycleFile(t *testing.T, root, relative string) *ast.File {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	source, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", relative, err)
	}
	return source
}

func namedStructFields(t *testing.T, source *ast.File, name string) map[string]bool {
	t.Helper()
	for _, declaration := range source.Decls {
		generic, ok := declaration.(*ast.GenDecl)
		if !ok || generic.Tok != token.TYPE {
			continue
		}
		for _, specification := range generic.Specs {
			typed, ok := specification.(*ast.TypeSpec)
			if !ok || typed.Name.Name != name {
				continue
			}
			structure, ok := typed.Type.(*ast.StructType)
			if !ok {
				t.Fatalf("%s is not a struct", name)
			}
			fields := make(map[string]bool)
			for _, field := range structure.Fields.List {
				for _, fieldName := range field.Names {
					fields[fieldName.Name] = true
				}
			}
			return fields
		}
	}
	t.Fatalf("struct %s not found", name)
	return nil
}

func namedFunctionBody(t *testing.T, source *ast.File, name string) *ast.BlockStmt {
	t.Helper()
	for _, declaration := range source.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name && function.Body != nil {
			return function.Body
		}
	}
	t.Fatalf("function %s not found", name)
	return nil
}

func genericProductionActivationLiterals(t *testing.T, root string) []string {
	t.Helper()
	var violations []string
	internalRoot := filepath.Join(root, "internal")
	err := filepath.WalkDir(internalRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if strings.HasPrefix(relative, "internal/infra/") {
			return nil
		}
		source, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(source, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok || !isStoreDriverMutationType(literal.Type) {
				return true
			}
			for _, element := range literal.Elts {
				field, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := field.Key.(*ast.Ident)
				if !ok {
					continue
				}
				switch key.Name {
				case "ActiveVersionID":
					violations = append(violations, relative+": generic Driver mutation carries ActiveVersionID")
				case "Status":
					if expressionNamesDriverStatusActive(field.Value) {
						violations = append(violations, relative+": generic Driver mutation sets DriverStatusActive")
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return violations
}

func isStoreDriverMutationType(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || (selector.Sel.Name != "DriverCreate" && selector.Sel.Name != "DriverUpdate") {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && packageName.Name == "store"
}

func expressionNamesDriverStatusActive(expression ast.Expr) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "DriverStatusActive" {
			found = true
			return false
		}
		return !found
	})
	return found
}
