package archtest

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const modulePath = "github.com/tysonthomas9/loomcli"

type Report struct {
	CompositeStoreFiles          []string
	CompositeStoreOutside        []string
	ModuleRoots                  []string
	ModuleImportEdges            []ObservedEdge
	PendingDecisions             []string
	CompositeStoreMaximum        int
	CompositeStoreOutsideMaximum int
	LegacyHandlerImports         []LegacyImportUse
	LegacyHandlerImportMaximum   int
	AnalysisProfileTotal         int
	AnalysisProfilesEnforced     int
	MutationCommands             int
	DirectPersistenceWrites      int
	RuntimeComponents            int
	RuntimeGoroutineLaunches     int
	PerformanceMetrics           int
	PerformanceMetricsMeasured   int
	PerformanceMetricsDeferred   int
}

type ObservedEdge struct {
	From string
	To   string
	File string
}

type ViolationsError struct {
	Violations []string
}

func (e *ViolationsError) Error() string {
	return "architecture guardrail violations:\n- " + strings.Join(e.Violations, "\n- ")
}

func CheckRepository(root, manifestsDir string) (Report, error) {
	manifests, err := loadRepositoryManifests(manifestsDir)
	if err != nil {
		return Report{}, err
	}
	violations := validateGraphDecisions(manifests.graph, manifests.baseline)
	report, scanViolations, err := scanRepository(root, manifests.baseline, manifests.graph)
	if err != nil {
		return Report{}, err
	}
	report.AnalysisProfileTotal = len(manifests.matrix.Release) + len(manifests.matrix.Tagged)
	report.MutationCommands = len(manifests.ledger.Commands)
	report.RuntimeComponents = len(manifests.runtime.Components)
	report.RuntimeGoroutineLaunches = len(manifests.runtime.GoroutineLaunches)
	report.PerformanceMetrics = 6
	for _, status := range []string{
		manifests.performance.LoomServeStartupReadiness.Record.Status,
		manifests.performance.WorkflowApprovalLatency.Record.Status,
		manifests.performance.FleetDBRoundTrips.Record.Status,
		manifests.performance.ProductionBackgroundLoops.Record.Status,
		manifests.performance.FullBuildGateDuration.Record.Status,
		manifests.performance.FrontendRouteChunkSizes.Record.Status,
	} {
		if status == performanceMeasured {
			report.PerformanceMetricsMeasured++
		} else {
			report.PerformanceMetricsDeferred++
		}
	}
	profiles := append(append([]AnalysisProfile{}, manifests.matrix.Release...), manifests.matrix.Tagged...)
	for _, profile := range profiles {
		if profile.Enforced {
			report.AnalysisProfilesEnforced++
		}
	}
	violations = append(violations, scanViolations...)
	extended, err := runPhase1Analyses(root, manifests, &report)
	if err != nil {
		return report, err
	}
	violations = append(violations, extended...)
	slices.Sort(violations)
	if len(violations) > 0 {
		return report, &ViolationsError{Violations: violations}
	}
	return report, nil
}

type repositoryManifests struct {
	baseline     Baseline
	graph        CapabilityGraph
	matrix       AnalysisMatrix
	ledger       MutationLedger
	directWrites DirectWriteInventory
	runtime      RuntimeInventory
	performance  PerformanceInventory
}

func loadRepositoryManifests(directory string) (repositoryManifests, error) {
	var result repositoryManifests
	loaders := []func() error{
		func() (err error) {
			result.baseline, err = LoadBaseline(filepath.Join(directory, "migration-baseline.json"))
			return err
		},
		func() (err error) {
			result.graph, err = LoadCapabilityGraph(filepath.Join(directory, "capability-graph.yaml"))
			return err
		},
		func() (err error) {
			result.matrix, err = LoadAnalysisMatrix(filepath.Join(directory, "analysis-matrix.yaml"))
			return err
		},
		func() (err error) {
			result.ledger, err = LoadMutationLedger(filepath.Join(directory, "mutation-ledger.yaml"))
			return err
		},
		func() (err error) {
			result.directWrites, err = LoadDirectWriteInventory(filepath.Join(directory, "direct-writes.yaml"))
			return err
		},
		func() (err error) {
			result.runtime, err = LoadRuntimeInventory(filepath.Join(directory, "runtime-components.yaml"))
			return err
		},
		func() (err error) {
			result.performance, err = LoadPerformanceInventory(filepath.Join(directory, "performance-baseline.yaml"))
			return err
		},
	}
	for _, load := range loaders {
		if err := load(); err != nil {
			return repositoryManifests{}, err
		}
	}
	if err := result.directWrites.ValidateCompletedPhase(result.graph.CompletedPhase); err != nil {
		return repositoryManifests{}, err
	}
	return result, nil
}

