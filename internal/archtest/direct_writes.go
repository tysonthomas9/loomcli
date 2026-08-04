package archtest

import (
	cryptosha256 "crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"golang.org/x/tools/go/packages"
)

type DirectWriteInventory struct {
	SchemaVersion             int                          `yaml:"schema_version"`
	Status                    string                       `yaml:"status"`
	SourceHead                string                       `yaml:"source_head"`
	AdapterRoots              []string                     `yaml:"adapter_roots"`
	OwnerAdapters             []DirectWriteOwnerAdapter    `yaml:"owner_adapters"`
	AnalysisProfiles          []string                     `yaml:"analysis_profiles"`
	ClassificationPolicy      string                       `yaml:"classification_policy"`
	CandidateReceiverSuffixes []string                     `yaml:"candidate_receiver_suffixes"`
	PersistencePackages       []PersistencePackage         `yaml:"persistence_packages"`
	MethodSets                []PersistenceMethodSet       `yaml:"method_sets"`
	ReceiverSurfaces          []PersistenceReceiverSurface `yaml:"receiver_surfaces"`
	FunctionSurfaces          []PersistenceFunctionSurface `yaml:"function_surfaces"`
	Writes                    []DirectWriteUse             `yaml:"writes"`
	GenericMechanisms         []GenericMechanismUse        `yaml:"generic_mechanisms"`
	LegacyDriver              *LegacyDirectWriteBaseline   `yaml:"legacy_driver,omitempty"`
}

// DirectWriteOwnerAdapter declares the narrow composition or infrastructure
// path where one aggregate owner's public port is implemented by concrete
// persistence. These are target-architecture adapters, not migration debt.
// Every other observed write remains transitional and must expire.
type DirectWriteOwnerAdapter struct {
	Path           string `yaml:"path"`
	AggregateOwner string `yaml:"aggregate_owner"`
}

// LegacyDirectWriteBaseline is a strict digest ratchet for a legacy root too
// large to pretend was migrated. Phase 4 uses it for internal/driver: exact
// rows, sites, capability-owner distribution, and a content digest make any
// drift fail without falsely relabeling the package's cross-capability writes
// as Execution-owned. expires_after_phase=6 means the baseline must be gone
// when Phase 6 is marked complete.
type LegacyDirectWriteBaseline struct {
	Root              string                           `yaml:"root"`
	ExpiresAfterPhase int                              `yaml:"expires_after_phase"`
	Rows              int                              `yaml:"rows"`
	Sites             int                              `yaml:"sites"`
	Digest            string                           `yaml:"digest"`
	Owners            []LegacyDirectWriteOwnerBaseline `yaml:"owners"`
}

const legacyDriverDirectWriteExpiresAfterPhase = 6

type LegacyDirectWriteOwnerBaseline struct {
	CapabilityOwner string `yaml:"capability_owner"`
	Rows            int    `yaml:"rows"`
	Sites           int    `yaml:"sites"`
}

type PersistencePackage struct {
	Path             string   `yaml:"path"`
	ReceiverNames    []string `yaml:"receiver_names"`
	ReceiverSuffixes []string `yaml:"receiver_suffixes"`
	GuardSubpackages bool     `yaml:"guard_subpackages,omitempty"`
}

type PersistenceMethodSet struct {
	Name     string   `yaml:"name"`
	ReadOnly []string `yaml:"read_only"`
	Mutating []string `yaml:"mutating"`
}

type PersistenceReceiverSurface struct {
	Receiver        string `yaml:"receiver"`
	Package         string `yaml:"package"`
	MethodSet       string `yaml:"method_set"`
	CapabilityOwner string `yaml:"capability_owner"`
}

// PersistenceFunctionSurface classifies a package-level function exported by a
// declared persistence package. Package helpers need their own surface because
// go/types represents a qualified function (store.Helper) as a package object,
// not as a method selection. Without this policy, a helper can perform writes
// while bypassing the receiver-method ratchet entirely.
type PersistenceFunctionSurface struct {
	Package         string `yaml:"package"`
	Function        string `yaml:"function"`
	Access          string `yaml:"access"`
	CapabilityOwner string `yaml:"capability_owner"`
}

type DirectWriteUse struct {
	File              string `yaml:"file"`
	Receiver          string `yaml:"receiver"`
	Method            string `yaml:"method"`
	Count             int    `yaml:"count"`
	AggregateOwner    string `yaml:"aggregate_owner"`
	Disposition       string `yaml:"disposition"`
	ExpiresAfterPhase int    `yaml:"expires_after_phase,omitempty"`
}

const (
	directWriteDispositionOwnerAdapter = "owner_adapter"
	directWriteDispositionTransitional = "transitional"
)

type GenericMechanismUse struct {
	Mechanism           string   `yaml:"mechanism"`
	DirectUses          int      `yaml:"direct_uses"`
	OwnerMapping        string   `yaml:"owner_mapping"`
	AllowedAdapterRoots []string `yaml:"allowed_adapter_roots"`
	Enforcement         string   `yaml:"enforcement"`
}

func LoadDirectWriteInventory(path string) (DirectWriteInventory, error) {
	var value DirectWriteInventory
	if err := decodeYAML(path, &value); err != nil {
		return DirectWriteInventory{}, fmt.Errorf("decode direct-write inventory: %w", err)
	}
	if err := value.Validate(); err != nil {
		return DirectWriteInventory{}, err
	}
	return value, nil
}

