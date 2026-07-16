package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/tools/go/packages"
)

// analyzeProfiles loads the complete package graph for each supported source
// selection. go/packages is intentional here: direct AST scans cannot expose
// transitive forbidden paths or profile-specific import cycles.
func analyzeProfiles(root string, matrix AnalysisMatrix, graph CapabilityGraph, genericMechanisms []GenericMechanismUse) ([]string, error) {
	profiles := append(append([]AnalysisProfile{}, matrix.Release...), matrix.Tagged...)
	type result struct {
		violations []string
		err        error
	}
	results := make([]result, len(profiles))
	semaphore := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for i, profile := range profiles {
		i, profile := i, profile
		wg.Add(1)
		go func() {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			results[i].violations, results[i].err = analyzeProfile(root, profile, graph, genericMechanisms)
		}()
	}
	wg.Wait()
	violations := []string{}
	for i, result := range results {
		if result.err != nil {
			return nil, fmt.Errorf("analyze profile %s: %w", profiles[i].Name, result.err)
		}
		violations = append(violations, result.violations...)
	}
	return violations, nil
}

func analyzeProfile(root string, profile AnalysisProfile, graph CapabilityGraph, genericMechanisms []GenericMechanismUse) ([]string, error) {
	tags := append([]string(nil), profile.Tags...)
	if profile.Race {
		// The matrix calls for race source selection, not execution. The implicit
		// race build tag selects the same files without requiring cross-CGO builds.
		tags = append(tags, "race")
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedImports | packages.NeedDeps | packages.NeedModule,
		Dir:   root,
		Env:   profileEnvironment(profile),
		Tests: true,
	}
	if len(tags) > 0 {
		cfg.BuildFlags = []string{"-tags=" + strings.Join(tags, ",")}
	}
	loaded, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, err
	}
	violations := []string{}
	for _, pkg := range loaded {
		for _, pkgErr := range pkg.Errors {
			violations = append(violations, fmt.Sprintf("profile %s package %s: %s", profile.Name, pkg.PkgPath, pkgErr.Msg))
		}
		violations = append(violations, profilePackageViolations(root, profile.Name, pkg, graph, genericMechanisms)...)
	}
	violations = append(violations, requiredSourceViolations(root, profile, loaded)...)
	return violations, nil
}

func requiredSourceViolations(root string, profile AnalysisProfile, loaded []*packages.Package) []string {
	if len(profile.RequiredFiles) == 0 {
		return nil
	}
	selected := make(map[string]struct{})
	for _, pkg := range loaded {
		for _, path := range pkg.CompiledGoFiles {
			rel, err := filepath.Rel(root, path)
			if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			selected[filepath.ToSlash(rel)] = struct{}{}
		}
	}
	violations := []string{}
	for _, required := range profile.RequiredFiles {
		if _, ok := selected[required]; !ok {
			violations = append(violations, fmt.Sprintf("profile %s did not select required source %s", profile.Name, required))
		}
	}
	return violations
}

func profilePackageViolations(root, profile string, pkg *packages.Package, graph CapabilityGraph, genericMechanisms []GenericMechanismUse) []string {
	if !strings.HasPrefix(pkg.PkgPath, modulePath+"/internal/") {
		return nil
	}
	// packages.Load(Tests: true) adds a synthetic test-main package whose path
	// ends in ".test". Its imports are the test harness, not a production or
	// test source boundary. The real package-under-test variants are loaded
	// separately and remain fully checked, including their _test.go imports.
	if strings.HasSuffix(pkg.PkgPath, ".test") {
		return nil
	}
	violations := []string{}
	for importPath := range pkg.Imports {
		if reason := forbiddenBoundaryImport(pkg.PkgPath, importPath, graph); reason != "" {
			violations = append(violations, fmt.Sprintf("profile %s package %s imports %s: %s", profile, pkg.PkgPath, importPath, reason))
		}
	}
	violations = append(violations, typedGenericMechanismViolations(root, profile, pkg, genericMechanisms)...)
	packageBoundary := classifyBoundaryPackage(pkg.PkgPath, graph)
	if isCapabilityPackage(packageBoundary.kind) {
		violations = append(violations, typedExportedSignatureViolations(profile, pkg, graph)...)
	}
	if packageBoundary.kind == boundaryPackageCapabilityPublic || packageBoundary.kind == boundaryPackageCapabilityCore {
		violations = append(violations, transitiveModuleViolations(profile, pkg)...)
	}
	return violations
}