func runPhase1Analyses(root string, manifests repositoryManifests, report *Report) ([]string, error) {
	profileViolations, err := analyzeProfiles(root, manifests.matrix, manifests.graph, manifests.directWrites.GenericMechanisms)
	if err != nil {
		return nil, err
	}
	astViolations, err := analyzeAllGoFiles(root, manifests.matrix, manifests.graph, manifests.directWrites.GenericMechanisms)
	if err != nil {
		return nil, err
	}
	observedWrites, directWriteViolations, err := CheckDirectWrites(root, manifests.matrix, manifests.directWrites)
	if err != nil {
		return nil, err
	}
	report.DirectPersistenceWrites = len(observedWrites)
	violations := append([]string{}, profileViolations...)
	violations = append(violations, astViolations...)
	violations = append(violations, directWriteViolations...)
	if err := CompareRuntimeTickerInventory(root, manifests.runtime); err != nil {
		violations = append(violations, err.Error())
	}
	if err := ComparePerformanceRuntimeInventory(manifests.performance, manifests.runtime); err != nil {
		violations = append(violations, err.Error())
	}
	return violations, nil
}

func validateGraphDecisions(graph CapabilityGraph, baseline Baseline) []string {
	decisions := make(map[string]string, len(baseline.Decisions))
	for _, decision := range baseline.Decisions {
		decisions[decision.ID] = decision.Status
	}
	violations := []string{}
	required := graph.DecisionDependencies
	if graph.Status == "approved" {
		required = migrationDecisionIDs()
	}
	for _, id := range required {
		status, ok := decisions[id]
		if !ok {
			violations = append(violations, fmt.Sprintf("capability graph depends on unrecorded decision %s", id))
			continue
		}
		if graph.Status == "approved" && status != "approved" {
			violations = append(violations, fmt.Sprintf("approved capability graph requires %s to be approved; status is %s", id, status))
		}
	}
	return violations
}

func scanRepository(root string, baseline Baseline, graph CapabilityGraph) (Report, []string, error) {
	storeFiles, err := findCompositeStoreFiles(root)
	if err != nil {
		return Report{}, nil, err
	}
	ratchet := baseline.Ratchets.CompositeStore
	outside := outsidePrefixes(storeFiles, ratchet.CompositionPrefixes)
	handlerImports, err := findLegacyImports(root, baseline.Ratchets.LegacyHandlerImports)
	if err != nil {
		return Report{}, nil, err
	}
	serviceImports, err := findLegacyImports(root, baseline.Ratchets.LegacyHandlerServiceImports)
	if err != nil {
		return Report{}, nil, err
	}
	handlerImports = append(handlerImports, serviceImports...)
	slices.SortFunc(handlerImports, func(a, b LegacyImportUse) int {
		return strings.Compare(a.File+"\x00"+a.Import, b.File+"\x00"+b.Import)
	})
	report := Report{
		CompositeStoreFiles:          storeFiles,
		CompositeStoreOutside:        outside,
		CompositeStoreMaximum:        ratchet.MaxProductionFiles,
		CompositeStoreOutsideMaximum: ratchet.MaxOutsideComposition,
		LegacyHandlerImports:         handlerImports,
		LegacyHandlerImportMaximum:   len(baseline.Ratchets.LegacyHandlerImports.Allowed) + len(baseline.Ratchets.LegacyHandlerServiceImports.Allowed),
	}

	violations := compositeStoreViolations(storeFiles, outside, ratchet)
	violations = append(violations, legacyImportViolations(handlerImports, baseline.Ratchets.LegacyHandlerImports)...)
	violations = append(violations, legacyImportViolations(handlerImports, baseline.Ratchets.LegacyHandlerServiceImports)...)

	roots, edges, moduleViolations, err := inspectModules(root, graph)
	if err != nil {
		return Report{}, nil, err
	}
	report.ModuleRoots = roots
	report.ModuleImportEdges = edges
	violations = append(violations, moduleViolations...)
	for _, decision := range baseline.Decisions {
		if decision.Status == "pending" {
			report.PendingDecisions = append(report.PendingDecisions, decision.ID)
		}
	}
	slices.Sort(report.PendingDecisions)
	return report, violations, nil
}