// LoadDirectWriteSnapshotPolicy loads only the scanner policy needed to create
// a fresh direct-write snapshot. Unlike LoadDirectWriteInventory, it does not
// validate the checked-in writes or an expiring legacy baseline: those are the
// stale observations that snapshot generation exists to replace. The scanner
// still fails closed on metadata, persistence-surface classifications, and
// generic-mechanism policy.
func LoadDirectWriteSnapshotPolicy(path string) (DirectWriteInventory, error) {
	var value DirectWriteInventory
	if err := decodeYAML(path, &value); err != nil {
		return DirectWriteInventory{}, fmt.Errorf("decode direct-write snapshot policy: %w", err)
	}
	if err := value.validateMetadata(); err != nil {
		return DirectWriteInventory{}, err
	}
	if err := value.validatePersistencePolicy(); err != nil {
		return DirectWriteInventory{}, err
	}
	if err := value.validateGenericMechanisms(); err != nil {
		return DirectWriteInventory{}, err
	}
	return value, nil
}

func (i DirectWriteInventory) Validate() error {
	if err := i.validateMetadata(); err != nil {
		return err
	}
	if err := i.validatePersistencePolicy(); err != nil {
		return err
	}
	if err := i.validateWrites(); err != nil {
		return err
	}
	if err := i.validateLegacyExecution(); err != nil {
		return err
	}
	return i.validateGenericMechanisms()
}

// ValidateCompletedPhase makes transitional row expiry operational. A durable
// owner adapter has no phase expiry because it is the required implementation
// side of a public owner port; every other direct write must disappear by its
// declared final phase.
func (i DirectWriteInventory) ValidateCompletedPhase(completedPhase int) error {
	if completedPhase < 1 || completedPhase > 7 {
		return fmt.Errorf("direct-write completed phase must be between 1 and 7, got %d", completedPhase)
	}
	for _, use := range i.Writes {
		if use.Disposition == directWriteDispositionTransitional && use.ExpiresAfterPhase <= completedPhase {
			return fmt.Errorf("direct-write row %s.%s in %s expired after Phase %d but completed_phase is %d", use.Receiver, use.Method, use.File, use.ExpiresAfterPhase, completedPhase)
		}
	}
	if baseline := i.LegacyDriver; baseline != nil && baseline.ExpiresAfterPhase <= completedPhase {
		return fmt.Errorf("legacy direct-write root %s expired after Phase %d but completed_phase is %d", baseline.Root, baseline.ExpiresAfterPhase, completedPhase)
	}
	return nil
}

func (i DirectWriteInventory) validateLegacyExecution() error {
	baseline := i.LegacyDriver
	if baseline == nil {
		return nil
	}
	if baseline.Root != "internal/driver" || baseline.ExpiresAfterPhase != legacyDriverDirectWriteExpiresAfterPhase {
		return fmt.Errorf("legacy driver direct-write baseline must freeze internal/driver until Phase 6 completion")
	}
	if baseline.Rows <= 0 || baseline.Sites <= 0 || len(baseline.Digest) != cryptosha256.Size*2 {
		return fmt.Errorf("legacy driver direct-write baseline requires positive rows/sites and a sha256 digest")
	}
	if _, err := hex.DecodeString(baseline.Digest); err != nil {
		return fmt.Errorf("legacy driver direct-write digest: %w", err)
	}
	ownerNames := make([]string, 0, len(baseline.Owners))
	totalRows, totalSites := 0, 0
	for _, owner := range baseline.Owners {
		if !validPersistenceOwner(owner.CapabilityOwner) || owner.Rows <= 0 || owner.Sites <= 0 {
			return fmt.Errorf("legacy driver owner baseline requires a valid owner and positive rows/sites: %+v", owner)
		}
		ownerNames = append(ownerNames, owner.CapabilityOwner)
		totalRows += owner.Rows
		totalSites += owner.Sites
	}
	if err := validateSortedUnique("legacy driver capability owner", ownerNames); err != nil {
		return err
	}
	if !slices.Contains(ownerNames, "execution") {
		return fmt.Errorf("legacy driver owner baseline must include execution")
	}
	if totalRows != baseline.Rows || totalSites != baseline.Sites {
		return fmt.Errorf("legacy driver owner totals rows=%d sites=%d do not match baseline rows=%d sites=%d", totalRows, totalSites, baseline.Rows, baseline.Sites)
	}
	return nil
}

func (i DirectWriteInventory) validateMetadata() error {
	if i.SchemaVersion != SchemaVersion {
		return fmt.Errorf("direct-write inventory schema_version: got %d, want %d", i.SchemaVersion, SchemaVersion)
	}
	if i.Status != "complete" {
		return fmt.Errorf("direct-write inventory status %q is unsupported", i.Status)
	}
	if !fullSHA.MatchString(i.SourceHead) {
		return errors.New("direct-write inventory source_head must be a full lowercase SHA")
	}
	if err := validateSortedUnique("direct-write adapter root", i.AdapterRoots); err != nil {
		return err
	}
	wantRoots := []string{
		"internal/app",
		"internal/cli",
		"internal/driver",
		"internal/infra/agentsbootstrapstore",
		"internal/infra/connectorscatalog",
		"internal/infra/connectorsrotation",
		"internal/infra/sessionstoreadapter",
		"internal/infra/stackstoreadapter",
		"internal/infra/usageprojection",
		"internal/infra/workspacecatalog",
		"internal/modules",
		"internal/webui/handlers",
	}
	if !slices.Equal(i.AdapterRoots, wantRoots) {
		return fmt.Errorf("direct-write adapter roots: got %v, want %v", i.AdapterRoots, wantRoots)
	}
	if err := i.validateOwnerAdapters(); err != nil {
		return err
	}
	if err := validateSortedUnique("direct-write analysis profile", i.AnalysisProfiles); err != nil {
		return err
	}
	if len(i.AnalysisProfiles) == 0 {
		return errors.New("direct-write inventory requires at least one analysis profile")
	}
	if i.ClassificationPolicy != "default-deny" {
		return fmt.Errorf("direct-write classification_policy: got %q, want default-deny", i.ClassificationPolicy)
	}
	return nil
}

