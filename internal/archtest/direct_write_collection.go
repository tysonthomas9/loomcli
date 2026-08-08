package archtest

import (
	"fmt"
	"go/ast"
	"go/types"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

type directWriteCountKey struct {
	file     string
	receiver string
	method   string
	owner    string
}

type persistenceAccess uint8

const (
	persistenceReadOnly persistenceAccess = iota + 1
	persistenceMutating
)

type directWriteCallIdentity struct {
	file   string
	line   int
	column int
}

type directWriteCall struct {
	directWriteCallIdentity
	receiver string
	method   string
	profiles map[string]struct{}
}

type directWriteProblemIdentity struct {
	file    string
	line    int
	column  int
	message string
}

type directWriteProblem struct {
	directWriteProblemIdentity
	profiles map[string]struct{}
}

func compareDirectWriteCallIdentity(left, right directWriteCallIdentity) int {
	leftKey := fmt.Sprintf("%s\x00%09d\x00%09d", left.file, left.line, left.column)
	rightKey := fmt.Sprintf("%s\x00%09d\x00%09d", right.file, right.line, right.column)
	return strings.Compare(leftKey, rightKey)
}

func compareDirectWriteProblemIdentity(left, right directWriteProblemIdentity) int {
	leftKey := fmt.Sprintf("%s\x00%09d\x00%09d\x00%s", left.file, left.line, left.column, left.message)
	rightKey := fmt.Sprintf("%s\x00%09d\x00%09d\x00%s", right.file, right.line, right.column, right.message)
	return strings.Compare(leftKey, rightKey)
}

func (problem directWriteProblem) withProfiles() string {
	profiles := make([]string, 0, len(problem.profiles))
	for profile := range problem.profiles {
		profiles = append(profiles, profile)
	}
	slices.Sort(profiles)
	return fmt.Sprintf("%s at %s:%d:%d (profiles: %s)", problem.message, problem.file, problem.line, problem.column, strings.Join(profiles, ", "))
}

func collectDirectWritePackage(
	root string,
	pkg *packages.Package,
	adapterRoots []string,
	classifier persistenceClassifier,
	calls map[directWriteCallIdentity]directWriteCall,
	problems map[directWriteProblemIdentity]directWriteProblem,
) error {
	if len(pkg.Errors) > 0 {
		return fmt.Errorf("load direct-write package %s: %s", pkg.PkgPath, pkg.Errors[0].Msg)
	}
	for _, file := range pkg.Syntax {
		if err := collectDirectWriteFile(root, pkg, file, adapterRoots, classifier, calls, problems); err != nil {
			return err
		}
	}
	return nil
}

func collectDirectWriteFile(
	root string,
	pkg *packages.Package,
	file *ast.File,
	adapterRoots []string,
	classifier persistenceClassifier,
	calls map[directWriteCallIdentity]directWriteCall,
	problems map[directWriteProblemIdentity]directWriteProblem,
) error {
	position := pkg.Fset.Position(file.Pos())
	rel, err := filepath.Rel(root, position.Filename)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	if !underAnyRoot(rel, adapterRoots) || strings.HasSuffix(rel, "_test.go") {
		return nil
	}
	collectUndeclaredPersistenceImports(pkg, file, rel, classifier, problems)
	collectDirectWriteSelectors(pkg, file, rel, classifier, calls)
	return nil
}

func collectUndeclaredPersistenceImports(
	pkg *packages.Package,
	file *ast.File,
	rel string,
	classifier persistenceClassifier,
	problems map[directWriteProblemIdentity]directWriteProblem,
) {
	for _, imported := range file.Imports {
		importPath, unquoteErr := strconv.Unquote(imported.Path.Value)
		if unquoteErr != nil {
			continue
		}
		importPosition := pkg.Fset.Position(imported.Pos())
		if imported.Name != nil && imported.Name.Name == "." && classifier.isPersistenceFunctionPackage(importPath) {
			identity := directWriteProblemIdentity{
				file: rel, line: importPosition.Line, column: importPosition.Column,
				message: fmt.Sprintf("dot import of persistence package %s bypasses qualified package-function analysis", importPath),
			}
			problems[identity] = directWriteProblem{directWriteProblemIdentity: identity}
			continue
		}
		if !classifier.undeclaredPersistenceImport(importPath) {
			continue
		}
		identity := directWriteProblemIdentity{
			file: rel, line: importPosition.Line, column: importPosition.Column,
			message: fmt.Sprintf("undeclared persistence package import %s", importPath),
		}
		problems[identity] = directWriteProblem{directWriteProblemIdentity: identity}
	}
}

func collectDirectWriteSelectors(
	pkg *packages.Package,
	file *ast.File,
	rel string,
	classifier persistenceClassifier,
	calls map[directWriteCallIdentity]directWriteCall,
) {
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		selection := pkg.TypesInfo.Selections[selector]
		if selection == nil {
			fn, isFunction := pkg.TypesInfo.Uses[selector.Sel].(*types.Func)
			if !isFunction || fn.Pkg() == nil || !classifier.isPersistenceFunctionPackage(fn.Pkg().Path()) {
				return true
			}
			signature, isSignature := fn.Type().(*types.Signature)
			if !isSignature || signature.Recv() != nil {
				return true
			}
			collectDirectWriteCall(pkg, selector, rel, fn.Pkg().Path(), fn.Name(), calls)
			return true
		}
		fn, ok := selection.Obj().(*types.Func)
		if !ok {
			return true
		}
		packagePath, receiverName, receiver, ok := persistenceMethodReceiver(fn)
		if !ok || !classifier.isPersistenceCandidate(packagePath, receiverName) {
			return true
		}
		collectDirectWriteCall(pkg, selector, rel, receiver, fn.Name(), calls)
		return true
	})
}

