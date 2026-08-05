package archtest

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCheckedInManifestsAndRepository(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	report, err := CheckRepository(root, filepath.Join(root, "internal", "archtest", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(report.CompositeStoreFiles), 92; got != want {
		t.Fatalf("composite Store file count = %d, want %d; files = %v", got, want, report.CompositeStoreFiles)
	}
	if got, want := len(report.CompositeStoreOutside), 81; got != want {
		t.Fatalf("outside-composition Store file count = %d, want %d", got, want)
	}
	if got, want := len(report.LegacyHandlerImports), 91; got != want {
		t.Fatalf("legacy handler imports = %d, want %d", got, want)
	}
	if got, want := report.ModuleRoots, []string{"workflowcatalog"}; !slices.Equal(got, want) {
		t.Fatalf("module roots = %v, want active Phase 2 extraction %v", got, want)
	}
	if got, want := len(report.PendingDecisions), 0; got != want {
		t.Fatalf("pending decisions = %d, want %d", got, want)
	}
	if got, want := report.AnalysisProfilesEnforced, 11; got != want {
		t.Fatalf("enforced analysis profiles = %d, want %d", got, want)
	}
	if got, want := report.MutationCommands, 3; got != want {
		t.Fatalf("mutation commands = %d, want %d", got, want)
	}
	if got, want := report.RuntimeComponents, 83; got != want {
		t.Fatalf("runtime components = %d, want %d", got, want)
	}
	if got, want := report.RuntimeGoroutineLaunches, 108; got != want {
		t.Fatalf("runtime goroutine launches = %d, want %d", got, want)
	}
	if got, want := report.PerformanceMetrics, 6; got != want {
		t.Fatalf("performance metrics = %d, want %d", got, want)
	}
	if got, want := report.PerformanceMetricsMeasured, 6; got != want {
		t.Fatalf("measured performance metrics = %d, want %d", got, want)
	}
	if got, want := report.PerformanceMetricsDeferred, 0; got != want {
		t.Fatalf("deferred performance metrics = %d, want %d", got, want)
	}
}

func TestBaselineRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	contents := `{"schema_version":1,"analyzer_version":"1.0.0","unknown":true}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBaseline(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected strict decode error, got %v", err)
	}
}

func TestBaselineRequiresEveryPhase1InventoryComplete(t *testing.T) {
	baseline, err := LoadBaseline(filepath.Join("testdata", "migration-baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseline.Inventories[0].Status = "deferred"
	if err := baseline.Validate(); err == nil || !strings.Contains(err.Error(), "must be complete") {
		t.Fatalf("Validate error = %v, want incomplete-inventory rejection", err)
	}

	baseline, err = LoadBaseline(filepath.Join("testdata", "migration-baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseline.Inventories[0].ID = "unexpected-inventory"
	if err := baseline.Validate(); err == nil || !strings.Contains(err.Error(), "missing Phase 1 inventory") {
		t.Fatalf("Validate error = %v, want exact-inventory rejection", err)
	}
}

func TestMutationLedgerRequiresEveryWorkflowCatalogPilotCommand(t *testing.T) {
	ledger, err := LoadMutationLedger(filepath.Join("testdata", "mutation-ledger.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	ledger.Commands = ledger.Commands[1:]
	if err := ledger.Validate(); err == nil || !strings.Contains(err.Error(), "missing required pilot command workflowcatalog.activate-version") {
		t.Fatalf("Validate error = %v, want missing-pilot-command rejection", err)
	}
}

func TestCapabilityGraphRejectsSynchronousCycle(t *testing.T) {
	graph := validGraph()
	graph.Edges = []GraphEdge{
		{From: "automation", To: "execution", Kinds: []string{"command"}},
		{From: "execution", To: "automation", Kinds: []string{"query"}},
	}
	if err := graph.Validate(); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestCapabilityGraphAllowsDurableReverseEdge(t *testing.T) {
	graph := validGraph()
	graph.Edges = []GraphEdge{
		{From: "automation", To: "execution", Kinds: []string{"command"}},
		{From: "execution", To: "automation", Kinds: []string{"durable_event"}, Durable: validDurablePolicy()},
	}
	if err := graph.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilityGraphApprovalRequiresCompletedBoundaryContract(t *testing.T) {
	graph := validGraph()
	graph.Status = "approved"
	if err := graph.Validate(); err != nil {
		t.Fatalf("completed graph should be approvable: %v", err)
	}
	graph.Restrictions.NamedAppsOwnPortsOnly = false
	if err := graph.Validate(); err == nil || !strings.Contains(err.Error(), "restrictions") {
		t.Fatalf("expected boundary-contract blocker, got %v", err)
	}
}

func TestCapabilityGraphRejectsUnboundedDurableEdge(t *testing.T) {
	graph := validGraph()
	graph.Edges = []GraphEdge{{From: "execution", To: "automation", Kinds: []string{"durable_event"}}}
	if err := graph.Validate(); err == nil || !strings.Contains(err.Error(), "requires idempotency") {
		t.Fatalf("expected durable policy error, got %v", err)
	}
}

func TestCapabilityGraphRequiresEveryMigrationDecision(t *testing.T) {
	graph := validGraph()
	graph.DecisionDependencies = graph.DecisionDependencies[:6]
	if err := graph.Validate(); err == nil || !strings.Contains(err.Error(), "decision dependencies") {
		t.Fatalf("expected decision-dependency error, got %v", err)
	}
}

func TestAnalysisMatrixRejectsDeferredProfileAfterPhase1(t *testing.T) {
	matrix, err := LoadAnalysisMatrix(filepath.Join("testdata", "analysis-matrix.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	matrix.Release[0].Enforced = false
	if err := matrix.Validate(); err == nil || !strings.Contains(err.Error(), "must be enforced") {
		t.Fatalf("expected deferred-profile error, got %v", err)
	}
}

func TestAnalysisMatrixRejectsWrongProfileTuple(t *testing.T) {
	matrix, err := LoadAnalysisMatrix(filepath.Join("testdata", "analysis-matrix.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	matrix.Release[0].GOOS = "windows"
	if err := matrix.Validate(); err == nil || !strings.Contains(err.Error(), "has tuple") {
		t.Fatalf("expected profile-tuple error, got %v", err)
	}
}

func TestAnalysisMatrixRequiresTaggedSourceSentinel(t *testing.T) {
	matrix, err := LoadAnalysisMatrix(filepath.Join("testdata", "analysis-matrix.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	matrix.Tagged[0].RequiredFiles = nil
	if err := matrix.Validate(); err == nil || !strings.Contains(err.Error(), "source-selection sentinel") {
		t.Fatalf("expected required-source sentinel error, got %v", err)
	}
}

func TestAnalysisMatrixRejectsUnsafeTaggedSourceSentinel(t *testing.T) {
	matrix, err := LoadAnalysisMatrix(filepath.Join("testdata", "analysis-matrix.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	matrix.Tagged[0].RequiredFiles = []string{"../outside.go"}
	if err := matrix.Validate(); err == nil || !strings.Contains(err.Error(), "clean relative Go source path") {
		t.Fatalf("expected unsafe required-source error, got %v", err)
	}
}

func TestRatchetRejectsNewCompositeStoreFile(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/newcap/new.go", `package newcap

import "github.com/tysonthomas9/loomcli/internal/store"

type State struct { Store store.Store }
`)
	baseline := scanBaseline(nil)
	_, violations, err := scanRepository(root, baseline, validGraph())
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "new composite Store use") {
		t.Fatalf("expected new-use violation, got %v", violations)
	}
}

func TestRatchetRequiresBaselineRefreshOnRemoval(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/empty/doc.go", "package empty\n")
	baseline := scanBaseline([]string{"internal/legacy/removed.go"})
	baseline.Ratchets.CompositeStore.MaxProductionFiles = 1
	_, violations, err := scanRepository(root, baseline, validGraph())
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "stale composite Store baseline entry") {
		t.Fatalf("removing a legacy use without refreshing the baseline must fail, got %v", violations)
	}
	baseline = scanBaseline(nil)
	_, violations, err = scanRepository(root, baseline, validGraph())
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("removal with refreshed baseline must pass, got %v", violations)
	}
}

func TestRatchetRejectsNewForbiddenHandlerImport(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/webui/handlers/new/module.go", `package new

import _ "github.com/tysonthomas9/loomcli/internal/store"
`)
	baseline := scanBaseline(nil)
	baseline.Ratchets.LegacyHandlerImports = LegacyImportRatchet{
		Root:           "internal/webui/handlers",
		DeniedPrefixes: []string{"github.com/tysonthomas9/loomcli/internal/store"},
	}
	_, violations, err := scanRepository(root, baseline, validGraph())
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "new forbidden handler import") {
		t.Fatalf("expected handler-import violation, got %v", violations)
	}
}

func TestProposedGraphBlocksPackageMoves(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/modules/workspace/doc.go", "package workspace\n")
	graph := validGraph()
	graph.Status = "proposed"
	_, violations, err := scanRepository(root, scanBaseline(nil), graph)
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "graph is still proposed") {
		t.Fatalf("expected proposed-graph violation, got %v", violations)
	}
}

func TestApprovedGraphRejectsUnknownModuleImport(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/modules/workspace/doc.go", `package workspace

import _ "github.com/tysonthomas9/loomcli/internal/modules/unknown"
`)
	graph := validGraph()
	graph.Status = "approved"
	_, violations, err := scanRepository(root, scanBaseline(nil), graph)
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "capability imports must target a declared capability public root") {
		t.Fatalf("expected unknown-root violation, got %v", violations)
	}
}

func TestApprovedGraphRejectsLegacyInternalImport(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/modules/workspace/doc.go", `package workspace

import _ "github.com/tysonthomas9/loomcli/internal/store"
`)
	graph := validGraph()
	graph.Status = "approved"
	_, violations, err := scanRepository(root, scanBaseline(nil), graph)
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "capability core may not import internal implementation package") {
		t.Fatalf("expected forbidden-internal-import violation, got %v", violations)
	}
}

func TestModuleRootRejectsGoFiles(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/modules/modules.go", "package modules\n")
	_, violations, err := scanRepository(root, scanBaseline(nil), validGraph())
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "directly at the module root") {
		t.Fatalf("expected module-root-file violation, got %v", violations)
	}
}

func TestViolationsErrorSupportsErrorsAs(t *testing.T) {
	err := error(&ViolationsError{Violations: []string{"one"}})
	var target *ViolationsError
	if !errors.As(err, &target) {
		t.Fatal("expected ViolationsError")
	}
}

func validGraph() CapabilityGraph {
	names := []string{"agents", "artifacts", "automation", "connectors", "execution", "interaction", "sourcecontrol", "workflowcatalog", "workitems", "workspace"}
	capabilities := make([]Capability, 0, len(names))
	for _, name := range names {
		capabilities = append(capabilities, Capability{Name: name, Root: name, Status: "planned"})
	}
	return CapabilityGraph{
		SchemaVersion:        SchemaVersion,
		Status:               "approved",
		CompletedPhase:       1,
		DecisionDependencies: []string{"MM-1", "MM-2", "MM-3", "MM-4", "MM-5", "MM-6", "MM-7"},
		ModuleRoot:           "internal/modules",
		AppRoot:              "internal/app",
		PlatformRoot:         "internal/platform",
		Restrictions: BoundaryRestrictions{
			ServeCompositionOnly:     true,
			NamedAppsPublicRootsOnly: true,
			NamedAppsOwnPortsOnly:    true,
			ModulesRejectLegacyTypes: true,
		},
		ExternalImports: ExternalImportPolicy{
			CoreAllowedPrefixes:        []string{},
			AdapterAllowedPrefixes:     []string{},
			PlatformAllowedPrefixes:    []string{},
			CoreDeniedStandardPrefixes: []string{"database/sql", "net/http", "net/rpc", "os", "plugin", "syscall"},
		},
		Capabilities:       capabilities,
		AggregateOwnership: validAggregateOwnership(),
		LegacyPaths: []LegacyPath{{
			Path: "internal/domain", Owner: "test", RemovalIssue: "MM-TEST", ExpiresAfterPhase: 2,
		}},
	}
}

func validAggregateOwnership() []AggregateOwnership {
	identities := approvedAggregateOwnership()
	records := make([]string, 0, len(identities))
	for record := range identities {
		records = append(records, record)
	}
	slices.Sort(records)
	values := make([]AggregateOwnership, 0, len(records))
	for _, record := range records {
		identity := identities[record]
		values = append(values, AggregateOwnership{
			Record: record, Owner: identity.owner, Mechanism: identity.mechanism,
			Discriminator: identity.discriminator, CrossCapabilityRule: "test contract",
		})
	}
	return values
}

func TestCapabilityGraphPinsApprovedOwnershipContract(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CapabilityGraph)
		want   string
	}{
		{name: "status downgrade", mutate: func(graph *CapabilityGraph) { graph.Status = "proposed" }, want: "status must be approved"},
		{name: "capability removed", mutate: func(graph *CapabilityGraph) { graph.Capabilities = graph.Capabilities[1:] }, want: "exact ten approved capabilities"},
		{name: "capability root changed", mutate: func(graph *CapabilityGraph) { graph.Capabilities[0].Root = "renamed" }, want: "root:"},
		{name: "ownership row removed", mutate: func(graph *CapabilityGraph) { graph.AggregateOwnership = graph.AggregateOwnership[1:] }, want: "exact approved aggregate-owner matrix"},
		{name: "ownership changed", mutate: func(graph *CapabilityGraph) { graph.AggregateOwnership[0].Owner = "workspace" }, want: "drifted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			graph := validGraph()
			tt.mutate(&graph)
			if err := graph.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestCapabilityGraphRejectsExpiredLegacyPath(t *testing.T) {
	graph := validGraph()
	graph.CompletedPhase = graph.LegacyPaths[0].ExpiresAfterPhase
	if err := graph.Validate(); err == nil || !strings.Contains(err.Error(), "expired after Phase") {
		t.Fatalf("Validate error = %v, want completed-phase expiry rejection", err)
	}
}

func TestCapabilityGraphAllowsZeroLegacyPathsAtMigrationCompletion(t *testing.T) {
	graph := validGraph()
	graph.CompletedPhase = 7
	graph.LegacyPaths = []LegacyPath{}
	if err := graph.Validate(); err != nil {
		t.Fatalf("Validate completed graph with no legacy paths: %v", err)
	}
}

func validDurablePolicy() *DurableEventPolicy {
	return &DurableEventPolicy{
		IdempotencyKey: "workspace:event", ActorScope: "execution", MaxHops: 8, ReentryPolicy: "reject",
	}
}

func scanBaseline(allowed []string) Baseline {
	decisions := make([]DecisionBaseline, 0, 7)
	for _, id := range []string{"MM-1", "MM-2", "MM-3", "MM-4", "MM-5", "MM-6", "MM-7"} {
		decisions = append(decisions, DecisionBaseline{
			ID: id, Status: "approved", Owner: "test", Rationale: "test", ReviewedBy: "test", ReviewedAt: "2026-07-15",
		})
	}
	return Baseline{
		Ratchets: RatchetBaseline{CompositeStore: CompositeStoreRatchet{
			MaxProductionFiles:        len(allowed),
			MaxOutsideComposition:     len(allowed),
			CompositionPrefixes:       []string{"internal/cli/serve/", "internal/infra/", "internal/store/"},
			AllowedProductionFileUses: allowed,
		}},
		Decisions: decisions,
	}
}

func writeGoFile(t *testing.T, root, relative, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func containsViolation(values []string, fragment string) bool {
	for _, value := range values {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}