func (i DirectWriteInventory) validateWrites() error {
	classifier := newPersistenceClassifier(i)
	keys := make([]string, 0, len(i.Writes))
	for _, use := range i.Writes {
		if use.File == "" || use.Receiver == "" || use.Method == "" || use.AggregateOwner == "" || use.Count <= 0 {
			return fmt.Errorf("invalid direct-write row for %s", use.File)
		}
		if !underAnyRoot(use.File, i.AdapterRoots) {
			return fmt.Errorf("direct-write file %s is outside the declared adapter roots", use.File)
		}
		access, owner, classified := classifier.classify(use.Receiver, use.Method)
		if !classified || access != persistenceMutating {
			return fmt.Errorf("direct-write row %s.%s must be explicitly classified mutating", use.Receiver, use.Method)
		}
		if use.AggregateOwner == "unassigned_legacy" || use.AggregateOwner != owner {
			return fmt.Errorf("direct-write row %s.%s owner %q must match declared capability owner %q", use.Receiver, use.Method, use.AggregateOwner, owner)
		}
		adapterOwner, isOwnerAdapter := i.ownerAdapterOwner(use.File)
		switch use.Disposition {
		case directWriteDispositionOwnerAdapter:
			if use.ExpiresAfterPhase != 0 {
				return fmt.Errorf("owner-adapter direct-write row %s must not declare an expiry", use.File)
			}
			if !isOwnerAdapter || adapterOwner != use.AggregateOwner {
				return fmt.Errorf("owner-adapter direct-write row %s owner %q is not declared by owner_adapters", use.File, use.AggregateOwner)
			}
		case directWriteDispositionTransitional:
			if use.ExpiresAfterPhase < 2 || use.ExpiresAfterPhase > 7 {
				return fmt.Errorf("transitional direct-write row %s requires expires_after_phase between 2 and 7", use.File)
			}
			if isOwnerAdapter && adapterOwner == use.AggregateOwner {
				return fmt.Errorf("direct-write row %s is a declared owner adapter and cannot be labeled transitional", use.File)
			}
		default:
			return fmt.Errorf("direct-write row %s disposition %q must be owner_adapter or transitional", use.File, use.Disposition)
		}
		keys = append(keys, directWriteKey(use))
	}
	return validateSortedUnique("direct-write row", keys)
}

func (i DirectWriteInventory) validateOwnerAdapters() error {
	keys := make([]string, 0, len(i.OwnerAdapters))
	for index, adapter := range i.OwnerAdapters {
		adapter.Path = cleanInventoryPath(adapter.Path)
		if adapter.Path == "" || !underAnyRoot(adapter.Path, i.AdapterRoots) {
			return fmt.Errorf("direct-write owner adapter path %q is outside the declared adapter roots", adapter.Path)
		}
		if !validPersistenceOwner(adapter.AggregateOwner) || adapter.AggregateOwner == "unassigned_legacy" {
			return fmt.Errorf("direct-write owner adapter %s has unsupported owner %q", adapter.Path, adapter.AggregateOwner)
		}
		for previous := 0; previous < index; previous++ {
			other := i.OwnerAdapters[previous]
			if pathContains(adapter.Path, other.Path) || pathContains(other.Path, adapter.Path) {
				return fmt.Errorf("direct-write owner adapter paths %s and %s overlap", other.Path, adapter.Path)
			}
		}
		keys = append(keys, adapter.Path+"\x00"+adapter.AggregateOwner)
	}
	return validateSortedUnique("direct-write owner adapter", keys)
}

func (i DirectWriteInventory) ownerAdapterOwner(file string) (string, bool) {
	for _, adapter := range i.OwnerAdapters {
		if pathContains(adapter.Path, file) {
			return adapter.AggregateOwner, true
		}
	}
	return "", false
}

func pathContains(root, path string) bool {
	root = cleanInventoryPath(root)
	path = cleanInventoryPath(path)
	return path == root || strings.HasPrefix(path, root+"/")
}

