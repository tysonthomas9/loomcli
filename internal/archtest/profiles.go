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

// repositoryScaleLoadConcurrency deliberately stays at one. A repository-wide
// packages.Load with syntax and type information keeps a complete typed graph
// live until the profile analysis returns. Running multiple profiles at once
// multiplies peak memory without improving the type-checker's bounded
// throughput enough to justify the cost.
const repositoryScaleLoadConcurrency = 1

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
	semaphore := make(chan struct{}, repositoryScaleLoadConcurrency)
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
	violations, seenPackageErrors, err := analyzeProfileDependencyMetadata(root, profile, graph)
	if err != nil {
		return nil, err
	}
	typedViolations, genericMechanismPatterns, err := analyzeProfileTypedRoots(root, profile, graph, seenPackageErrors)
	if err != nil {
		return nil, err
	}
	violations = append(violations, typedViolations...)
	genericMechanismViolations, err := analyzeProfileGenericMechanisms(root, profile, genericMechanismPatterns, genericMechanisms, seenPackageErrors)
	if err != nil {
		return nil, err
	}
	return append(violations, genericMechanismViolations...), nil
}

// analyzeProfileTypedRoots deliberately returns only strings and package patterns so
// its package graph becomes unreachable before the focused TypesInfo load.
func analyzeProfileTypedRoots(
	root string,
	profile AnalysisProfile,
	graph CapabilityGraph,
	seenPackageErrors map[string]struct{},
) ([]string, []string, error) {
	typedRoots, err := packages.Load(profilePackagesConfig(root, profile, profileTypedRootLoadMode), "./...")
	if err != nil {
		return nil, nil, err
	}
	violations := []string{}
	for _, pkg := range typedRoots {
		violations = append(violations, profilePackageErrorViolations(profile.Name, pkg, seenPackageErrors)...)
		violations = append(violations, profilePackageViolations(profile.Name, pkg, graph)...)
	}
	violations = append(violations, requiredSourceViolations(root, profile, typedRoots)...)
	genericMechanismPatterns, err := genericMechanismCandidatePatterns(root, typedRoots)
	if err != nil {
		return nil, nil, err
	}
	return violations, genericMechanismPatterns, nil
}

func analyzeProfileGenericMechanisms(
	root string,
	profile AnalysisProfile,
	candidatePatterns []string,
	policies []GenericMechanismUse,
	seenPackageErrors map[string]struct{},
) ([]string, error) {
	if len(candidatePatterns) == 0 {
		return nil, nil
	}
	packagesWithCandidates, err := packages.Load(
		profilePackagesConfig(root, profile, profileGenericMechanismLoadMode),
		candidatePatterns...,
	)
	if err != nil {
		return nil, err
	}
	violations := []string{}
	seenViolations := map[string]struct{}{}
	for _, pkg := range packagesWithCandidates {
		violations = append(violations, profilePackageErrorViolations(profile.Name, pkg, seenPackageErrors)...)
		if !profilePackageChecked(pkg) {
			continue
		}
		for _, violation := range typedGenericMechanismViolations(root, profile.Name, pkg, policies) {
			if _, seen := seenViolations[violation]; seen {
				continue
			}
			seenViolations[violation] = struct{}{}
			violations = append(violations, violation)
		}
	}
	return violations, nil
}

func analyzeProfileDependencyMetadata(
	root string,
	profile AnalysisProfile,
	graph CapabilityGraph,
) ([]string, map[string]struct{}, error) {
	dependencyRoots, err := packages.Load(profilePackagesConfig(root, profile, profileDependencyLoadMode), "./...")
	if err != nil {
		return nil, nil, err
	}
	violations := []string{}
	seenPackageErrors := map[string]struct{}{}
	for _, pkg := range dependencyRoots {
		violations = append(violations, profilePackageErrorViolations(profile.Name, pkg, seenPackageErrors)...)
		if !profilePackageChecked(pkg) {
			continue
		}
		packageBoundary := classifyBoundaryPackage(pkg.PkgPath, graph)
		if packageBoundary.kind == boundaryPackageCapabilityPublic || packageBoundary.kind == boundaryPackageCapabilityCore {
			violations = append(violations, transitiveModuleViolations(profile.Name, pkg)...)
		}
	}
	return violations, seenPackageErrors, nil
}

