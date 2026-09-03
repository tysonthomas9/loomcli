package realtime

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const realtimeImportPath = "github.com/tysonthomas9/loomcli/internal/webui/server/realtime"

type mutationSource struct {
	path            string
	file            *ast.File
	fileset         *token.FileSet
	realtimeAliases map[string]struct{}
}

func TestMutationPayloadContractGuard(t *testing.T) {
	repoRoot, err := mutationGuardRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	sources, err := parseMutationSources(filepath.Join(repoRoot, "internal"))
	if err != nil {
		t.Fatal(err)
	}
	constructors := contractConstructorNames(sources)
	var violations []string
	sites := 0
	for _, source := range sources {
		sites += mutationSourceSites(source)
		violations = append(violations, mutationSourceViolations(repoRoot, source, constructors)...)
	}
	if len(violations) != 0 {
		t.Fatalf("SSE mutation contract violations:\n%s", strings.Join(violations, "\n"))
	}
	// Without this the guard passes when the walk finds nothing at all, which
	// is exactly how it would fail silently if the payload type or the
	// broadcast API moved.
	if sites < mutationGuardMinimumSites {
		t.Fatalf("guard inspected %d mutation sites, want at least %d: finding nothing to check makes a green result meaningless (did MutationPayload or Broadcast move or get renamed?)", sites, mutationGuardMinimumSites)
	}
}

// mutationGuardMinimumSites is the number of production payload literals and
// Broadcast calls the walk sees today. Adding sites is fine; dropping below
// this means either a real removal, in which case lower it deliberately, or a
// walker that has stopped seeing the code it guards.
const mutationGuardMinimumSites = 23

// mutationSourceSites counts the sites the guard inspects, independent of
// whether any of them violate the contract.
func mutationSourceSites(source mutationSource) int {
	sites := 0
	ast.Inspect(source.file, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.CompositeLit:
			if isMutationPayloadType(typed.Type, source) {
				sites++
			}
		case *ast.CallExpr:
			if isBroadcastCall(typed) {
				sites++
			}
		}
		return true
	})
	return sites
}

func TestMutationPayloadContractGuardRejectsOmissions(t *testing.T) {
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, "bad_broadcast.go", `package realtime
func complete() *MutationPayload {
	return &MutationPayload{EntityType: "issue", Action: "issue.update"}
}
func broadcasts(h *Hub) {
	h.Broadcast(complete())
	h.Broadcast(&MutationPayload{Type: "update"})
}`, 0)
	if err != nil {
		t.Fatal(err)
	}
	source := mutationSource{file: file, fileset: fileset, realtimeAliases: map[string]struct{}{}}
	constructors := contractConstructorNames([]mutationSource{source})
	violations := mutationSourceViolations(".", source, constructors)
	if len(violations) != 2 {
		t.Fatalf("got violations %v, want literal and Broadcast violations for the incomplete payload", violations)
	}
	for _, violation := range violations {
		if !strings.HasPrefix(violation, "bad_broadcast.go:7:") {
			t.Errorf("violation %q does not name the incomplete site's file and line", violation)
		}
	}
}

func mutationGuardRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

func parseMutationSources(root string) ([]mutationSource, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && mutationGuardSkippedDir(entry.Name()) && path != root {
			return filepath.SkipDir
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk production Go source: %w", err)
	}
	sources := make([]mutationSource, 0, len(paths))
	for _, path := range paths {
		source, parseErr := parseMutationSource(path)
		if parseErr != nil {
			return nil, parseErr
		}
		sources = append(sources, source)
	}
	return sources, nil
}

func mutationGuardSkippedDir(name string) bool {
	switch name {
	case ".git", "node_modules", "third_party", "vendor", "worktrees":
		return true
	default:
		return false
	}
}

func parseMutationSource(path string) (mutationSource, error) {
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, path, nil, 0)
	if err != nil {
		return mutationSource{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return mutationSource{
		path: path, file: file, fileset: fileset, realtimeAliases: importedRealtimeAliases(file),
	}, nil
}

func importedRealtimeAliases(file *ast.File) map[string]struct{} {
	aliases := make(map[string]struct{})
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != realtimeImportPath {
			continue
		}
		name := "realtime"
		if spec.Name != nil {
			name = spec.Name.Name
		}
		aliases[name] = struct{}{}
	}
	return aliases
}