func typedExportedSignatureViolations(profile string, pkg *packages.Package, graph CapabilityGraph) []string {
	if pkg.Types == nil {
		return nil
	}
	names := pkg.Types.Scope().Names()
	slices.Sort(names)
	violations := []string{}
	for _, name := range names {
		if !token.IsExported(name) {
			continue
		}
		object := pkg.Types.Scope().Lookup(name)
		if object == nil {
			continue
		}
		if leaked := firstLeakedTypePackage(object.Type(), pkg.PkgPath, graph, map[types.Type]struct{}{}); leaked != "" {
			violations = append(violations, fmt.Sprintf(
				"profile %s package %s exported %s leaks forbidden type from %s",
				profile, pkg.PkgPath, name, leaked,
			))
		}
	}
	return violations
}

func firstLeakedTypePackage(value types.Type, currentPackage string, graph CapabilityGraph, seen map[types.Type]struct{}) string {
	if value == nil {
		return ""
	}
	if _, ok := seen[value]; ok {
		return ""
	}
	seen[value] = struct{}{}

	switch typed := value.(type) {
	case *types.Alias:
		return firstLeakedTypePackage(types.Unalias(typed), currentPackage, graph, seen)
	case *types.Named:
		return firstLeakedNamedPackage(typed, currentPackage, graph, seen)
	case *types.Pointer:
		return firstLeakedTypePackage(typed.Elem(), currentPackage, graph, seen)
	case *types.Array:
		return firstLeakedTypePackage(typed.Elem(), currentPackage, graph, seen)
	case *types.Slice:
		return firstLeakedTypePackage(typed.Elem(), currentPackage, graph, seen)
	case *types.Map:
		if leaked := firstLeakedTypePackage(typed.Key(), currentPackage, graph, seen); leaked != "" {
			return leaked
		}
		return firstLeakedTypePackage(typed.Elem(), currentPackage, graph, seen)
	case *types.Chan:
		return firstLeakedTypePackage(typed.Elem(), currentPackage, graph, seen)
	case *types.Signature:
		return firstLeakedSignaturePackage(typed, currentPackage, graph, seen)
	case *types.Struct:
		return firstLeakedStructPackage(typed, currentPackage, graph, seen)
	case *types.Interface:
		return firstLeakedInterfacePackage(typed, currentPackage, graph, seen)
	case *types.Tuple:
		return firstLeakedTuplePackage(typed, currentPackage, graph, seen)
	case *types.TypeParam:
		return firstLeakedTypePackage(typed.Constraint(), currentPackage, graph, seen)
	case *types.Union:
		return firstLeakedUnionPackage(typed, currentPackage, graph, seen)
	}
	return ""
}

func firstLeakedNamedPackage(named *types.Named, currentPackage string, graph CapabilityGraph, seen map[types.Type]struct{}) string {
	if object := named.Obj(); object != nil && object.Pkg() != nil && object.Pkg().Path() != currentPackage {
		path := object.Pkg().Path()
		if isLeakedSignatureImport(path, graph) {
			return path
		}
		// An allowed generic wrapper can still expose a forbidden argument.
		// Its underlying representation belongs to the external package and is
		// not itself part of this package's exported signature.
		return firstLeakedTypeArguments(named.TypeArgs(), currentPackage, graph, seen)
	}
	if leaked := firstLeakedTypeArguments(named.TypeArgs(), currentPackage, graph, seen); leaked != "" {
		return leaked
	}
	if leaked := firstLeakedTypePackage(named.Underlying(), currentPackage, graph, seen); leaked != "" {
		return leaked
	}
	for index := 0; index < named.NumMethods(); index++ {
		method := named.Method(index)
		if method.Exported() {
			if leaked := firstLeakedTypePackage(method.Type(), currentPackage, graph, seen); leaked != "" {
				return leaked
			}
		}
	}
	return ""
}

func firstLeakedTypeArguments(arguments *types.TypeList, currentPackage string, graph CapabilityGraph, seen map[types.Type]struct{}) string {
	if arguments == nil {
		return ""
	}
	for index := 0; index < arguments.Len(); index++ {
		if leaked := firstLeakedTypePackage(arguments.At(index), currentPackage, graph, seen); leaked != "" {
			return leaked
		}
	}
	return ""
}