func compositeStoreViolations(storeFiles, outside []string, ratchet CompositeStoreRatchet) []string {
	violations := []string{}
	allowed := make(map[string]struct{}, len(ratchet.AllowedProductionFileUses))
	for _, path := range ratchet.AllowedProductionFileUses {
		allowed[path] = struct{}{}
	}
	for _, path := range storeFiles {
		if _, ok := allowed[path]; !ok {
			violations = append(violations, fmt.Sprintf("new composite Store use in %s", path))
		}
	}
	observed := append([]string(nil), storeFiles...)
	expected := append([]string(nil), ratchet.AllowedProductionFileUses...)
	slices.Sort(observed)
	slices.Sort(expected)
	if !slices.Equal(observed, expected) {
		observedSet := sliceSet(observed)
		for _, path := range expected {
			if _, ok := observedSet[path]; !ok {
				violations = append(violations, fmt.Sprintf("stale composite Store baseline entry %s; remove it so the debt cannot be reintroduced", path))
			}
		}
	}
	if len(storeFiles) > ratchet.MaxProductionFiles {
		violations = append(violations, fmt.Sprintf("composite Store production files increased to %d (maximum %d)", len(storeFiles), ratchet.MaxProductionFiles))
	}
	if len(outside) > ratchet.MaxOutsideComposition {
		violations = append(violations, fmt.Sprintf("composite Store files outside composition increased to %d (maximum %d)", len(outside), ratchet.MaxOutsideComposition))
	}
	return violations
}

func legacyImportViolations(observed []LegacyImportUse, ratchet LegacyImportRatchet) []string {
	violations := []string{}
	allowedHandlerImports := map[string]struct{}{}
	for _, use := range ratchet.Allowed {
		allowedHandlerImports[use.File+"\x00"+use.Import] = struct{}{}
	}
	for _, use := range observed {
		if !matchesImportPrefix(use.Import, ratchet.DeniedPrefixes) {
			continue
		}
		if _, ok := allowedHandlerImports[use.File+"\x00"+use.Import]; !ok {
			violations = append(violations, fmt.Sprintf("new forbidden handler import %s in %s", use.Import, use.File))
		}
	}
	observedKeys := map[string]struct{}{}
	for _, use := range observed {
		if matchesImportPrefix(use.Import, ratchet.DeniedPrefixes) {
			observedKeys[use.File+"\x00"+use.Import] = struct{}{}
		}
	}
	for key := range allowedHandlerImports {
		if _, ok := observedKeys[key]; !ok {
			parts := strings.SplitN(key, "\x00", 2)
			violations = append(violations, fmt.Sprintf("stale forbidden-import baseline entry %s in %s; remove it so the debt cannot be reintroduced", parts[1], parts[0]))
		}
	}
	return violations
}

func sliceSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func findCompositeStoreFiles(root string) ([]string, error) {
	internalRoot := filepath.Join(root, "internal")
	files := []string{}
	err := filepath.WalkDir(internalRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == "vendor" || name == "third_party" || name == "node_modules" || name == "worktrees" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		contents, err := os.ReadFile(path) //nolint:gosec // WalkDir constrains path to the repository's internal tree.
		if err != nil {
			return err
		}
		if generated(contents) {
			return nil
		}
		used, err := fileUsesCompositeStore(path, contents)
		if err != nil {
			return err
		}
		if used {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan production Go files: %w", err)
	}
	slices.Sort(files)
	return files, nil
}

func findLegacyImports(root string, ratchet LegacyImportRatchet) ([]LegacyImportUse, error) {
	handlerRoot := filepath.Join(root, filepath.FromSlash(ratchet.Root))
	uses := []LegacyImportUse{}
	err := filepath.WalkDir(handlerRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		contents, err := os.ReadFile(path) //nolint:gosec // WalkDir constrains path to the configured handler root.
		if err != nil || generated(contents) {
			return err
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, contents, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for _, spec := range parsed.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if matchesImportPrefix(importPath, ratchet.DeniedPrefixes) {
				uses = append(uses, LegacyImportUse{File: filepath.ToSlash(rel), Import: importPath})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(uses, func(a, b LegacyImportUse) int {
		return strings.Compare(a.File+"\x00"+a.Import, b.File+"\x00"+b.Import)
	})
	return uses, nil
}

func fileUsesCompositeStore(path string, contents []byte) (bool, error) {
	aliases, dotImport, err := compositeStoreAliases(path, contents)
	if err != nil || (len(aliases) == 0 && !dotImport) {
		return false, err
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), path, contents, 0)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	return usesStoreSelector(parsed, aliases, dotImport), nil
}

func compositeStoreAliases(path string, contents []byte) (map[string]struct{}, bool, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), path, contents, parser.ImportsOnly)
	if err != nil {
		return nil, false, fmt.Errorf("parse imports in %s: %w", path, err)
	}
	aliases := map[string]struct{}{}
	dotImport := false
	for _, spec := range parsed.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, false, fmt.Errorf("parse import path in %s: %w", path, err)
		}
		if importPath != modulePath+"/internal/store" {
			continue
		}
		alias := "store"
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		if alias == "." {
			dotImport = true
			continue
		}
		if alias != "_" {
			aliases[alias] = struct{}{}
		}
	}
	return aliases, dotImport, nil
}

func usesStoreSelector(parsed *ast.File, aliases map[string]struct{}, dotImport bool) bool {
	used := false
	ast.Inspect(parsed, func(node ast.Node) bool {
		if used {
			return false
		}
		if selector, ok := node.(*ast.SelectorExpr); ok && selector.Sel.Name == "Store" {
			if ident, ok := selector.X.(*ast.Ident); ok {
				_, used = aliases[ident.Name]
			}
		}
		if dotImport {
			if ident, ok := node.(*ast.Ident); ok && ident.Name == "Store" {
				used = true
			}
		}
		return !used
	})
	return used
}

func generated(contents []byte) bool {
	lines := strings.SplitN(string(contents), "\n", 4)
	for i := 0; i < len(lines) && i < 3; i++ {
		if strings.Contains(lines[i], "// Code generated") {
			return true
		}
	}
	return false
}

func outsidePrefixes(files, prefixes []string) []string {
	result := []string{}
	for _, path := range files {
		inside := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(path, prefix) {
				inside = true
				break
			}
		}
		if !inside {
			result = append(result, path)
		}
	}
	return result
}

func inspectModules(root string, graph CapabilityGraph) ([]string, []ObservedEdge, []string, error) {
	moduleRoot := filepath.Join(root, filepath.FromSlash(graph.ModuleRoot))
	byRoot := capabilityNamesByRoot(graph.Capabilities)
	roots, violations, err := inspectModuleRoots(moduleRoot, byRoot, graph.Status)
	if err != nil {
		return nil, nil, nil, err
	}
	if roots == nil {
		return nil, nil, nil, nil
	}
	edges, importViolations, err := inspectModuleImports(root, moduleRoot, graph, byRoot)
	if err != nil {
		return nil, nil, nil, err
	}
	return roots, edges, append(violations, importViolations...), nil
}

func capabilityNamesByRoot(capabilities []Capability) map[string]string {
	result := make(map[string]string, len(capabilities))
	for _, capability := range capabilities {
		result[capability.Root] = capability.Name
	}
	return result
}

func inspectModuleRoots(moduleRoot string, byRoot map[string]string, graphStatus string) ([]string, []string, error) {
	entries, err := os.ReadDir(moduleRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	roots := []string{}
	violations := []string{}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		roots = append(roots, entry.Name())
		if _, ok := byRoot[entry.Name()]; !ok {
			violations = append(violations, fmt.Sprintf("undeclared module root %s", entry.Name()))
		}
	}
	slices.Sort(roots)
	if graphStatus != "approved" && len(roots) > 0 {
		violations = append(violations, "capability package roots exist while the capability graph is still proposed")
	}
	return roots, violations, nil
}