func contractConstructorNames(sources []mutationSource) map[string]struct{} {
	constructors := make(map[string]struct{})
	for _, source := range sources {
		for _, declaration := range source.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && returnsMutationPayload(function, source) && constructorHasContract(function, source) {
				constructors[function.Name.Name] = struct{}{}
			}
		}
	}
	return constructors
}

func returnsMutationPayload(function *ast.FuncDecl, source mutationSource) bool {
	if function.Type.Results == nil {
		return false
	}
	for _, field := range function.Type.Results.List {
		if isMutationPayloadType(unwrapPointer(field.Type), source) {
			return true
		}
	}
	return false
}

func constructorHasContract(function *ast.FuncDecl, source mutationSource) bool {
	foundReturn := false
	valid := true
	ast.Inspect(function.Body, func(node ast.Node) bool {
		statement, ok := node.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, result := range statement.Results {
			if identifier, nilResult := result.(*ast.Ident); nilResult && identifier.Name == "nil" {
				continue
			}
			foundReturn = true
			if !directContractExpression(result, source) {
				valid = false
			}
		}
		return valid
	})
	return foundReturn && valid
}

func mutationSourceViolations(repoRoot string, source mutationSource, constructors map[string]struct{}) []string {
	var violations []string
	ast.Inspect(source.file, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.CompositeLit:
			if isMutationPayloadType(typed.Type, source) && !literalHasMutationContract(typed) {
				violations = append(violations, mutationViolation(repoRoot, source, typed.Pos(),
					"MutationPayload literal must set both EntityType and Action"))
			}
		case *ast.CallExpr:
			if isBroadcastCall(typed) && !contractExpression(typed.Args[0], source, constructors) {
				violations = append(violations, mutationViolation(repoRoot, source, typed.Pos(),
					"Broadcast argument must set EntityType and Action directly or through a checked constructor"))
			}
		}
		return true
	})
	return violations
}

func isBroadcastCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "Broadcast" && len(call.Args) == 1
}

func contractExpression(expr ast.Expr, source mutationSource, constructors map[string]struct{}) bool {
	if directContractExpression(expr, source) {
		return true
	}
	call, ok := unwrapExpression(expr).(*ast.CallExpr)
	if !ok {
		return false
	}
	_, ok = constructors[calledFunctionName(call.Fun)]
	return ok
}

func directContractExpression(expr ast.Expr, source mutationSource) bool {
	literal, ok := unwrapExpression(expr).(*ast.CompositeLit)
	return ok && isMutationPayloadType(literal.Type, source) && literalHasMutationContract(literal)
}

func unwrapExpression(expr ast.Expr) ast.Expr {
	for {
		switch typed := expr.(type) {
		case *ast.ParenExpr:
			expr = typed.X
		case *ast.UnaryExpr:
			expr = typed.X
		default:
			return expr
		}
	}
}

func unwrapPointer(expr ast.Expr) ast.Expr {
	if pointer, ok := expr.(*ast.StarExpr); ok {
		return pointer.X
	}
	return expr
}

func isMutationPayloadType(expr ast.Expr, source mutationSource) bool {
	switch typed := expr.(type) {
	case *ast.Ident:
		_, dotImport := source.realtimeAliases["."]
		return (source.file.Name.Name == "realtime" || dotImport) && typed.Name == "MutationPayload"
	case *ast.SelectorExpr:
		identifier, ok := typed.X.(*ast.Ident)
		_, realtimeAlias := source.realtimeAliases[identifierName(identifier)]
		return ok && realtimeAlias && typed.Sel.Name == "MutationPayload"
	default:
		return false
	}
}

func identifierName(identifier *ast.Ident) string {
	if identifier == nil {
		return ""
	}
	return identifier.Name
}

func literalHasMutationContract(literal *ast.CompositeLit) bool {
	fields := make(map[string]bool, 2)
	for _, element := range literal.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if name, ok := pair.Key.(*ast.Ident); ok {
			fields[name.Name] = true
		}
	}
	return fields["EntityType"] && fields["Action"]
}

func calledFunctionName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		return typed.Sel.Name
	default:
		return ""
	}
}

func mutationViolation(repoRoot string, source mutationSource, position token.Pos, message string) string {
	location := source.fileset.Position(position)
	relative, err := filepath.Rel(repoRoot, location.Filename)
	if err == nil {
		location.Filename = relative
	}
	return fmt.Sprintf("%s: %s", location.String(), message)
}
