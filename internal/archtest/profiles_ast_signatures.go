package archtest

import (
	"go/ast"
	"strings"
)

// exportedSignatureExpressions returns only type/value expressions that are
// observable through the package API. Function bodies, method receivers,
// unexported receiver methods, and unexported struct/interface fields are
// implementation details and must not turn an allowed private adapter field
// into a public-signature violation.
func exportedSignatureExpressions(decl ast.Decl) []ast.Expr {
	switch value := decl.(type) {
	case *ast.FuncDecl:
		if !exportedFunctionDeclaration(value) {
			return nil
		}
		return appendFieldListExpressions(nil, value.Type.TypeParams, value.Type.Params, value.Type.Results)
	case *ast.GenDecl:
		expressions := []ast.Expr{}
		for _, spec := range value.Specs {
			switch typed := spec.(type) {
			case *ast.TypeSpec:
				if typed.Name.IsExported() {
					expressions = append(expressions, publicTypeExpressions(typed.Type)...)
				}
			case *ast.ValueSpec:
				expressions = append(expressions, exportedValueExpressions(typed)...)
			}
		}
		return expressions
	default:
		return nil
	}
}

func appendFieldListExpressions(expressions []ast.Expr, lists ...*ast.FieldList) []ast.Expr {
	for _, list := range lists {
		if list == nil {
			continue
		}
		for _, field := range list.List {
			expressions = append(expressions, field.Type)
		}
	}
	return expressions
}

func exportedValueExpressions(spec *ast.ValueSpec) []ast.Expr {
	if spec == nil {
		return nil
	}
	exported := make([]int, 0, len(spec.Names))
	for index, name := range spec.Names {
		if name.IsExported() {
			exported = append(exported, index)
		}
	}
	if len(exported) == 0 {
		return nil
	}
	expressions := []ast.Expr{}
	if spec.Type != nil {
		expressions = append(expressions, spec.Type)
	}
	if len(spec.Values) != len(spec.Names) {
		return append(expressions, spec.Values...)
	}
	for _, index := range exported {
		expressions = append(expressions, spec.Values[index])
	}
	return expressions
}

// publicTypeExpressions mirrors the typed exported-signature check for files
// outside the active build profile. Private struct fields and private explicit
// interface methods are not package API. Embedded interface types remain part
// of the public type identity and are therefore checked.
func publicTypeExpressions(expression ast.Expr) []ast.Expr {
	switch typed := expression.(type) {
	case *ast.StructType:
		return publicStructTypeExpressions(typed)
	case *ast.InterfaceType:
		return publicInterfaceTypeExpressions(typed)
	default:
		return []ast.Expr{expression}
	}
}

func publicStructTypeExpressions(typed *ast.StructType) []ast.Expr {
	expressions := []ast.Expr{}
	if typed.Fields == nil {
		return expressions
	}
	for _, field := range typed.Fields.List {
		if len(field.Names) == 0 {
			if embeddedFieldExported(field.Type) {
				expressions = append(expressions, field.Type)
			}
			continue
		}
		if hasExportedFieldName(field.Names) {
			expressions = append(expressions, field.Type)
		}
	}
	return expressions
}

func publicInterfaceTypeExpressions(typed *ast.InterfaceType) []ast.Expr {
	expressions := []ast.Expr{}
	if typed.Methods == nil {
		return expressions
	}
	for _, field := range typed.Methods.List {
		if len(field.Names) == 0 || hasExportedFieldName(field.Names) {
			expressions = append(expressions, field.Type)
		}
	}
	return expressions
}

func hasExportedFieldName(names []*ast.Ident) bool {
	for _, name := range names {
		if name.IsExported() {
			return true
		}
	}
	return false
}

func embeddedFieldExported(expression ast.Expr) bool {
	switch typed := expression.(type) {
	case *ast.Ident:
		return typed.IsExported()
	case *ast.SelectorExpr:
		return typed.Sel.IsExported()
	case *ast.StarExpr:
		return embeddedFieldExported(typed.X)
	case *ast.IndexExpr:
		return embeddedFieldExported(typed.X)
	case *ast.IndexListExpr:
		return embeddedFieldExported(typed.X)
	case *ast.ParenExpr:
		return embeddedFieldExported(typed.X)
	default:
		return false
	}
}

func firstLeakedASTTypeImport(expression ast.Expr, aliases, localLeaks map[string]string, graph CapabilityGraph) string {
	leaked := ""
	ast.Inspect(expression, func(node ast.Node) bool {
		if leaked != "" {
			return false
		}
		switch typed := node.(type) {
		case *ast.SelectorExpr:
			if ident, ok := typed.X.(*ast.Ident); ok {
				if importPath := aliases[ident.Name]; isLeakedSignatureImport(importPath, graph) {
					leaked = importPath
					return false
				}
			}
		case *ast.Ident:
			if importPath := localLeaks[typed.Name]; importPath != "" {
				leaked = importPath
				return false
			}
		}
		return true
	})
	return leaked
}

func exportedDeclaration(decl ast.Decl) bool {
	switch value := decl.(type) {
	case *ast.FuncDecl:
		return exportedFunctionDeclaration(value)
	case *ast.GenDecl:
		for _, spec := range value.Specs {
			switch typed := spec.(type) {
			case *ast.TypeSpec:
				if typed.Name.IsExported() {
					return true
				}
			case *ast.ValueSpec:
				for _, name := range typed.Names {
					if name.IsExported() {
						return true
					}
				}
			}
		}
	}
	return false
}

func exportedFunctionDeclaration(function *ast.FuncDecl) bool {
	if function == nil || !function.Name.IsExported() {
		return false
	}
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return true
	}
	return embeddedFieldExported(function.Recv.List[0].Type)
}

func isLeakedSignatureImport(importPath string, graph CapabilityGraph) bool {
	if isForbiddenModuleDependency(importPath) {
		return true
	}
	if capabilityForImport(importPath, graph) != "" {
		return !isCapabilityPublicRoot(importPath, graph)
	}
	return strings.Contains(importPath, "/adapter/") || strings.HasSuffix(importPath, "/adapter")
}
