package archtest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckedInDirectWriteInventoryStrictCounts(t *testing.T) {
	inventory, err := LoadDirectWriteInventory(filepath.Join("testdata", "direct-writes.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Writes) != 71 {
		t.Fatalf("direct-write rows = %d, want current source-backed ratchet of 71", len(inventory.Writes))
	}
	totalSites := 0
	for _, use := range inventory.Writes {
		totalSites += use.Count
	}
	if totalSites != 85 {
		t.Fatalf("direct-write sites = %d, want current source-backed ratchet of 85", totalSites)
	}
	if err := inventory.ValidateCompletedPhase(7); err != nil {
		t.Fatalf("checked-in inventory is not ready for Phase 7 completion: %v", err)
	}
}

func TestSnapshotDirectWritesTypeResolvesStoreInterface(t *testing.T) {
	root := directWriteFixture(t)
	matrix := directWriteTestMatrix()
	uses, err := SnapshotDirectWrites(root, matrix, directWriteTestInventory(matrix))
	if err != nil {
		t.Fatal(err)
	}
	if len(uses) != 1 {
		t.Fatalf("direct writes = %+v, want one type-resolved write", uses)
	}
	use := uses[0]
	if use.File != "internal/cli/write.go" || use.Method != "Create" || use.AggregateOwner != "workspace" ||
		!strings.HasSuffix(use.Receiver, "/internal/store.WorkspaceStore") {
		t.Fatalf("direct write = %+v, want WorkspaceStore.Create owned by workspace", use)
	}
	if use.Count != 1 {
		t.Fatalf("direct write count = %d, want one source call across two profiles", use.Count)
	}
}

func TestSnapshotDirectWritesAcceptsExactAdapterFile(t *testing.T) {
	root := directWriteFixture(t)
	writeGoFile(t, root, "internal/cli/other.go", `package cli
import "github.com/tysonthomas9/loomcli/internal/store"
func otherWrite(s workspaceowner.WorkspaceStore) error { return s.Create("outside-exact-adapter") }
`)
	matrix := oneDirectWriteProfile()
	inventory := directWriteTestInventory(matrix)
	inventory.AdapterRoots = []string{"internal/cli/write.go"}
	uses, err := SnapshotDirectWrites(root, matrix, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if len(uses) != 1 || uses[0].File != "internal/cli/write.go" || uses[0].Method != "Create" {
		t.Fatalf("direct writes = %+v, want only the exact adapter file", uses)
	}
}

func TestDirectWriteRatchetRejectsAdditionAndStaleEntry(t *testing.T) {
	root := directWriteFixture(t)
	matrix := directWriteTestMatrix()
	actual, err := SnapshotDirectWrites(root, matrix, directWriteTestInventory(matrix))
	if err != nil {
		t.Fatal(err)
	}
	inventory := directWriteTestInventory(matrix)
	_, violations, err := CheckDirectWrites(root, matrix, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "new direct persistence write") {
		t.Fatalf("addition violations = %v", violations)
	}

	if err := os.Remove(filepath.Join(root, "internal", "cli", "write.go")); err != nil {
		t.Fatal(err)
	}
	inventory.Writes = actual
	_, violations, err = CheckDirectWrites(root, matrix, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "stale direct-write baseline entry") {
		t.Fatalf("removal violations = %v", violations)
	}
}

func TestSnapshotDirectWritesRejectsUnclassifiedStoreMethods(t *testing.T) {
	root := t.TempDir()
	writeDirectWriteModule(t, root)
	writeGoFile(t, root, "internal/store/store.go", `package workspaceowner
type WorkspaceStore interface { Save(string) error; ApproveVersion(string) error }
`)
	writeGoFile(t, root, "internal/cli/write.go", `package cli
import "github.com/tysonthomas9/loomcli/internal/store"
func write(s workspaceowner.WorkspaceStore) error {
	if err := s.Save("one"); err != nil { return err }
	return s.ApproveVersion("two")
}
`)

	matrix := oneDirectWriteProfile()
	_, err := SnapshotDirectWrites(root, matrix, directWriteTestInventory(matrix))
	if err == nil {
		t.Fatal("expected unclassified persistence methods to fail closed")
	}
	for _, want := range []string{"WorkspaceStore.Save", "WorkspaceStore.ApproveVersion", "profiles: untagged", "read-only or mutating"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err, want)
		}
	}
}