func collectDirectWriteCall(
	pkg *packages.Package,
	selector *ast.SelectorExpr,
	rel, receiver, method string,
	calls map[directWriteCallIdentity]directWriteCall,
) {
	callPosition := pkg.Fset.Position(selector.Sel.Pos())
	identity := directWriteCallIdentity{
		file: rel, line: callPosition.Line, column: callPosition.Column,
	}
	calls[identity] = directWriteCall{
		directWriteCallIdentity: identity,
		receiver:                receiver,
		method:                  method,
	}
}

func persistenceMethodReceiver(fn *types.Func) (string, string, string, bool) {
	if fn.Pkg() == nil {
		return "", "", "", false
	}
	signature, ok := fn.Type().(*types.Signature)
	if !ok || signature.Recv() == nil {
		return "", "", "", false
	}
	receiverType := signature.Recv().Type()
	named := receiverType
	if pointer, ok := named.(*types.Pointer); ok {
		named = pointer.Elem()
	}
	namedType, ok := named.(*types.Named)
	if !ok || namedType.Obj().Pkg() == nil {
		return "", "", "", false
	}
	receiver := types.TypeString(receiverType, func(p *types.Package) string { return p.Path() })
	return namedType.Obj().Pkg().Path(), namedType.Obj().Name(), receiver, true
}

func directWriteRows(counts map[directWriteCountKey]int, inventory DirectWriteInventory) []DirectWriteUse {
	result := make([]DirectWriteUse, 0, len(counts))
	for key, count := range counts {
		disposition := directWriteDispositionTransitional
		expiresAfterPhase := 7
		if key.file == "internal/driver" || strings.HasPrefix(key.file, "internal/driver/") {
			expiresAfterPhase = legacyDriverDirectWriteExpiresAfterPhase
		}
		if owner, ok := inventory.ownerAdapterOwner(key.file); ok && owner == key.owner {
			disposition = directWriteDispositionOwnerAdapter
			expiresAfterPhase = 0
		}
		result = append(result, DirectWriteUse{
			File: key.file, Receiver: key.receiver, Method: key.method, Count: count,
			AggregateOwner: key.owner, Disposition: disposition, ExpiresAfterPhase: expiresAfterPhase,
		})
	}
	slices.SortFunc(result, func(a, b DirectWriteUse) int { return strings.Compare(directWriteKey(a), directWriteKey(b)) })
	return result
}

func directWriteKey(use DirectWriteUse) string {
	return use.File + "\x00" + use.Receiver + "\x00" + use.Method
}

func underAnyRoot(path string, roots []string) bool {
	for _, root := range roots {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}