func profilePackageErrorViolations(profile string, pkg *packages.Package, seen map[string]struct{}) []string {
	violations := []string{}
	for _, pkgErr := range pkg.Errors {
		message := fmt.Sprintf("profile %s package %s: %s", profile, pkg.PkgPath, pkgErr.Msg)
		if _, exists := seen[message]; exists {
			continue
		}
		seen[message] = struct{}{}
		violations = append(violations, message)
	}
	return violations
}

const profileTypedRootLoadMode packages.LoadMode = packages.NeedName |
	packages.NeedFiles |
	packages.NeedCompiledGoFiles |
	packages.NeedTypes |
	packages.NeedImports

const profileGenericMechanismLoadMode packages.LoadMode = profileTypedRootLoadMode |
	packages.NeedSyntax |
	packages.NeedTypesInfo

const profileDependencyLoadMode packages.LoadMode = packages.NeedName |
	packages.NeedImports |
	packages.NeedDeps

func profilePackagesConfig(root string, profile AnalysisProfile, mode packages.LoadMode) *packages.Config {
	return &packages.Config{
		Mode:       mode,
		Dir:        root,
		Env:        profileEnvironment(profile),
		Tests:      true,
		BuildFlags: profileBuildFlags(profile),
	}
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

func profilePackageViolations(profile string, pkg *packages.Package, graph CapabilityGraph) []string {
	if !profilePackageChecked(pkg) {
		return nil
	}
	violations := []string{}
	for importPath := range pkg.Imports {
		if reason := forbiddenBoundaryImport(pkg.PkgPath, importPath, graph); reason != "" {
			violations = append(violations, fmt.Sprintf("profile %s package %s imports %s: %s", profile, pkg.PkgPath, importPath, reason))
		}
	}
	packageBoundary := classifyBoundaryPackage(pkg.PkgPath, graph)
	if isCapabilityPackage(packageBoundary.kind) {
		violations = append(violations, typedExportedSignatureViolations(profile, pkg, graph)...)
	}
	return violations
}

func profilePackageChecked(pkg *packages.Package) bool {
	if !strings.HasPrefix(pkg.PkgPath, modulePath+"/internal/") {
		return false
	}
	// packages.Load(Tests: true) adds a synthetic test-main package whose path
	// ends in ".test". Its imports are the test harness, not a production or
	// test source boundary. The real package-under-test variants are loaded
	// separately and remain fully checked, including their _test.go imports.
	if strings.HasSuffix(pkg.PkgPath, ".test") {
		return false
	}
	return true
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

func genericMechanismCandidatePatterns(root string, loaded []*packages.Package) ([]string, error) {
	visited := map[string]struct{}{}
	candidatePatterns := map[string]struct{}{}
	for _, pkg := range loaded {
		if !profilePackageChecked(pkg) {
			continue
		}
		for _, path := range pkg.CompiledGoFiles {
			if _, seen := visited[path]; seen {
				continue
			}
			visited[path] = struct{}{}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil, fmt.Errorf("resolve generic-mechanism candidate %s: %w", path, err)
			}
			rel = filepath.ToSlash(rel)
			if !genericMechanismSourceChecked(rel) {
				continue
			}
			candidate, err := sourceContainsGenericMechanismSelector(path)
			if err != nil {
				return nil, fmt.Errorf("prefilter generic-mechanism candidate %s: %w", rel, err)
			}
			if candidate {
				candidatePatterns["./"+filepath.ToSlash(filepath.Dir(rel))] = struct{}{}
			}
		}
	}
	patterns := make([]string, 0, len(candidatePatterns))
	for pattern := range candidatePatterns {
		patterns = append(patterns, pattern)
	}
	slices.Sort(patterns)
	return patterns, nil
}

func sourceContainsGenericMechanismSelector(path string) (bool, error) {
	contents, err := os.ReadFile(path) //nolint:gosec // paths come from go/packages CompiledGoFiles
	if err != nil {
		return false, err
	}
	parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, contents, parser.SkipObjectResolution)
	if parseErr != nil {
		// Fail closed by sending malformed selected source through the focused
		// load too. The package error remains the authoritative diagnostic.
		return true, nil
	}
	candidate := false
	ast.Inspect(parsed, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && isGenericMechanismSelector(selector.Sel.Name) {
			candidate = true
			return false
		}
		return !candidate
	})
	return candidate, nil
}

func genericMechanismSourceChecked(rel string) bool {
	return underAnyRoot(rel, []string{"internal/app", "internal/cli", "internal/modules", "internal/webui/handlers"})
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
		if !genericMechanismSourceChecked(rel) {
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
	if !genericMechanismSourceChecked(rel) {
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