func TestSnapshotDirectWritesIncludesDirectFleetDBClientMutation(t *testing.T) {
	root := t.TempDir()
	writeDirectWriteModule(t, root)
	writeGoFile(t, root, "internal/infra/fleetdb/client.go", `package fleetdb
type Client struct{}
func (c *Client) Create(string) error { return nil }
`)
	writeGoFile(t, root, "internal/cli/write.go", `package cli
import "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
func write(c *fleetdb.Client) error { return c.Create("one") }
`)

	matrix := oneDirectWriteProfile()
	inventory := directWriteTestInventory(matrix)
	inventory.PersistencePackages = append(inventory.PersistencePackages, PersistencePackage{
		Path: modulePath + "/internal/infra/fleetdb", ReceiverNames: []string{"Client"}, ReceiverSuffixes: []string{},
	})
	inventory.MethodSets = append(inventory.MethodSets, PersistenceMethodSet{Name: "fleetdb", ReadOnly: []string{}, Mutating: []string{"Create"}})
	inventory.ReceiverSurfaces = append([]PersistenceReceiverSurface{{
		Receiver: "*" + modulePath + "/internal/infra/fleetdb.Client", Package: modulePath + "/internal/infra/fleetdb",
		MethodSet: "fleetdb", CapabilityOwner: "workspace",
	}}, inventory.ReceiverSurfaces...)
	uses, err := SnapshotDirectWrites(root, matrix, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if len(uses) != 1 {
		t.Fatalf("direct writes = %+v, want one FleetDB Client mutation", uses)
	}
	use := uses[0]
	if use.File != "internal/cli/write.go" || use.Method != "Create" || use.Count != 1 ||
		!strings.HasSuffix(use.Receiver, "/internal/infra/fleetdb.Client") {
		t.Fatalf("direct write = %+v, want direct FleetDB Client.Create", use)
	}
}

func TestSnapshotDirectWritesCountsAssignedAndCallbackMethodValues(t *testing.T) {
	root := directWriteFixture(t)
	writeGoFile(t, root, "internal/cli/write.go", `package cli
import "github.com/tysonthomas9/loomcli/internal/store"
func consume(func(string) error) {}
func refs(s workspaceowner.WorkspaceStore) {
	assigned := s.Create
	_ = assigned
	consume(s.Create)
}
`)
	matrix := oneDirectWriteProfile()
	uses, err := SnapshotDirectWrites(root, matrix, directWriteTestInventory(matrix))
	if err != nil {
		t.Fatal(err)
	}
	if len(uses) != 1 || uses[0].Method != "Create" || uses[0].Count != 2 {
		t.Fatalf("direct writes = %+v, want two Create method references", uses)
	}
}

func TestSnapshotDirectWritesAllowsOwnerCoreThroughOwnDeclaredPortOnly(t *testing.T) {
	root := t.TempDir()
	writeDirectWriteModule(t, root)
	writeGoFile(t, root, "internal/modules/workflowcatalog/ports.go", `package workflowcatalog
type VersionLifecycleStore interface { ApproveVersion() error }
`)
	writeGoFile(t, root, "internal/modules/workflowcatalog/service.go", `package workflowcatalog
func execute(port VersionLifecycleStore) error { return port.ApproveVersion() }
`)
	writeGoFile(t, root, "internal/modules/workflowcatalog/fleetdb/adapter.go", `package fleetdb
import "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
func apply(port workflowcatalog.VersionLifecycleStore) error { return port.ApproveVersion() }
`)

	matrix := oneDirectWriteProfile()
	inventory := DirectWriteInventory{
		AdapterRoots:              []string{"internal/modules"},
		AnalysisProfiles:          directWriteProfileNames(matrix),
		CandidateReceiverSuffixes: []string{"Repository", "Store"},
		PersistencePackages: []PersistencePackage{{
			Path: modulePath + "/internal/modules/workflowcatalog", ReceiverNames: []string{"VersionLifecycleStore"}, ReceiverSuffixes: []string{},
		}},
		MethodSets: []PersistenceMethodSet{{
			Name: "lifecycle", ReadOnly: []string{}, Mutating: []string{"ApproveVersion"},
		}},
		ReceiverSurfaces: []PersistenceReceiverSurface{{
			Receiver: modulePath + "/internal/modules/workflowcatalog.VersionLifecycleStore",
			Package:  modulePath + "/internal/modules/workflowcatalog", MethodSet: "lifecycle", CapabilityOwner: "workflowcatalog",
		}},
	}
	uses, err := SnapshotDirectWrites(root, matrix, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if len(uses) != 1 || uses[0].File != "internal/modules/workflowcatalog/fleetdb/adapter.go" || uses[0].Method != "ApproveVersion" {
		t.Fatalf("direct writes = %+v, want only the concrete adapter call retained", uses)
	}
}

func TestSnapshotDirectWritesAllowsNamedWorkflowCoreThroughOwnDeclaredPortOnly(t *testing.T) {
	root := t.TempDir()
	writeDirectWriteModule(t, root)
	writeGoFile(t, root, "internal/app/agentprovisioning/ports.go", `package agentprovisioning
type ProgressStore interface { Save() error }
`)
	writeGoFile(t, root, "internal/app/agentprovisioning/manager.go", `package agentprovisioning
func persist(port ProgressStore) error { return port.Save() }
`)
	writeGoFile(t, root, "internal/app/agentprovisioning/fleetdb/adapter.go", `package fleetdb
import "github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
func persist(port agentprovisioning.ProgressStore) error { return port.Save() }
`)

	matrix := oneDirectWriteProfile()
	inventory := DirectWriteInventory{
		AdapterRoots:              []string{"internal/app"},
		AnalysisProfiles:          directWriteProfileNames(matrix),
		CandidateReceiverSuffixes: []string{"Repository", "Store"},
		PersistencePackages: []PersistencePackage{{
			Path: modulePath + "/internal/app/agentprovisioning", ReceiverNames: []string{"ProgressStore"}, ReceiverSuffixes: []string{},
		}},
		MethodSets: []PersistenceMethodSet{{
			Name: "progress", ReadOnly: []string{}, Mutating: []string{"Save"},
		}},
		ReceiverSurfaces: []PersistenceReceiverSurface{{
			Receiver: modulePath + "/internal/app/agentprovisioning.ProgressStore",
			Package:  modulePath + "/internal/app/agentprovisioning", MethodSet: "progress", CapabilityOwner: "named_application_workflow",
		}},
	}
	uses, err := SnapshotDirectWrites(root, matrix, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if len(uses) != 1 || uses[0].File != "internal/app/agentprovisioning/fleetdb/adapter.go" || uses[0].Method != "Save" {
		t.Fatalf("direct writes = %+v, want only the concrete workflow adapter call retained", uses)
	}
}

func TestSnapshotDirectWritesIncludesPackageLevelPersistenceHelper(t *testing.T) {
	root := t.TempDir()
	writeDirectWriteModule(t, root)
	writeGoFile(t, root, "internal/store/store.go", `package workspaceowner
type WorkspaceStore interface { Create(string) error }
func UploadContentArtifact(s WorkspaceStore) error { return s.Create("artifact") }
`)
	writeGoFile(t, root, "internal/cli/write.go", `package cli
import "github.com/tysonthomas9/loomcli/internal/store"
func consume(func(workspaceowner.WorkspaceStore) error) {}
func write(s workspaceowner.WorkspaceStore) error {
	consume(workspaceowner.UploadContentArtifact)
	return workspaceowner.UploadContentArtifact(s)
}
`)

	matrix := oneDirectWriteProfile()
	inventory := directWriteTestInventory(matrix)
	inventory.FunctionSurfaces = []PersistenceFunctionSurface{{
		Package: modulePath + "/internal/store", Function: "UploadContentArtifact",
		Access: "mutating", CapabilityOwner: "artifacts",
	}}
	uses, err := SnapshotDirectWrites(root, matrix, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if len(uses) != 1 {
		t.Fatalf("direct writes = %+v, want one package-level persistence helper", uses)
	}
	use := uses[0]
	if use.File != "internal/cli/write.go" || use.Receiver != modulePath+"/internal/store" ||
		use.Method != "UploadContentArtifact" || use.Count != 2 || use.AggregateOwner != "artifacts" {
		t.Fatalf("direct write = %+v, want classified workspaceowner.UploadContentArtifact helper", use)
	}
}

func TestSnapshotDirectWritesRejectsUnclassifiedPackageLevelPersistenceHelper(t *testing.T) {
	root := t.TempDir()
	writeDirectWriteModule(t, root)
	writeGoFile(t, root, "internal/store/store.go", `package workspaceowner
type WorkspaceStore interface { Create(string) error }
func UploadContentArtifact(s WorkspaceStore) error { return s.Create("artifact") }
`)
	writeGoFile(t, root, "internal/cli/write.go", `package cli
import "github.com/tysonthomas9/loomcli/internal/store"
func write(s workspaceowner.WorkspaceStore) error { return workspaceowner.UploadContentArtifact(s) }
`)

	_, err := SnapshotDirectWrites(root, oneDirectWriteProfile(), directWriteTestInventory(oneDirectWriteProfile()))
	if err == nil || !strings.Contains(err.Error(), "unclassified persistence package function "+modulePath+"/internal/store.UploadContentArtifact") {
		t.Fatalf("error = %v, want package-level persistence helper to fail closed", err)
	}
}

func TestSnapshotDirectWritesRejectsDotImportedPersistencePackage(t *testing.T) {
	root := t.TempDir()
	writeDirectWriteModule(t, root)
	writeGoFile(t, root, "internal/store/store.go", `package workspaceowner
type WorkspaceStore interface { Create(string) error }
func UploadContentArtifact(s WorkspaceStore) error { return s.Create("artifact") }
`)
	writeGoFile(t, root, "internal/cli/write.go", `package cli
import . "github.com/tysonthomas9/loomcli/internal/store"
func write(s WorkspaceStore) error { return UploadContentArtifact(s) }
`)

	_, err := SnapshotDirectWrites(root, oneDirectWriteProfile(), directWriteTestInventory(oneDirectWriteProfile()))
	if err == nil || !strings.Contains(err.Error(), "dot import of persistence package "+modulePath+"/internal/store") {
		t.Fatalf("error = %v, want persistence dot import to fail closed", err)
	}
}

func TestSnapshotDirectWritesRejectsUnknownCompositeStoreMethod(t *testing.T) {
	root := t.TempDir()
	writeDirectWriteModule(t, root)
	writeGoFile(t, root, "internal/store/store.go", `package workspaceowner
type Store interface { Workspaces() WorkspaceStore; Save() error }
type WorkspaceStore interface { Create(string) error }
`)
	writeGoFile(t, root, "internal/cli/write.go", `package cli
import "github.com/tysonthomas9/loomcli/internal/store"
func write(s workspaceowner.Store) { _ = s.Workspaces(); _ = s.Save }
`)
	matrix := oneDirectWriteProfile()
	inventory := directWriteTestInventory(matrix)
	inventory.MethodSets = append([]PersistenceMethodSet{{Name: "composite", ReadOnly: []string{"Workspaces"}, Mutating: []string{}}}, inventory.MethodSets...)
	inventory.ReceiverSurfaces = append([]PersistenceReceiverSurface{{
		Receiver: modulePath + "/internal/store.Store", Package: modulePath + "/internal/store",
		MethodSet: "composite", CapabilityOwner: "workspace",
	}}, inventory.ReceiverSurfaces...)
	_, err := SnapshotDirectWrites(root, matrix, inventory)
	if err == nil || !strings.Contains(err.Error(), "unclassified persistence method "+modulePath+"/internal/store.Store.Save") {
		t.Fatalf("error = %v, want default-deny composite Store method", err)
	}
}

func TestSnapshotDirectWritesRejectsUndeclaredReceiverSurface(t *testing.T) {
	root := t.TempDir()
	writeDirectWriteModule(t, root)
	writeGoFile(t, root, "internal/persistence/store.go", `package persistence
type Store struct{}
type OtherStore struct{}
func (*Store) Get() error { return nil }
func (*OtherStore) Create() error { return nil }
`)
	writeGoFile(t, root, "internal/cli/write.go", `package cli
import "github.com/tysonthomas9/loomcli/internal/persistence"
func write(s *persistence.OtherStore) error { return s.Create() }
`)
	matrix := oneDirectWriteProfile()
	inventory := DirectWriteInventory{
		AdapterRoots: []string{"internal/cli"}, AnalysisProfiles: directWriteProfileNames(matrix),
		CandidateReceiverSuffixes: []string{"Repository", "Store"},
		PersistencePackages: []PersistencePackage{{
			Path: modulePath + "/internal/persistence", ReceiverNames: []string{"Store"}, ReceiverSuffixes: []string{},
		}},
		MethodSets: []PersistenceMethodSet{{Name: "store", ReadOnly: []string{"Get"}, Mutating: []string{}}},
		ReceiverSurfaces: []PersistenceReceiverSurface{{
			Receiver: "*" + modulePath + "/internal/persistence.Store", Package: modulePath + "/internal/persistence",
			MethodSet: "store", CapabilityOwner: "workspace",
		}},
	}
	_, err := SnapshotDirectWrites(root, matrix, inventory)
	if err == nil || !strings.Contains(err.Error(), "undeclared persistence receiver surface") {
		t.Fatalf("error = %v, want undeclared persistence surface failure", err)
	}
}

func TestSnapshotDirectWritesRejectsUndeclaredPersistenceSubpackageImport(t *testing.T) {
	root := t.TempDir()
	writeDirectWriteModule(t, root)
	writeGoFile(t, root, "internal/persistence/store.go", `package persistence
type Store struct{}
func (*Store) Get() error { return nil }
`)
	writeGoFile(t, root, "internal/persistence/rogue/rogue.go", "package rogue\n")
	writeGoFile(t, root, "internal/cli/read.go", `package cli
import _ "github.com/tysonthomas9/loomcli/internal/persistence/rogue"
`)
	matrix := oneDirectWriteProfile()
	inventory := DirectWriteInventory{
		AdapterRoots: []string{"internal/cli"}, AnalysisProfiles: directWriteProfileNames(matrix),
		PersistencePackages: []PersistencePackage{{
			Path: modulePath + "/internal/persistence", ReceiverNames: []string{"Store"}, ReceiverSuffixes: []string{},
			GuardSubpackages: true,
		}},
		MethodSets: []PersistenceMethodSet{{Name: "store", ReadOnly: []string{"Get"}, Mutating: []string{}}},
		ReceiverSurfaces: []PersistenceReceiverSurface{{
			Receiver: "*" + modulePath + "/internal/persistence.Store", Package: modulePath + "/internal/persistence",
			MethodSet: "store", CapabilityOwner: "workspace",
		}},
	}
	_, err := SnapshotDirectWrites(root, matrix, inventory)
	if err == nil || !strings.Contains(err.Error(), "undeclared persistence package import "+modulePath+"/internal/persistence/rogue") {
		t.Fatalf("error = %v, want undeclared persistence subpackage import failure", err)
	}
}

func TestSnapshotDirectWritesEnforcesRequiredSourceSentinel(t *testing.T) {
	root := directWriteFixture(t)
	writeGoFile(t, root, "internal/cli/sentinel.go", "//go:build selectedtag\n\npackage cli\n")
	matrix := AnalysisMatrix{Tagged: []AnalysisProfile{{
		Name: "wrong-tag", GOOS: "linux", GOARCH: "amd64", Tags: []string{"othertag"},
		RequiredFiles: []string{"internal/cli/sentinel.go"},
	}}}
	_, err := SnapshotDirectWrites(root, matrix, directWriteTestInventory(matrix))
	if err == nil || !strings.Contains(err.Error(), "did not select required source internal/cli/sentinel.go") {
		t.Fatalf("error = %v, want missing required-source failure", err)
	}
}

func TestDirectWritePersistencePolicyRejectsUnassignedOwner(t *testing.T) {
	inventory := directWriteTestInventory(oneDirectWriteProfile())
	inventory.ReceiverSurfaces[0].CapabilityOwner = "unassigned_legacy"
	if err := inventory.validatePersistencePolicy(); err == nil || !strings.Contains(err.Error(), "explicit capability owner") {
		t.Fatalf("error = %v, want unassigned owner rejection", err)
	}
}

func TestDirectWritePersistencePolicyAllowsExactReceiverWithoutClassifyingWholePackage(t *testing.T) {
	inventory := directWriteTestInventory(oneDirectWriteProfile())
	const packagePath = modulePath + "/internal/example"
	const receiver = packagePath + ".Journal"
	inventory.MethodSets = append([]PersistenceMethodSet{{
		Name: "projection", ReadOnly: []string{"Workspaces"}, Mutating: []string{},
	}}, inventory.MethodSets...)
	inventory.ReceiverSurfaces = append([]PersistenceReceiverSurface{{
		Receiver: receiver, Package: packagePath,
		MethodSet: "projection", CapabilityOwner: "read_projection",
	}}, inventory.ReceiverSurfaces...)
	if err := inventory.validatePersistencePolicy(); err != nil {
		t.Fatalf("validate narrow candidate surface: %v", err)
	}
	classifier := newPersistenceClassifier(inventory)
	if classifier.isPersistenceFunctionPackage(packagePath) {
		t.Fatal("declaring one narrow candidate receiver classified every package helper as persistence")
	}
	if !classifier.isPersistenceCandidate(packagePath, "Journal") {
		t.Fatal("exact receiver surface was not recognized as a persistence candidate")
	}
	access, owner, ok := classifier.classify(receiver, "Workspaces")
	if !ok || access != persistenceReadOnly || owner != "read_projection" {
		t.Fatalf("candidate receiver classification = (%v, %q, %t), want read-only read_projection", access, owner, ok)
	}
}

func TestPersistencePackageReceiverPolicyDoesNotClassifyUnlistedFunctions(t *testing.T) {
	inventory := directWriteTestInventory(oneDirectWriteProfile())
	const packagePath = modulePath + "/internal/example"
	inventory.PersistencePackages = append(inventory.PersistencePackages, PersistencePackage{
		Path: packagePath, ReceiverNames: []string{"Journal"}, ReceiverSuffixes: []string{},
	})
	classifier := newPersistenceClassifier(inventory)
	if classifier.isPersistenceFunctionPackage(packagePath) {
		t.Fatal("a receiver-only persistence package classified unrelated package functions")
	}
	if !classifier.isPersistenceCandidate(packagePath, "Journal") {
		t.Fatal("declared receiver was not recognized as a persistence candidate")
	}
}

func TestSnapshotDirectWritesExactReceiverFailsClosedWithoutClassifyingPackageFunctions(t *testing.T) {
	root := t.TempDir()
	writeDirectWriteModule(t, root)
	writeGoFile(t, root, "internal/example/journal.go", `package example
type Journal struct{}
func New() *Journal { return &Journal{} }
func (journal *Journal) Persist() error { return nil }
func (journal *Journal) UndeclaredWrite() error { return nil }
`)
	writeGoFile(t, root, "internal/cli/write.go", `package cli
import "github.com/tysonthomas9/loomcli/internal/example"
func write(journal *example.Journal) error {
	_ = example.New()
	return journal.UndeclaredWrite()
}
`)
	matrix := oneDirectWriteProfile()
	inventory := directWriteTestInventory(matrix)
	inventory.MethodSets = append([]PersistenceMethodSet{{
		Name: "journal", ReadOnly: []string{}, Mutating: []string{"Persist"},
	}}, inventory.MethodSets...)
	inventory.ReceiverSurfaces = append([]PersistenceReceiverSurface{{
		Receiver: "*" + modulePath + "/internal/example.Journal",
		Package:  modulePath + "/internal/example", MethodSet: "journal", CapabilityOwner: "workspace",
	}}, inventory.ReceiverSurfaces...)

	_, err := SnapshotDirectWrites(root, matrix, inventory)
	if err == nil || !strings.Contains(err.Error(), "Journal.UndeclaredWrite") {
		t.Fatalf("error = %v, want exact receiver's undeclared method to fail closed", err)
	}
	if strings.Contains(err.Error(), "example.New") {
		t.Fatalf("error = %v, exact receiver must not classify unrelated package functions", err)
	}
}

func TestDirectWriteInventoryRejectsExpiredRowsForCompletedPhase(t *testing.T) {
	inventory := directWriteTestInventory(oneDirectWriteProfile())
	inventory.Writes = []DirectWriteUse{{
		File: "internal/cli/write.go", Receiver: modulePath + "/internal/store.WorkspaceStore",
		Method: "Create", Count: 1, AggregateOwner: "workspace", Disposition: directWriteDispositionTransitional, ExpiresAfterPhase: 2,
	}}
	if err := inventory.ValidateCompletedPhase(2); err == nil || !strings.Contains(err.Error(), "expired after Phase 2") {
		t.Fatalf("ValidateCompletedPhase error = %v, want expired-row rejection", err)
	}
}

func TestDirectWriteInventoryAllowsDeclaredOwnerAdapterAtPhase7(t *testing.T) {
	inventory := directWriteTestInventory(oneDirectWriteProfile())
	inventory.OwnerAdapters = []DirectWriteOwnerAdapter{{
		Path: "internal/cli/workspaceadapter", AggregateOwner: "workspace",
	}}
	inventory.Writes = []DirectWriteUse{{
		File: "internal/cli/workspaceadapter/write.go", Receiver: modulePath + "/internal/store.WorkspaceStore",
		Method: "Create", Count: 1, AggregateOwner: "workspace", Disposition: directWriteDispositionOwnerAdapter,
	}}
	if err := inventory.validateOwnerAdapters(); err != nil {
		t.Fatal(err)
	}
	if err := inventory.validateWrites(); err != nil {
		t.Fatal(err)
	}
	if err := inventory.ValidateCompletedPhase(7); err != nil {
		t.Fatalf("durable owner adapter rejected at Phase 7: %v", err)
	}
}

func TestDirectWriteInventoryRejectsForgedOwnerAdapterDisposition(t *testing.T) {
	inventory := directWriteTestInventory(oneDirectWriteProfile())
	inventory.OwnerAdapters = []DirectWriteOwnerAdapter{{
		Path: "internal/cli/workspaceadapter", AggregateOwner: "workspace",
	}}
	inventory.Writes = []DirectWriteUse{{
		File: "internal/cli/rogue/write.go", Receiver: modulePath + "/internal/store.WorkspaceStore",
		Method: "Create", Count: 1, AggregateOwner: "workspace", Disposition: directWriteDispositionOwnerAdapter,
	}}
	if err := inventory.validateWrites(); err == nil || !strings.Contains(err.Error(), "is not declared by owner_adapters") {
		t.Fatalf("validateWrites error = %v, want undeclared owner-adapter rejection", err)
	}
}

func TestDirectWriteInventoryRejectsOwnerAdapterOwnerMismatch(t *testing.T) {
	inventory := directWriteTestInventory(oneDirectWriteProfile())
	inventory.OwnerAdapters = []DirectWriteOwnerAdapter{{
		Path: "internal/cli/workspaceadapter", AggregateOwner: "interaction",
	}}
	inventory.Writes = []DirectWriteUse{{
		File: "internal/cli/workspaceadapter/write.go", Receiver: modulePath + "/internal/store.WorkspaceStore",
		Method: "Create", Count: 1, AggregateOwner: "workspace", Disposition: directWriteDispositionOwnerAdapter,
	}}
	if err := inventory.validateWrites(); err == nil || !strings.Contains(err.Error(), "is not declared by owner_adapters") {
		t.Fatalf("validateWrites error = %v, want owner mismatch rejection", err)
	}
}

func TestDirectWriteRowsPreserveLegacyDriverPhase6Expiry(t *testing.T) {
	inventory := DirectWriteInventory{OwnerAdapters: []DirectWriteOwnerAdapter{{Path: "internal/app/serve/agents.go", AggregateOwner: "agents"}}}
	rows := directWriteRows(map[directWriteCountKey]int{
		{file: "internal/driver/register.go", receiver: "driver.Store", method: "Create", owner: "workflowcatalog"}: 1,
		{file: "internal/app/serve/agents.go", receiver: "agents.Store", method: "Create", owner: "agents"}:         1,
	}, inventory)
	if len(rows) != 2 {
		t.Fatalf("direct-write rows = %+v, want 2", rows)
	}
	expiryByFile := make(map[string]int, len(rows))
	for _, row := range rows {
		expiryByFile[row.File] = row.ExpiresAfterPhase
	}
	if got := expiryByFile["internal/driver/register.go"]; got != legacyDriverDirectWriteExpiresAfterPhase {
		t.Fatalf("driver expiry = %d, want %d", got, legacyDriverDirectWriteExpiresAfterPhase)
	}
	if got := expiryByFile["internal/app/serve/agents.go"]; got != 0 {
		t.Fatalf("owner adapter expiry = %d, want 0", got)
	}
}

func TestLegacyDriverDirectWriteRatchetRejectsDrift(t *testing.T) {
	root := t.TempDir()
	writeDirectWriteModule(t, root)
	writeGoFile(t, root, "internal/store/store.go", `package workspaceowner
type WorkspaceStore interface { Create(string) error }
`)
	writeGoFile(t, root, "internal/driver/write.go", `package driver
import "github.com/tysonthomas9/loomcli/internal/store"
func write(s workspaceowner.WorkspaceStore) error { return s.Create("one") }
`)
	writeGoFile(t, root, "internal/cli/read.go", "package cli\n")

	matrix := oneDirectWriteProfile()
	inventory := directWriteTestInventory(matrix)
	inventory.AdapterRoots = append(inventory.AdapterRoots, "internal/driver")
	inventory.ReceiverSurfaces[0].CapabilityOwner = "execution"
	uses, err := snapshotDirectWritesAtRoots(root, matrix, inventory, []string{"internal/driver"})
	if err != nil {
		t.Fatal(err)
	}
	inventory.Writes = uses
	rows, sites, digest := directWriteDigest(uses)
	inventory.LegacyDriver = &LegacyDirectWriteBaseline{
		Root: "internal/driver", ExpiresAfterPhase: 6,
		Rows: rows, Sites: sites, Digest: digest, Owners: directWriteOwnerBaselines(uses),
	}
	if err := inventory.validateLegacyExecution(); err != nil {
		t.Fatal(err)
	}
	if _, violations, err := CheckDirectWrites(root, matrix, inventory); err != nil || len(violations) != 0 {
		t.Fatalf("matching legacy driver baseline violations=%v err=%v", violations, err)
	}

	writeGoFile(t, root, "internal/driver/write.go", `package driver
import "github.com/tysonthomas9/loomcli/internal/store"
func write(s workspaceowner.WorkspaceStore) error {
	if err := s.Create("one"); err != nil { return err }
	return s.Create("two")
}
`)
	_, violations, err := CheckDirectWrites(root, matrix, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "legacy driver direct-write ratchet changed") {
		t.Fatalf("legacy driver drift violations = %v", violations)
	}
}

func TestDirectWritesWithinRootUsesPathBoundary(t *testing.T) {
	uses := []DirectWriteUse{
		{File: "internal/driver"},
		{File: "internal/driver/nested/write.go"},
		{File: "internal/driverx/write.go"},
		{File: "internal/app/write.go"},
	}

	got := directWritesWithinRoot(uses, "./internal/driver/")
	if len(got) != 2 || got[0].File != "internal/driver" || got[1].File != "internal/driver/nested/write.go" {
		t.Fatalf("direct writes within root = %+v", got)
	}
}

func TestLegacyDriverDirectWriteRatchetExpiresAtPhase6Completion(t *testing.T) {
	inventory := directWriteTestInventory(oneDirectWriteProfile())
	inventory.LegacyDriver = &LegacyDirectWriteBaseline{
		Root: "internal/driver", ExpiresAfterPhase: 6, Rows: 1, Sites: 1,
		Digest: strings.Repeat("a", 64),
		Owners: []LegacyDirectWriteOwnerBaseline{{CapabilityOwner: "execution", Rows: 1, Sites: 1}},
	}
	if err := inventory.validateLegacyExecution(); err != nil {
		t.Fatal(err)
	}
	if err := inventory.ValidateCompletedPhase(5); err != nil {
		t.Fatalf("Phase 5 must retain the bounded legacy baseline: %v", err)
	}
	if err := inventory.ValidateCompletedPhase(6); err == nil || !strings.Contains(err.Error(), "expired after Phase 6") {
		t.Fatalf("Phase 6 completion error = %v, want expiry rejection", err)
	}
}

func TestSnapshotDirectWritesIncludesTaggedOnlyMutation(t *testing.T) {
	root := t.TempDir()
	writeDirectWriteModule(t, root)
	writeGoFile(t, root, "internal/store/store.go", `package workspaceowner
type WorkspaceStore interface { Create(string) error }
`)
	writeGoFile(t, root, "internal/cli/base.go", "package cli\n")
	writeGoFile(t, root, "internal/cli/tagged.go", `//go:build directwritefixture

package cli

import "github.com/tysonthomas9/loomcli/internal/store"

func taggedWrite(s workspaceowner.WorkspaceStore) error { return s.Create("tagged") }
`)
	matrix := AnalysisMatrix{
		Release: []AnalysisProfile{{Name: "untagged", GOOS: "linux", GOARCH: "amd64"}},
		Tagged: []AnalysisProfile{{
			Name: "tagged", GOOS: "linux", GOARCH: "amd64", Tags: []string{"directwritefixture"},
			RequiredFiles: []string{"internal/cli/tagged.go"},
		}},
	}

	uses, err := SnapshotDirectWrites(root, matrix, directWriteTestInventory(matrix))
	if err != nil {
		t.Fatal(err)
	}
	if len(uses) != 1 || uses[0].File != "internal/cli/tagged.go" || uses[0].Count != 1 {
		t.Fatalf("direct writes = %+v, want one tagged-only mutation", uses)
	}
}

func TestGenericMechanismAccessIsDefaultDeny(t *testing.T) {
	root := t.TempDir()
	writeDirectWriteModule(t, root)
	writeGoFile(t, root, "internal/cli/lease.go", `package cli
func consume(any) {}
func use(s interface{ Leases() any; ActionLedger() any; ActionLedgers() any }) {
	leaseAccessor := s.Leases
	_ = leaseAccessor
	consume(s.ActionLedger)
	_ = s.ActionLedgers
}
`)
	violations, err := analyzeProfile(root, AnalysisProfile{
		Name: "generic-mechanism-fixture", GOOS: "linux", GOARCH: "amd64", Enforced: true,
	}, validGraph(), genericMechanismTestPolicies())
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 3 {
		t.Fatalf("generic mechanism violations = %v, want all three method values", violations)
	}
	for _, mechanism := range []string{"Leases", "ActionLedger", "ActionLedgers"} {
		if !containsViolation(violations, "generic "+mechanism+"; use an owner-scoped adapter") {
			t.Fatalf("generic mechanism violations = %v, missing %s method value", violations, mechanism)
		}
	}
}

func TestGenericMechanismAccessAllowsOnlyDeclaredOwnerAdapters(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "mechanism.go", `package fixture
func use(s interface{ Leases() any; ActionLedger() any }) {
	_ = s.Leases()
	_ = s.ActionLedger()
}
`)
	contents, err := os.ReadFile(filepath.Join(root, "mechanism.go"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "mechanism.go", contents, 0)
	if err != nil {
		t.Fatal(err)
	}
	policies := genericMechanismTestPolicies()
	if violations := genericMechanismBoundaryViolations("internal/modules/execution/fleetdb/store.go", parsed, policies); len(violations) != 0 {
		t.Fatalf("execution owner adapter violations = %v, want none", violations)
	}
	violations := genericMechanismBoundaryViolations("internal/modules/workflowcatalog/fleetdb/store.go", parsed, policies)
	if len(violations) != 2 || !containsViolation(violations, "generic Leases") || !containsViolation(violations, "generic ActionLedger") {
		t.Fatalf("wrong-owner adapter violations = %v, want both mechanisms denied", violations)
	}
	violations = genericMechanismBoundaryViolations("internal/modules/execution/internal/core.go", parsed, policies)
	if len(violations) != 2 {
		t.Fatalf("owner core violations = %v, want both mechanisms denied outside adapter", violations)
	}
}

func genericMechanismTestPolicies() []GenericMechanismUse {
	return []GenericMechanismUse{
		{Mechanism: "action_ledger", AllowedAdapterRoots: []string{"internal/modules/execution/fleetdb"}},
		{Mechanism: "lease", AllowedAdapterRoots: []string{
			"internal/modules/agents/fleetdb",
			"internal/modules/execution/fleetdb",
			"internal/modules/interaction/fleetdb",
		}},
	}
}

func directWriteFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeDirectWriteModule(t, root)
	writeGoFile(t, root, "internal/store/store.go", `package workspaceowner
type WorkspaceStore interface { Create(string) error; Get(string) error }
`)
	writeGoFile(t, root, "internal/cli/write.go", `package cli
import "github.com/tysonthomas9/loomcli/internal/store"
func write(s workspaceowner.WorkspaceStore) error { _ = s.Get("one"); return s.Create("two") }
`)
	return root
}

func writeDirectWriteModule(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+modulePath+"\n\ngo 1.25.6\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func oneDirectWriteProfile() AnalysisMatrix {
	return AnalysisMatrix{Release: []AnalysisProfile{{Name: "untagged", GOOS: "linux", GOARCH: "amd64"}}}
}

func directWriteTestMatrix() AnalysisMatrix {
	return AnalysisMatrix{
		Release: []AnalysisProfile{{Name: "untagged", GOOS: "linux", GOARCH: "amd64"}},
		Tagged:  []AnalysisProfile{{Name: "tagged", GOOS: "linux", GOARCH: "amd64", Tags: []string{"directwritefixture"}}},
	}
}

func directWriteTestInventory(matrix AnalysisMatrix) DirectWriteInventory {
	return DirectWriteInventory{
		AdapterRoots:              []string{"internal/cli"},
		AnalysisProfiles:          directWriteProfileNames(matrix),
		CandidateReceiverSuffixes: []string{"Repository", "Store"},
		PersistencePackages: []PersistencePackage{{
			Path: modulePath + "/internal/store", ReceiverNames: []string{}, ReceiverSuffixes: []string{"Store"}, GuardFunctions: true,
		}},
		MethodSets: []PersistenceMethodSet{{Name: "store", ReadOnly: []string{"Get"}, Mutating: []string{"Create"}}},
		ReceiverSurfaces: []PersistenceReceiverSurface{{
			Receiver: modulePath + "/internal/store.WorkspaceStore", Package: modulePath + "/internal/store",
			MethodSet: "store", CapabilityOwner: "workspace",
		}},
	}
}