func inspectModuleImports(root, moduleRoot string, graph CapabilityGraph, byRoot map[string]string) ([]ObservedEdge, []string, error) {
	allowedImports := allowedModuleImports(graph.Edges)
	edges := []ObservedEdge{}
	violations := []string{}
	err := filepath.WalkDir(moduleRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		fileEdges, fileViolations, err := inspectModuleFile(root, moduleRoot, path, graph, byRoot, allowedImports)
		if err != nil {
			return err
		}
		edges = append(edges, fileEdges...)
		violations = append(violations, fileViolations...)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	slices.SortFunc(edges, func(a, b ObservedEdge) int {
		return strings.Compare(a.From+"\x00"+a.To+"\x00"+a.File, b.From+"\x00"+b.To+"\x00"+b.File)
	})
	return edges, violations, nil
}

func allowedModuleImports(edges []GraphEdge) map[string]struct{} {
	result := map[string]struct{}{}
	for _, edge := range edges {
		for _, kind := range edge.Kinds {
			if kind == "import" {
				result[edge.From+"\x00"+edge.To] = struct{}{}
			}
		}
	}
	return result
}

func inspectModuleFile(root, moduleRoot, path string, graph CapabilityGraph, byRoot map[string]string, allowed map[string]struct{}) ([]ObservedEdge, []string, error) {
	rel, err := filepath.Rel(moduleRoot, path)
	if err != nil {
		return nil, nil, err
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		fileRel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil, nil, relErr
		}
		return nil, []string{fmt.Sprintf("%s places Go code directly at the module root", filepath.ToSlash(fileRel))}, nil
	}
	from, known := byRoot[parts[0]]
	if !known {
		return nil, nil, nil
	}
	contents, err := os.ReadFile(path) //nolint:gosec // WalkDir constrains path to the declared module root.
	if err != nil || generated(contents) {
		return nil, nil, err
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), path, contents, parser.ImportsOnly)
	if err != nil {
		return nil, nil, err
	}
	fileRel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, nil, err
	}
	return inspectModuleSpecs(parsed.Imports, graph, from, filepath.ToSlash(fileRel), byRoot, allowed)
}

func inspectModuleSpecs(specs []*ast.ImportSpec, graph CapabilityGraph, from, file string, byRoot map[string]string, allowed map[string]struct{}) ([]ObservedEdge, []string, error) {
	edges := []ObservedEdge{}
	violations := []string{}
	prefix := modulePath + "/" + graph.ModuleRoot + "/"
	internalPrefix := modulePath + "/internal/"
	directoryImport := modulePath + "/" + strings.TrimSuffix(filepath.ToSlash(filepath.Dir(file)), ".")
	for _, spec := range specs {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, nil, err
		}
		if !strings.HasPrefix(importPath, internalPrefix) {
			continue
		}
		if reason := forbiddenBoundaryImport(directoryImport, importPath, graph); reason != "" {
			violations = append(violations, fmt.Sprintf("%s imports %s: %s", file, importPath, reason))
			continue
		}
		if !strings.HasPrefix(importPath, prefix) {
			// The shared boundary policy admits platform mechanisms from every
			// capability layer and the shared FleetDB transport only from a
			// capability's fleetdb adapter. They are not capability edges.
			continue
		}
		targetParts := strings.Split(strings.TrimPrefix(importPath, prefix), "/")
		to, known := byRoot[targetParts[0]]
		if !known {
			violations = append(violations, fmt.Sprintf("%s imports undeclared module root %s", file, targetParts[0]))
			continue
		}
		if to == from {
			continue
		}
		observed := ObservedEdge{From: from, To: to, File: file}
		edges = append(edges, observed)
		if len(targetParts) != 1 {
			violations = append(violations, fmt.Sprintf("%s imports non-public package of %s", file, to))
			continue
		}
		if _, ok := allowed[from+"\x00"+to]; !ok {
			violations = append(violations, fmt.Sprintf("%s has undeclared import edge %s -> %s", file, from, to))
		}
	}
	return edges, violations, nil
}