func (i DirectWriteInventory) validateGenericMechanisms() error {
	wantMechanisms := []string{"action_ledger", "lease"}
	wantAdapterRoots := map[string][]string{
		"action_ledger": {"internal/modules/execution/fleetdb"},
		"lease": {
			"internal/modules/agents/fleetdb",
			"internal/modules/artifacts/fleetdb",
			"internal/modules/execution/fleetdb",
			"internal/modules/interaction/fleetdb",
		},
	}
	gotMechanisms := make([]string, 0, len(i.GenericMechanisms))
	for _, mechanism := range i.GenericMechanisms {
		if mechanism.DirectUses != 0 || mechanism.OwnerMapping == "" || mechanism.Enforcement == "" || mechanism.AllowedAdapterRoots == nil {
			return fmt.Errorf("generic mechanism %s must record zero direct uses plus owner mapping, allowed adapter roots, and enforcement", mechanism.Mechanism)
		}
		if err := validateSortedUnique("generic mechanism "+mechanism.Mechanism+" allowed adapter root", mechanism.AllowedAdapterRoots); err != nil {
			return err
		}
		if !slices.Equal(mechanism.AllowedAdapterRoots, wantAdapterRoots[mechanism.Mechanism]) {
			return fmt.Errorf("generic mechanism %s allowed adapter roots: got %v, want %v", mechanism.Mechanism, mechanism.AllowedAdapterRoots, wantAdapterRoots[mechanism.Mechanism])
		}
		gotMechanisms = append(gotMechanisms, mechanism.Mechanism)
	}
	if !slices.Equal(gotMechanisms, wantMechanisms) {
		return fmt.Errorf("generic mechanisms: got %v, want %v", gotMechanisms, wantMechanisms)
	}
	return nil
}

func (i DirectWriteInventory) validatePersistencePolicy() error {
	if err := validateSortedUnique("direct-write candidate receiver suffix", i.CandidateReceiverSuffixes); err != nil {
		return err
	}
	if len(i.CandidateReceiverSuffixes) == 0 {
		return errors.New("direct-write inventory requires candidate receiver suffixes for undeclared internal persistence surfaces")
	}
	packagesByPath, err := validatePersistencePackages(i.PersistencePackages)
	if err != nil {
		return err
	}
	methodSetsByName, err := validatePersistenceMethodSets(i.MethodSets)
	if err != nil {
		return err
	}
	if err := validatePersistenceReceiverSurfaces(i.ReceiverSurfaces, packagesByPath, methodSetsByName); err != nil {
		return err
	}
	return validatePersistenceFunctionSurfaces(i.FunctionSurfaces, packagesByPath)
}

func validatePersistencePackages(packages []PersistencePackage) (map[string]PersistencePackage, error) {
	if len(packages) == 0 {
		return nil, errors.New("direct-write inventory requires declared persistence packages")
	}
	packagePaths := make([]string, 0, len(packages))
	packagesByPath := make(map[string]PersistencePackage, len(packages))
	for _, pkg := range packages {
		if !strings.HasPrefix(pkg.Path, modulePath+"/internal/") {
			return nil, fmt.Errorf("direct-write persistence package %q must be below the module internal root", pkg.Path)
		}
		if pkg.ReceiverNames == nil || pkg.ReceiverSuffixes == nil || len(pkg.ReceiverNames)+len(pkg.ReceiverSuffixes) == 0 {
			return nil, fmt.Errorf("direct-write persistence package %s requires explicit receiver_names and receiver_suffixes", pkg.Path)
		}
		if err := validateSortedUnique("direct-write persistence receiver name", pkg.ReceiverNames); err != nil {
			return nil, err
		}
		if err := validateSortedUnique("direct-write persistence receiver suffix", pkg.ReceiverSuffixes); err != nil {
			return nil, err
		}
		packagePaths = append(packagePaths, pkg.Path)
		packagesByPath[pkg.Path] = pkg
	}
	if err := validateSortedUnique("direct-write persistence package", packagePaths); err != nil {
		return nil, err
	}
	return packagesByPath, nil
}

func validatePersistenceMethodSets(methodSets []PersistenceMethodSet) (map[string]PersistenceMethodSet, error) {
	methodSetNames := make([]string, 0, len(methodSets))
	methodSetsByName := make(map[string]PersistenceMethodSet, len(methodSets))
	for _, methodSet := range methodSets {
		if methodSet.Name == "" || methodSet.ReadOnly == nil || methodSet.Mutating == nil || len(methodSet.ReadOnly)+len(methodSet.Mutating) == 0 {
			return nil, fmt.Errorf("direct-write method set %q requires explicit non-empty read_only or mutating classifications", methodSet.Name)
		}
		if err := validateSortedUnique("direct-write "+methodSet.Name+" read-only method", methodSet.ReadOnly); err != nil {
			return nil, err
		}
		if err := validateSortedUnique("direct-write "+methodSet.Name+" mutating method", methodSet.Mutating); err != nil {
			return nil, err
		}
		readOnly := sliceSet(methodSet.ReadOnly)
		for _, method := range methodSet.Mutating {
			if _, duplicate := readOnly[method]; duplicate {
				return nil, fmt.Errorf("direct-write method set %s classifies %s as both read-only and mutating", methodSet.Name, method)
			}
		}
		methodSetNames = append(methodSetNames, methodSet.Name)
		methodSetsByName[methodSet.Name] = methodSet
	}
	if err := validateSortedUnique("direct-write method set", methodSetNames); err != nil {
		return nil, err
	}
	return methodSetsByName, nil
}

