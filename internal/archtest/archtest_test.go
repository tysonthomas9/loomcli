package archtest

import (
	"errors"
	"os"
	"path/filepath"
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
	if got, want := len(report.CompositeStoreFiles), 93; got != want {
		t.Fatalf("composite Store file count = %d, want %d; files = %v", got, want, report.CompositeStoreFiles)
	}
	if got, want := len(report.CompositeStoreOutside), 82; got != want {
		t.Fatalf("outside-composition Store file count = %d, want %d", got, want)
	}
	if got, want := len(report.LegacyHandlerImports), 91; got != want {
		t.Fatalf("legacy handler imports = %d, want %d", got, want)
	}
	if got := len(report.ModuleRoots); got != 0 {
		t.Fatalf("module roots = %d, want 0 before decisions are approved", got)
	}
	if got, want := len(report.PendingDecisions), 7; got != want {
		t.Fatalf("pending decisions = %d, want %d", got, want)
	}
}

func TestBaselineRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	contents := `{"schema_version":1,"analyzer_version":"0.1.0","unknown":true}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBaseline(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected strict decode error, got %v", err)
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
		{From: "execution", To: "automation", Kinds: []string{"durable_event"}},
	}
	if err := graph.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilityGraphApprovalIsBlockedDuringBootstrap(t *testing.T) {
	graph := validGraph()
	graph.Status = "approved"
	if err := graph.Validate(); err == nil || !strings.Contains(err.Error(), "cannot approve") {
		t.Fatalf("expected bootstrap approval blocker, got %v", err)
	}
}

func TestCapabilityGraphRequiresEveryMigrationDecision(t *testing.T) {
	graph := validGraph()
	graph.DecisionDependencies = graph.DecisionDependencies[:6]
	if err := graph.Validate(); err == nil || !strings.Contains(err.Error(), "decision dependencies") {
		t.Fatalf("expected decision-dependency error, got %v", err)
	}
}

func TestAnalysisMatrixCannotClaimProfileEnforcement(t *testing.T) {
	matrix, err := LoadAnalysisMatrix(filepath.Join("testdata", "analysis-matrix.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	matrix.Release[0].Enforced = true
	if err := matrix.Validate(); err == nil || !strings.Contains(err.Error(), "cannot be marked enforced") {
		t.Fatalf("expected profile-enforcement error, got %v", err)
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

func TestRatchetAllowsRemoval(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "internal/empty/doc.go", "package empty\n")
	baseline := scanBaseline([]string{"internal/legacy/removed.go"})
	baseline.Ratchets.CompositeStore.MaxProductionFiles = 1
	_, violations, err := scanRepository(root, baseline, validGraph())
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("removing a legacy use must pass, got %v", violations)
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
	_, violations, err := scanRepository(root, scanBaseline(nil), validGraph())
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
	if !containsViolation(violations, "imports undeclared module root unknown") {
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
	if !containsViolation(violations, "imports forbidden internal package") {
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
		Status:               "proposed",
		DecisionDependencies: []string{"MM-1", "MM-2", "MM-3", "MM-4", "MM-5", "MM-6", "MM-7"},
		ModuleRoot:           "internal/modules",
		Capabilities:         capabilities,
	}
}

func scanBaseline(allowed []string) Baseline {
	decisions := make([]DecisionBaseline, 0, 7)
	for _, id := range []string{"MM-1", "MM-2", "MM-3", "MM-4", "MM-5", "MM-6", "MM-7"} {
		decisions = append(decisions, DecisionBaseline{ID: id, Status: "pending", Owner: "test"})
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