func firstLeakedSignaturePackage(signature *types.Signature, currentPackage string, graph CapabilityGraph, seen map[types.Type]struct{}) string {
	if leaked := firstLeakedTuplePackage(signature.Params(), currentPackage, graph, seen); leaked != "" {
		return leaked
	}
	if leaked := firstLeakedTuplePackage(signature.Results(), currentPackage, graph, seen); leaked != "" {
		return leaked
	}
	return firstLeakedTypeParameters(signature.TypeParams(), currentPackage, graph, seen)
}

func firstLeakedStructPackage(structure *types.Struct, currentPackage string, graph CapabilityGraph, seen map[types.Type]struct{}) string {
	for index := 0; index < structure.NumFields(); index++ {
		field := structure.Field(index)
		if field.Exported() {
			if leaked := firstLeakedTypePackage(field.Type(), currentPackage, graph, seen); leaked != "" {
				return leaked
			}
		}
	}
	return ""
}

func firstLeakedInterfacePackage(iface *types.Interface, currentPackage string, graph CapabilityGraph, seen map[types.Type]struct{}) string {
	iface.Complete()
	for index := 0; index < iface.NumExplicitMethods(); index++ {
		method := iface.ExplicitMethod(index)
		if method.Exported() {
			if leaked := firstLeakedTypePackage(method.Type(), currentPackage, graph, seen); leaked != "" {
				return leaked
			}
		}
	}
	for index := 0; index < iface.NumEmbeddeds(); index++ {
		if leaked := firstLeakedTypePackage(iface.EmbeddedType(index), currentPackage, graph, seen); leaked != "" {
			return leaked
		}
	}
	return ""
}

func firstLeakedUnionPackage(union *types.Union, currentPackage string, graph CapabilityGraph, seen map[types.Type]struct{}) string {
	for index := 0; index < union.Len(); index++ {
		if leaked := firstLeakedTypePackage(union.Term(index).Type(), currentPackage, graph, seen); leaked != "" {
			return leaked
		}
	}
	return ""
}

func firstLeakedTuplePackage(tuple *types.Tuple, currentPackage string, graph CapabilityGraph, seen map[types.Type]struct{}) string {
	if tuple == nil {
		return ""
	}
	for index := 0; index < tuple.Len(); index++ {
		if leaked := firstLeakedTypePackage(tuple.At(index).Type(), currentPackage, graph, seen); leaked != "" {
			return leaked
		}
	}
	return ""
}

func firstLeakedTypeParameters(parameters *types.TypeParamList, currentPackage string, graph CapabilityGraph, seen map[types.Type]struct{}) string {
	if parameters == nil {
		return ""
	}
	for index := 0; index < parameters.Len(); index++ {
		if leaked := firstLeakedTypePackage(parameters.At(index).Constraint(), currentPackage, graph, seen); leaked != "" {
			return leaked
		}
	}
	return ""
}

func typedGenericMechanismViolations(
	root, profile string,
	pkg *packages.Package,
	policies []GenericMechanismUse,
) []string {
	allowedRoots := genericMechanismAllowedRoots(policies)
	violations := []string{}
	for _, file := range pkg.Syntax {
		position := pkg.Fset.Position(file.Pos())
		rel, err := filepath.Rel(root, position.Filename)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if !underAnyRoot(rel, []string{"internal/app", "internal/cli", "internal/modules", "internal/webui/handlers"}) {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || !isGenericMechanismSelector(selector.Sel.Name) {
				return true
			}
			selection := pkg.TypesInfo.Selections[selector]
			if selection == nil {
				return true
			}
			if _, ok := selection.Obj().(*types.Func); !ok {
				return true
			}
			if underAnyRoot(rel, allowedRoots[selector.Sel.Name]) {
				return true
			}
			selectorPosition := pkg.Fset.Position(selector.Sel.Pos())
			violations = append(violations, fmt.Sprintf(
				"profile %s file %s:%d directly references generic %s; use an owner-scoped adapter",
				profile, rel, selectorPosition.Line, selector.Sel.Name,
			))
			return true
		})
	}
	return violations
}