func validatePersistenceReceiverSurfaces(
	surfaces []PersistenceReceiverSurface,
	packagesByPath map[string]PersistencePackage,
	methodSetsByName map[string]PersistenceMethodSet,
) error {
	receivers := make([]string, 0, len(surfaces))
	for _, surface := range surfaces {
		pkg, ok := packagesByPath[surface.Package]
		if !ok {
			return fmt.Errorf("direct-write receiver %s uses undeclared persistence package %s", surface.Receiver, surface.Package)
		}
		methodSet, ok := methodSetsByName[surface.MethodSet]
		if !ok {
			return fmt.Errorf("direct-write receiver %s uses unknown method set %s", surface.Receiver, surface.MethodSet)
		}
		receiverPackage, receiverName, ok := splitReceiver(surface.Receiver)
		if !ok || receiverPackage != surface.Package || !pkg.matchesReceiver(receiverName) {
			return fmt.Errorf("direct-write receiver surface %s does not match package %s receiver policy", surface.Receiver, surface.Package)
		}
		if surface.CapabilityOwner == "" || surface.CapabilityOwner == "unassigned_legacy" {
			return fmt.Errorf("direct-write receiver %s requires an explicit capability owner", surface.Receiver)
		}
		if len(methodSet.Mutating) > 0 && !validPersistenceOwner(surface.CapabilityOwner) {
			return fmt.Errorf("direct-write receiver %s has unsupported mutating capability owner %s", surface.Receiver, surface.CapabilityOwner)
		}
		receivers = append(receivers, surface.Receiver)
	}
	if len(receivers) == 0 {
		return errors.New("direct-write inventory requires declared receiver surfaces")
	}
	return validateSortedUnique("direct-write receiver surface", receivers)
}

func validatePersistenceFunctionSurfaces(
	surfaces []PersistenceFunctionSurface,
	packagesByPath map[string]PersistencePackage,
) error {
	keys := make([]string, 0, len(surfaces))
	for _, surface := range surfaces {
		if _, ok := packagesByPath[surface.Package]; !ok {
			return fmt.Errorf("direct-write function %s uses undeclared persistence package %s", surface.Function, surface.Package)
		}
		if surface.Function == "" {
			return errors.New("direct-write persistence function requires a name")
		}
		if surface.Access != "read-only" && surface.Access != "mutating" {
			return fmt.Errorf("direct-write persistence function %s.%s access %q must be read-only or mutating", surface.Package, surface.Function, surface.Access)
		}
		if surface.CapabilityOwner == "" || surface.CapabilityOwner == "unassigned_legacy" {
			return fmt.Errorf("direct-write persistence function %s.%s requires an explicit capability owner", surface.Package, surface.Function)
		}
		if !validPersistenceOwner(surface.CapabilityOwner) {
			return fmt.Errorf("direct-write persistence function %s.%s has unsupported capability owner %s", surface.Package, surface.Function, surface.CapabilityOwner)
		}
		keys = append(keys, persistenceFunctionKey(surface.Package, surface.Function))
	}
	return validateSortedUnique("direct-write persistence function surface", keys)
}

func persistenceFunctionKey(packagePath, function string) string {
	return packagePath + "." + function
}

