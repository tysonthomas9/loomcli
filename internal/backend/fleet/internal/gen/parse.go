package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
)

func parseFleetActionContract(path string) ([]contractRow, error) {
	source, err := os.ReadFile(path) //nolint:gosec // The operator explicitly selects the FleetDB checkout.
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	constants := stringConstants(file)
	actions, err := validActionNames(file)
	if err != nil {
		return nil, err
	}
	entities, err := actionEntityNames(file)
	if err != nil {
		return nil, err
	}
	rows, err := buildRows(actions, entities, constants)
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Action < rows[j].Action })
	return rows, nil
}

func stringConstants(file *ast.File) map[string]string {
	constants := make(map[string]string)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			collectStringConstants(spec, constants)
		}
	}
	return constants
}

func collectStringConstants(spec ast.Spec, constants map[string]string) {
	valueSpec, ok := spec.(*ast.ValueSpec)
	if !ok {
		return
	}
	for i, name := range valueSpec.Names {
		if i >= len(valueSpec.Values) {
			continue
		}
		literal, ok := valueSpec.Values[i].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			continue
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil {
			constants[name.Name] = value
		}
	}
}

func validActionNames(file *ast.File) ([]string, error) {
	arrayName := validActionsArrayName(file)
	if arrayName == "" {
		return nil, fmt.Errorf("ValidActions must copy from a canonical action array")
	}
	literal := namedCompositeLiteral(file, arrayName)
	if literal == nil {
		return nil, fmt.Errorf("ValidActions canonical array %q not found", arrayName)
	}
	names := make([]string, 0, len(literal.Elts))
	for _, element := range literal.Elts {
		identifier, ok := element.(*ast.Ident)
		if !ok {
			return nil, fmt.Errorf("%s contains a non-identifier action", arrayName)
		}
		names = append(names, identifier.Name)
	}
	return names, nil
}

func validActionsArrayName(file *ast.File) string {
	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok || function.Name.Name != "ValidActions" {
			continue
		}
		var arrayName string
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if ok && isCopyCall(call) {
				arrayName = slicedIdentifier(call.Args[1])
			}
			return arrayName == ""
		})
		return arrayName
	}
	return ""
}

func isCopyCall(call *ast.CallExpr) bool {
	identifier, ok := call.Fun.(*ast.Ident)
	return ok && identifier.Name == "copy" && len(call.Args) == 2
}

func slicedIdentifier(expr ast.Expr) string {
	slice, ok := expr.(*ast.SliceExpr)
	if !ok {
		return ""
	}
	identifier, ok := slice.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return identifier.Name
}

func namedCompositeLiteral(file *ast.File, name string) *ast.CompositeLit {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			if literal := valueSpecComposite(spec, name); literal != nil {
				return literal
			}
		}
	}
	return nil
}

func valueSpecComposite(spec ast.Spec, name string) *ast.CompositeLit {
	valueSpec, ok := spec.(*ast.ValueSpec)
	if !ok {
		return nil
	}
	for i, identifier := range valueSpec.Names {
		if identifier.Name == name && i < len(valueSpec.Values) {
			literal, _ := valueSpec.Values[i].(*ast.CompositeLit)
			return literal
		}
	}
	return nil
}

func actionEntityNames(file *ast.File) (map[string]string, error) {
	literal := namedCompositeLiteral(file, "actionEntityMap")
	if literal == nil {
		return nil, fmt.Errorf("actionEntityMap not found")
	}
	entities := make(map[string]string, len(literal.Elts))
	for _, element := range literal.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return nil, fmt.Errorf("actionEntityMap contains a non-keyed element")
		}
		action, actionOK := pair.Key.(*ast.Ident)
		entity, entityOK := pair.Value.(*ast.Ident)
		if !actionOK || !entityOK {
			return nil, fmt.Errorf("actionEntityMap contains a non-identifier pair")
		}
		entities[action.Name] = entity.Name
	}
	return entities, nil
}

func buildRows(actions []string, entities, constants map[string]string) ([]contractRow, error) {
	rows := make([]contractRow, 0, len(actions))
	for _, actionName := range actions {
		action, ok := constants[actionName]
		if !ok {
			return nil, fmt.Errorf("action constant %q has no string value", actionName)
		}
		entityName, ok := entities[actionName]
		if !ok {
			return nil, fmt.Errorf("FleetDB action %q is missing from actionEntityMap", action)
		}
		entity, ok := constants[entityName]
		if !ok {
			return nil, fmt.Errorf("entity constant %q has no string value", entityName)
		}
		rows = append(rows, contractRow{
			Action: action, EntityType: entity, ExpectedCoarseType: expectedCoarseType(action, entity),
		})
	}
	if len(rows) != len(entities) {
		return nil, fmt.Errorf("actionEntityMap has %d entries but ValidActions has %d", len(entities), len(rows))
	}
	return rows, nil
}