func transitiveModuleViolations(profile string, root *packages.Package) []string {
	queue := []*packages.Package{root}
	seen := map[string]struct{}{root.PkgPath: {}}
	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		for importPath, imported := range pkg.Imports {
			if _, ok := seen[importPath]; ok {
				continue
			}
			seen[importPath] = struct{}{}
			if isForbiddenModuleDependency(importPath) {
				return []string{fmt.Sprintf("profile %s package %s reaches forbidden transitive dependency %s through %s", profile, root.PkgPath, importPath, pkg.PkgPath)}
			}
			if strings.HasPrefix(importPath, modulePath+"/internal/") {
				queue = append(queue, imported)
			}
		}
	}
	return nil
}

func analyzeAllGoFiles(root string, matrix AnalysisMatrix, graph CapabilityGraph, genericMechanisms []GenericMechanismUse) ([]string, error) {
	state := &allFileScanState{
		root: root, profile: matrix.AST, graph: graph,
		genericMechanisms: genericMechanisms,
		ignored:           make(map[string]IgnoreException, len(matrix.AST.Ignore)),
		seenIgnored:       map[string]struct{}{},
	}
	for _, exception := range matrix.AST.Ignore {
		state.ignored[exception.Path] = exception
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && excludedWalkDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		return state.scan(path)
	})
	if err != nil {
		return nil, err
	}
	for path := range state.ignored {
		if _, ok := state.seenIgnored[path]; !ok {
			state.violations = append(state.violations, fmt.Sprintf("stale AST ignore exception %s", path))
		}
	}
	return state.violations, nil
}

type allFileScanState struct {
	root              string
	profile           ASTProfile
	graph             CapabilityGraph
	genericMechanisms []GenericMechanismUse
	ignored           map[string]IgnoreException
	seenIgnored       map[string]struct{}
	violations        []string
}

func (state *allFileScanState) scan(path string) error {
	if !strings.HasSuffix(path, ".go") {
		return nil
	}
	contents, err := os.ReadFile(path) //nolint:gosec // repository walk controls the path
	if err != nil {
		return err
	}
	if state.profile.ExcludeGenerated && generated(contents) {
		return nil
	}
	rel, err := filepath.Rel(state.root, path)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	if hasIgnoreBuildConstraint(contents) {
		state.recordIgnore(rel)
		return nil
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), path, contents, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse %s: %w", rel, err)
	}
	state.violations = append(state.violations, allFileBoundaryViolations(rel, parsed, state.graph, state.genericMechanisms)...)
	return nil
}

func (state *allFileScanState) recordIgnore(rel string) {
	exception, ok := state.ignored[rel]
	if !ok {
		state.violations = append(state.violations, fmt.Sprintf("AST profile found unallowlisted //go:build ignore file %s", rel))
		return
	}
	state.seenIgnored[rel] = struct{}{}
	expiry, err := time.Parse(time.DateOnly, exception.Expiry)
	if err != nil || !expiry.After(time.Now().UTC()) {
		state.violations = append(state.violations, fmt.Sprintf("AST ignore exception %s has invalid or expired date %s", rel, exception.Expiry))
	}
}

func excludedWalkDirectory(name string) bool {
	return name == ".git" || name == "node_modules" || name == "vendor" || name == "third_party" || name == "worktrees"
}

func hasIgnoreBuildConstraint(contents []byte) bool {
	for _, line := range strings.SplitN(string(contents), "\n", 20) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "//go:build ignore" || strings.Contains(trimmed, "//go:build") && strings.Contains(trimmed, "ignore") {
			return true
		}
		if strings.HasPrefix(trimmed, "package ") {
			break
		}
	}
	return false
}

func allFileBoundaryViolations(rel string, file *ast.File, graph CapabilityGraph, genericMechanisms []GenericMechanismUse) []string {
	directoryImport := modulePath + "/" + strings.TrimSuffix(filepath.ToSlash(filepath.Dir(rel)), ".")
	aliases, violations := boundaryImportViolations(rel, directoryImport, file.Imports, graph)
	violations = append(violations, genericMechanismBoundaryViolations(rel, file, genericMechanisms)...)
	if !strings.HasPrefix(rel, graph.ModuleRoot+"/") {
		return violations
	}
	return append(violations, moduleASTViolations(rel, file, aliases, graph)...)
}

func genericMechanismBoundaryViolations(rel string, file *ast.File, policies []GenericMechanismUse) []string {
	if !underAnyRoot(rel, []string{"internal/app", "internal/cli", "internal/modules", "internal/webui/handlers"}) {
		return nil
	}
	violations := []string{}
	allowedRoots := genericMechanismAllowedRoots(policies)
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !isGenericMechanismSelector(selector.Sel.Name) {
			return true
		}
		if underAnyRoot(rel, allowedRoots[selector.Sel.Name]) {
			return true
		}
		violations = append(violations, fmt.Sprintf("AST file %s directly accesses generic %s; use an owner-scoped adapter", rel, selector.Sel.Name))
		return true
	})
	return violations
}