func (pkg PersistencePackage) matchesReceiver(name string) bool {
	if slices.Contains(pkg.ReceiverNames, name) {
		return true
	}
	for _, suffix := range pkg.ReceiverSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func splitReceiver(receiver string) (string, string, bool) {
	named := strings.TrimPrefix(receiver, "*")
	separator := strings.LastIndex(named, ".")
	if separator <= 0 || separator == len(named)-1 {
		return "", "", false
	}
	return named[:separator], named[separator+1:], true
}

func validPersistenceOwner(owner string) bool {
	return slices.Contains([]string{
		"agents", "artifacts", "automation", "connectors", "execution", "fleet-db",
		"interaction", "legacy_tombstone", "named_application_workflow", "read_projection", "sourcecontrol",
		"workflowcatalog", "workitems", "workspace",
	}, owner)
}

func CheckDirectWrites(root string, matrix AnalysisMatrix, inventory DirectWriteInventory) ([]DirectWriteUse, []string, error) {
	profiles := directWriteProfileNames(matrix)
	if !slices.Equal(profiles, inventory.AnalysisProfiles) {
		return nil, nil, fmt.Errorf("direct-write analysis profiles: got %v, baseline declares %v", profiles, inventory.AnalysisProfiles)
	}
	observed, err := SnapshotDirectWrites(root, matrix, inventory)
	if err != nil {
		return nil, nil, err
	}
	expected := inventory.Writes
	violations := []string{}
	expectedByKey := make(map[string]DirectWriteUse, len(expected))
	observedByKey := make(map[string]DirectWriteUse, len(observed))
	for _, use := range expected {
		expectedByKey[directWriteKey(use)] = use
	}
	for _, use := range observed {
		observedByKey[directWriteKey(use)] = use
		baseline, ok := expectedByKey[directWriteKey(use)]
		if !ok {
			violations = append(violations, fmt.Sprintf("new direct persistence write %s.%s in %s", use.Receiver, use.Method, use.File))
			continue
		}
		if baseline.Count != use.Count {
			violations = append(violations, fmt.Sprintf("direct persistence writes %s.%s in %s changed to %d (baseline %d)", use.Receiver, use.Method, use.File, use.Count, baseline.Count))
		}
		if baseline.AggregateOwner != use.AggregateOwner {
			violations = append(violations, fmt.Sprintf("direct persistence write %s.%s owner changed to %s (baseline %s)", use.Receiver, use.Method, use.AggregateOwner, baseline.AggregateOwner))
		}
		if baseline.Disposition != use.Disposition || baseline.ExpiresAfterPhase != use.ExpiresAfterPhase {
			violations = append(violations, fmt.Sprintf("direct persistence write %s.%s in %s lifecycle changed to %s/Phase %d (baseline %s/Phase %d)", use.Receiver, use.Method, use.File, use.Disposition, use.ExpiresAfterPhase, baseline.Disposition, baseline.ExpiresAfterPhase))
		}
	}
	for _, use := range expected {
		if _, ok := observedByKey[directWriteKey(use)]; !ok {
			violations = append(violations, fmt.Sprintf("stale direct-write baseline entry %s.%s in %s; refresh the baseline so the write cannot be reintroduced", use.Receiver, use.Method, use.File))
		}
	}
	if baseline := inventory.LegacyDriver; baseline != nil {
		if !rootCoveredByAdapterRoots(baseline.Root, inventory.AdapterRoots) {
			return nil, nil, fmt.Errorf("legacy driver root %s must be covered by adapter_roots", baseline.Root)
		}
		legacy := directWritesWithinRoot(observed, baseline.Root)
		rows, sites, digest := directWriteDigest(legacy)
		owners := directWriteOwnerBaselines(legacy)
		if rows != baseline.Rows || sites != baseline.Sites || digest != baseline.Digest || !slices.Equal(owners, baseline.Owners) {
			violations = append(violations, fmt.Sprintf("legacy driver direct-write ratchet changed: rows=%d sites=%d digest=%s owners=%+v (baseline rows=%d sites=%d digest=%s owners=%+v)", rows, sites, digest, owners, baseline.Rows, baseline.Sites, baseline.Digest, baseline.Owners))
		}
	}
	return observed, violations, nil
}

func rootCoveredByAdapterRoots(root string, adapterRoots []string) bool {
	root = cleanInventoryPath(root)
	for _, adapterRoot := range adapterRoots {
		adapterRoot = cleanInventoryPath(adapterRoot)
		if root == adapterRoot || strings.HasPrefix(root, adapterRoot+"/") {
			return true
		}
	}
	return false
}

func directWritesWithinRoot(uses []DirectWriteUse, root string) []DirectWriteUse {
	root = cleanInventoryPath(root)
	within := make([]DirectWriteUse, 0, len(uses))
	for _, use := range uses {
		file := cleanInventoryPath(use.File)
		if file == root || strings.HasPrefix(file, root+"/") {
			within = append(within, use)
		}
	}
	return within
}

func cleanInventoryPath(path string) string {
	return strings.Trim(filepath.ToSlash(filepath.Clean(strings.TrimSpace(path))), "/")
}

func SnapshotDirectWrites(
	root string,
	matrix AnalysisMatrix,
	inventory DirectWriteInventory,
) ([]DirectWriteUse, error) {
	return snapshotDirectWritesAtRoots(root, matrix, inventory, inventory.AdapterRoots)
}

func snapshotDirectWritesAtRoots(
	root string,
	matrix AnalysisMatrix,
	inventory DirectWriteInventory,
	roots []string,
) ([]DirectWriteUse, error) {
	profiles := append(append([]AnalysisProfile{}, matrix.Release...), matrix.Tagged...)
	if len(profiles) == 0 {
		return nil, errors.New("direct-write analysis requires at least one declared analysis profile")
	}
	classifier := newPersistenceClassifier(inventory)
	results := snapshotDirectWriteProfiles(root, profiles, roots, classifier)
	calls, problems, err := mergeDirectWriteProfileResults(profiles, results)
	if err != nil {
		return nil, err
	}
	return classifyDirectWriteCalls(calls, problems, classifier, inventory)
}

func directWriteDigest(uses []DirectWriteUse) (int, int, string) {
	hash := cryptosha256.New()
	sites := 0
	for _, use := range uses {
		sites += use.Count
		_, _ = fmt.Fprintf(hash, "%s\t%s\t%s\t%d\t%s\n", use.File, use.Receiver, use.Method, use.Count, use.AggregateOwner)
	}
	return len(uses), sites, hex.EncodeToString(hash.Sum(nil))
}

func directWriteOwnerBaselines(uses []DirectWriteUse) []LegacyDirectWriteOwnerBaseline {
	byOwner := make(map[string]LegacyDirectWriteOwnerBaseline)
	for _, use := range uses {
		owner := byOwner[use.AggregateOwner]
		owner.CapabilityOwner = use.AggregateOwner
		owner.Rows++
		owner.Sites += use.Count
		byOwner[use.AggregateOwner] = owner
	}
	owners := make([]LegacyDirectWriteOwnerBaseline, 0, len(byOwner))
	for _, owner := range byOwner {
		owners = append(owners, owner)
	}
	slices.SortFunc(owners, func(left, right LegacyDirectWriteOwnerBaseline) int {
		return strings.Compare(left.CapabilityOwner, right.CapabilityOwner)
	})
	return owners
}

type directWriteProfileResult struct {
	calls    map[directWriteCallIdentity]directWriteCall
	problems map[directWriteProblemIdentity]directWriteProblem
	err      error
}

func snapshotDirectWriteProfiles(
	root string,
	profiles []AnalysisProfile,
	adapterRoots []string,
	classifier persistenceClassifier,
) []directWriteProfileResult {
	results := make([]directWriteProfileResult, len(profiles))
	semaphore := make(chan struct{}, repositoryScaleLoadConcurrency)
	var wg sync.WaitGroup
	for index, profile := range profiles {
		index, profile := index, profile
		wg.Add(1)
		go func() {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			results[index].err = withRepositoryProfileCache(profile, func(environment []string) error {
				var err error
				results[index].calls, results[index].problems, err = snapshotDirectWriteProfileWithEnvironment(
					root, profile, adapterRoots, classifier, environment,
				)
				return err
			})
		}()
	}
	wg.Wait()
	return results
}

func mergeDirectWriteProfileResults(
	profiles []AnalysisProfile,
	results []directWriteProfileResult,
) (map[directWriteCallIdentity]directWriteCall, map[directWriteProblemIdentity]directWriteProblem, error) {
	calls := map[directWriteCallIdentity]directWriteCall{}
	problems := map[directWriteProblemIdentity]directWriteProblem{}
	for index, result := range results {
		if result.err != nil {
			return nil, nil, fmt.Errorf("scan direct writes for profile %s: %w", profiles[index].Name, result.err)
		}
		if err := mergeDirectWriteCalls(calls, result.calls, profiles[index].Name); err != nil {
			return nil, nil, err
		}
		mergeDirectWriteProblems(problems, result.problems, profiles[index].Name)
	}
	return calls, problems, nil
}

func mergeDirectWriteCalls(
	mergedCalls map[directWriteCallIdentity]directWriteCall,
	incoming map[directWriteCallIdentity]directWriteCall,
	profile string,
) error {
	for identity, call := range incoming {
		merged, ok := mergedCalls[identity]
		if !ok {
			merged = call
			merged.profiles = map[string]struct{}{}
		} else if merged.receiver != call.receiver || merged.method != call.method {
			return fmt.Errorf(
				"direct persistence call %s:%d:%d resolves as %s.%s in one profile and %s.%s in profile %s",
				identity.file, identity.line, identity.column,
				merged.receiver, merged.method, call.receiver, call.method, profile,
			)
		}
		merged.profiles[profile] = struct{}{}
		mergedCalls[identity] = merged
	}
	return nil
}

func mergeDirectWriteProblems(
	mergedProblems map[directWriteProblemIdentity]directWriteProblem,
	incoming map[directWriteProblemIdentity]directWriteProblem,
	profile string,
) {
	for identity, problem := range incoming {
		merged, ok := mergedProblems[identity]
		if !ok {
			merged = problem
			merged.profiles = map[string]struct{}{}
		}
		merged.profiles[profile] = struct{}{}
		mergedProblems[identity] = merged
	}
}

func classifyDirectWriteCalls(
	calls map[directWriteCallIdentity]directWriteCall,
	problems map[directWriteProblemIdentity]directWriteProblem,
	classifier persistenceClassifier,
	inventory DirectWriteInventory,
) ([]DirectWriteUse, error) {
	unclassified := directWriteProblemMessages(problems)
	identities := make([]directWriteCallIdentity, 0, len(calls))
	for identity := range calls {
		identities = append(identities, identity)
	}
	slices.SortFunc(identities, compareDirectWriteCallIdentity)
	counts := map[directWriteCountKey]int{}
	for _, identity := range identities {
		call := calls[identity]
		access, owner, classified := classifier.classify(call.receiver, call.method)
		if !classified {
			unclassified = append(unclassified, unclassifiedDirectWriteCall(call, classifier))
			continue
		}
		if access == persistenceMutating {
			if ownerCoreUsesOwnDeclaredPort(call, owner) {
				continue
			}
			counts[directWriteCountKey{file: call.file, receiver: call.receiver, method: call.method, owner: owner}]++
		}
	}
	if len(unclassified) > 0 {
		return nil, fmt.Errorf(
			"unclassified persistence methods must be explicitly classified read-only or mutating:\n- %s",
			strings.Join(unclassified, "\n- "),
		)
	}
	return directWriteRows(counts, inventory), nil
}

// ownerCoreUsesOwnDeclaredPort distinguishes target architecture from a
// legacy direct persistence write. A capability core is expected to invoke
// its own declared command port; concrete fleetdb/httpapi adapters remain in
// the direct-write inventory, as do calls to legacy or foreign-owner stores.
func ownerCoreUsesOwnDeclaredPort(call directWriteCall, owner string) bool {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return false
	}
	if owner == "named_application_workflow" {
		return namedWorkflowCoreUsesOwnDeclaredPort(call)
	}
	prefix := "internal/modules/" + owner + "/"
	if !strings.HasPrefix(call.file, prefix) {
		return false
	}
	remainder := strings.TrimPrefix(call.file, prefix)
	if first, _, found := strings.Cut(remainder, "/"); found && isConcreteAdapterSegment(first) {
		return false
	}
	receiver := strings.TrimPrefix(call.receiver, "*")
	return strings.HasPrefix(receiver, modulePath+"/internal/modules/"+owner+".")
}

// namedWorkflowCoreUsesOwnDeclaredPort applies the same owner-core exemption
// to an explicitly classified application workflow. The workflow directory
// name is the aggregate discriminator: a workflow may invoke only a port
// declared by its own root package, while concrete fleetdb/http adapters stay
// visible in the direct-write inventory.
func namedWorkflowCoreUsesOwnDeclaredPort(call directWriteCall) bool {
	const appPrefix = "internal/app/"
	if !strings.HasPrefix(call.file, appPrefix) {
		return false
	}
	relative := strings.TrimPrefix(call.file, appPrefix)
	workflow, remainder, found := strings.Cut(relative, "/")
	if !found || workflow == "" || remainder == "" {
		return false
	}
	if segment, _, nested := strings.Cut(remainder, "/"); nested && isConcreteAdapterSegment(segment) {
		return false
	}
	receiver := strings.TrimPrefix(call.receiver, "*")
	return strings.HasPrefix(receiver, modulePath+"/internal/app/"+workflow+".")
}

func directWriteProblemMessages(problems map[directWriteProblemIdentity]directWriteProblem) []string {
	messages := make([]string, 0, len(problems))
	problemIdentities := make([]directWriteProblemIdentity, 0, len(problems))
	for identity := range problems {
		problemIdentities = append(problemIdentities, identity)
	}
	slices.SortFunc(problemIdentities, compareDirectWriteProblemIdentity)
	for _, identity := range problemIdentities {
		messages = append(messages, problems[identity].withProfiles())
	}
	return messages
}