func genericMechanismAllowedRoots(policies []GenericMechanismUse) map[string][]string {
	allowedRoots := map[string][]string{}
	for _, policy := range policies {
		switch policy.Mechanism {
		case "action_ledger":
			allowedRoots["ActionLedger"] = policy.AllowedAdapterRoots
			allowedRoots["ActionLedgers"] = policy.AllowedAdapterRoots
		case "lease":
			allowedRoots["Leases"] = policy.AllowedAdapterRoots
		}
	}
	return allowedRoots
}

func isGenericMechanismSelector(name string) bool {
	return name == "Leases" || name == "ActionLedger" || name == "ActionLedgers"
}

func boundaryImportViolations(rel, directoryImport string, specs []*ast.ImportSpec, graph CapabilityGraph) (map[string]string, []string) {
	aliases := map[string]string{}
	violations := []string{}
	for _, spec := range specs {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		alias := filepath.Base(importPath)
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		aliases[alias] = importPath
		if reason := forbiddenBoundaryImport(directoryImport, importPath, graph); reason != "" {
			violations = append(violations, fmt.Sprintf("AST file %s imports %s: %s", rel, importPath, reason))
		}
	}
	return aliases, violations
}

func moduleASTViolations(rel string, file *ast.File, aliases map[string]string, graph CapabilityGraph) []string {
	localLeaks := localLeakedTypeAliases(file, aliases, graph)
	violations := compositeStoreInjectionViolations(rel, file, aliases)
	return append(violations, exportedTypeLeakViolations(rel, file, aliases, localLeaks, graph)...)
}

func compositeStoreInjectionViolations(rel string, file *ast.File, aliases map[string]string) []string {
	violations := []string{}
	storeAlias := ""
	for alias, importPath := range aliases {
		if importPath == modulePath+"/internal/store" {
			storeAlias = alias
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		if ident.Name == storeAlias && selector.Sel.Name == "Store" {
			violations = append(violations, fmt.Sprintf("AST file %s injects composite store.Store into a migrated module", rel))
		}
		return true
	})
	return violations
}

func exportedTypeLeakViolations(rel string, file *ast.File, aliases, localLeaks map[string]string, graph CapabilityGraph) []string {
	violations := []string{}
	for _, decl := range file.Decls {
		if !exportedDeclaration(decl) {
			continue
		}
		for _, expression := range exportedSignatureExpressions(decl) {
			ast.Inspect(expression, func(node ast.Node) bool {
				if ident, ok := node.(*ast.Ident); ok {
					if importPath := localLeaks[ident.Name]; importPath != "" {
						violations = append(violations, fmt.Sprintf("AST file %s exports local alias %s of forbidden type from %s", rel, ident.Name, importPath))
					}
					return true
				}
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := selector.X.(*ast.Ident)
				if !ok {
					return true
				}
				if importPath := aliases[ident.Name]; isLeakedSignatureImport(importPath, graph) {
					violations = append(violations, fmt.Sprintf("AST file %s exports legacy or implementation type %s.%s", rel, ident.Name, selector.Sel.Name))
				}
				return true
			})
		}
	}
	return violations
}

func localLeakedTypeAliases(file *ast.File, aliases map[string]string, graph CapabilityGraph) map[string]string {
	leaked := map[string]string{}
	typeSpecs := []*ast.TypeSpec{}
	for _, declaration := range file.Decls {
		generic, ok := declaration.(*ast.GenDecl)
		if !ok || generic.Tok != token.TYPE {
			continue
		}
		for _, spec := range generic.Specs {
			if typed, ok := spec.(*ast.TypeSpec); ok {
				typeSpecs = append(typeSpecs, typed)
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, spec := range typeSpecs {
			if leaked[spec.Name.Name] != "" {
				continue
			}
			for _, expression := range publicTypeExpressions(spec.Type) {
				if importPath := firstLeakedASTTypeImport(expression, aliases, leaked, graph); importPath != "" {
					leaked[spec.Name.Name] = importPath
					changed = true
					break
				}
			}
		}
	}
	return leaked
}

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