func unclassifiedDirectWriteCall(call directWriteCall, classifier persistenceClassifier) string {
	profileNames := make([]string, 0, len(call.profiles))
	for profile := range call.profiles {
		profileNames = append(profileNames, profile)
	}
	slices.Sort(profileNames)
	return fmt.Sprintf("%s at %s:%d:%d (profiles: %s)",
		classifier.unclassifiedReason(call.receiver, call.method),
		call.file, call.line, call.column, strings.Join(profileNames, ", "))
}

func directWriteProfileNames(matrix AnalysisMatrix) []string {
	profiles := append(append([]AnalysisProfile{}, matrix.Release...), matrix.Tagged...)
	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		names = append(names, profile.Name)
	}
	slices.Sort(names)
	return names
}

func snapshotDirectWriteProfile(
	root string,
	profile AnalysisProfile,
	adapterRoots []string,
	classifier persistenceClassifier,
) (map[directWriteCallIdentity]directWriteCall, map[directWriteProblemIdentity]directWriteProblem, error) {
	return snapshotDirectWriteProfileWithEnvironment(
		root, profile, adapterRoots, classifier, profileEnvironment(profile),
	)
}

func snapshotDirectWriteProfileWithEnvironment(
	root string,
	profile AnalysisProfile,
	adapterRoots []string,
	classifier persistenceClassifier,
	environment []string,
) (map[directWriteCallIdentity]directWriteCall, map[directWriteProblemIdentity]directWriteProblem, error) {
	if err := validateDirectWriteRequiredSourcesWithEnvironment(root, profile, environment); err != nil {
		return nil, nil, err
	}
	loaded, err := loadDirectWritePackagesWithEnvironment(root, profile, adapterRoots, environment)
	if err != nil {
		return nil, nil, err
	}
	calls := map[directWriteCallIdentity]directWriteCall{}
	problems := map[directWriteProblemIdentity]directWriteProblem{}
	for _, pkg := range loaded {
		if err := collectDirectWritePackage(root, pkg, adapterRoots, classifier, calls, problems); err != nil {
			return nil, nil, err
		}
	}
	return calls, problems, nil
}

func validateDirectWriteRequiredSources(root string, profile AnalysisProfile) error {
	return validateDirectWriteRequiredSourcesWithEnvironment(root, profile, profileEnvironment(profile))
}

func validateDirectWriteRequiredSourcesWithEnvironment(
	root string,
	profile AnalysisProfile,
	environment []string,
) error {
	if len(profile.RequiredFiles) == 0 {
		return nil
	}
	patterns := make([]string, 0, len(profile.RequiredFiles))
	seenPatterns := map[string]struct{}{}
	for _, required := range profile.RequiredFiles {
		directory := filepath.ToSlash(filepath.Dir(required))
		pattern := "./" + directory
		if directory == "." {
			pattern = "."
		}
		if _, seen := seenPatterns[pattern]; !seen {
			seenPatterns[pattern] = struct{}{}
			patterns = append(patterns, pattern)
		}
	}
	cfg := &packages.Config{
		Mode:       packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles,
		Dir:        root,
		Env:        environment,
		Tests:      true,
		BuildFlags: profileBuildFlags(profile),
	}
	loaded, err := packages.Load(cfg, patterns...)
	if err != nil {
		return err
	}
	violations := requiredSourceViolations(root, profile, loaded)
	if len(violations) > 0 {
		return errors.New(strings.Join(violations, "; "))
	}
	return nil
}

const directWritePackageLoadMode packages.LoadMode = packages.NeedName |
	packages.NeedFiles |
	packages.NeedCompiledGoFiles |
	packages.NeedSyntax |
	packages.NeedTypes |
	packages.NeedTypesInfo |
	packages.NeedImports

func loadDirectWritePackages(root string, profile AnalysisProfile, adapterRoots []string) ([]*packages.Package, error) {
	return loadDirectWritePackagesWithEnvironment(root, profile, adapterRoots, profileEnvironment(profile))
}

func loadDirectWritePackagesWithEnvironment(
	root string,
	profile AnalysisProfile,
	adapterRoots []string,
	environment []string,
) ([]*packages.Package, error) {
	cfg := &packages.Config{
		// Direct-write collection inspects only the requested adapter roots.
		// Dependency types are resolved from export data; loading dependency
		// syntax and TypesInfo here creates a second repository-scale graph per
		// profile and is not needed by collectDirectWritePackage.
		Mode:       directWritePackageLoadMode,
		Dir:        root,
		Env:        environment,
		BuildFlags: profileBuildFlags(profile),
	}
	patterns := []string{}
	for _, sourceRoot := range adapterRoots {
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(sourceRoot))); statErr != nil {
			return nil, fmt.Errorf("inspect direct-write adapter root %q: %w", sourceRoot, statErr)
		}
		patterns = append(patterns, "./"+sourceRoot+"/...")
	}
	if len(patterns) == 0 {
		return nil, nil
	}
	loaded, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}
	return loaded, nil
}
