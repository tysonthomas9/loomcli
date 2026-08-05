// Package archtest provides behavior-neutral architecture checks used while
// Loom migrates toward capability-owned modules.
package archtest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	AnalyzerVersion = "1.0.0"
	SchemaVersion   = 1
)

var (
	fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
	sha256  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Baseline struct {
	SchemaVersion   int                 `json:"schema_version"`
	AnalyzerVersion string              `json:"analyzer_version"`
	Source          SourceBaseline      `json:"source"`
	Validation      ValidationBaseline  `json:"validation"`
	Structural      StructuralBaseline  `json:"structural"`
	Ratchets        RatchetBaseline     `json:"ratchets"`
	Inventories     []InventoryBaseline `json:"inventories"`
	Decisions       []DecisionBaseline  `json:"decisions"`
}

type SourceBaseline struct {
	LoomHead             string `json:"loom_head"`
	LoomV5Head           string `json:"loom_v5_head"`
	FleetDBHead          string `json:"fleetdb_head"`
	FleetDBMainHead      string `json:"fleetdb_main_head"`
	FleetDBOpenAPISHA256 string `json:"fleetdb_openapi_sha256"`
}

type StructuralBaseline struct {
	GoPackages                  int `json:"go_packages"`
	InternalGoPackages          int `json:"internal_go_packages"`
	FrontendProductionFiles     int `json:"frontend_production_files"`
	FrontendComponentDirs       int `json:"frontend_component_dirs"`
	FrontendAppLines            int `json:"frontend_app_lines"`
	PackageSizeCeiling          int `json:"package_size_ceiling"`
	InternalImportFanoutCeiling int `json:"internal_import_fanout_ceiling"`
}

type RatchetBaseline struct {
	CompositeStore              CompositeStoreRatchet `json:"composite_store"`
	LegacyHandlerImports        LegacyImportRatchet   `json:"legacy_handler_imports"`
	LegacyHandlerServiceImports LegacyImportRatchet   `json:"legacy_handler_service_imports"`
}

type CompositeStoreRatchet struct {
	MaxProductionFiles        int      `json:"max_production_files"`
	MaxOutsideComposition     int      `json:"max_outside_composition"`
	CompositionPrefixes       []string `json:"composition_prefixes"`
	AllowedProductionFileUses []string `json:"allowed_production_file_uses"`
}

type LegacyImportRatchet struct {
	Root           string            `json:"root"`
	DeniedPrefixes []string          `json:"denied_prefixes"`
	Allowed        []LegacyImportUse `json:"allowed"`
}

type LegacyImportUse struct {
	File   string `json:"file"`
	Import string `json:"import"`
}

type InventoryBaseline struct {
	ID                 string `json:"id"`
	Status             string `json:"status"`
	Owner              string `json:"owner"`
	AcceptanceCriteria string `json:"acceptance_criteria"`
}

type DecisionBaseline struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Owner      string `json:"owner"`
	Rationale  string `json:"rationale"`
	ReviewedBy string `json:"reviewed_by"`
	ReviewedAt string `json:"reviewed_at"`
}

type CapabilityGraph struct {
	SchemaVersion        int                  `yaml:"schema_version"`
	Status               string               `yaml:"status"`
	CompletedPhase       int                  `yaml:"completed_phase"`
	DecisionDependencies []string             `yaml:"decision_dependencies"`
	ModuleRoot           string               `yaml:"module_root"`
	AppRoot              string               `yaml:"app_root"`
	PlatformRoot         string               `yaml:"platform_root"`
	Restrictions         BoundaryRestrictions `yaml:"restrictions"`
	ExternalImports      ExternalImportPolicy `yaml:"external_import_policy"`
	Capabilities         []Capability         `yaml:"capabilities"`
	AggregateOwnership   []AggregateOwnership `yaml:"aggregate_ownership"`
	LegacyPaths          []LegacyPath         `yaml:"legacy_paths"`
	Edges                []GraphEdge          `yaml:"edges"`
}

type Capability struct {
	Name   string `yaml:"name"`
	Root   string `yaml:"root"`
	Status string `yaml:"status"`
}

type GraphEdge struct {
	From    string              `yaml:"from"`
	To      string              `yaml:"to"`
	Kinds   []string            `yaml:"kinds"`
	Durable *DurableEventPolicy `yaml:"durable,omitempty"`
}

type BoundaryRestrictions struct {
	PlatformImportsCapabilities bool `yaml:"platform_imports_capabilities"`
	ServeCompositionOnly        bool `yaml:"serve_composition_only"`
	NamedAppsPublicRootsOnly    bool `yaml:"named_apps_public_roots_only"`
	NamedAppsOwnPortsOnly       bool `yaml:"named_apps_own_ports_only"`
	ModulesRejectLegacyTypes    bool `yaml:"modules_reject_legacy_types"`
}

// ExternalImportPolicy keeps capability packages default-deny for third-party
// dependencies. A capability core may use ordinary standard-library building
// blocks except the explicitly denied infrastructure families below. Concrete
// adapters may use the standard library, but third-party transport/storage
// dependencies still require a reviewed graph entry.
type ExternalImportPolicy struct {
	CoreAllowedPrefixes        []string `yaml:"core_allowed_prefixes"`
	AdapterAllowedPrefixes     []string `yaml:"adapter_allowed_prefixes"`
	PlatformAllowedPrefixes    []string `yaml:"platform_allowed_prefixes"`
	CoreDeniedStandardPrefixes []string `yaml:"core_denied_standard_prefixes"`
}

type AggregateOwnership struct {
	Record              string `yaml:"record"`
	Owner               string `yaml:"owner"`
	Mechanism           bool   `yaml:"mechanism,omitempty"`
	Discriminator       string `yaml:"discriminator,omitempty"`
	CrossCapabilityRule string `yaml:"cross_capability_rule"`
}

type LegacyPath struct {
	Path              string               `yaml:"path"`
	Owner             string               `yaml:"owner"`
	RemovalIssue      string               `yaml:"removal_issue"`
	ExpiresAfterPhase int                  `yaml:"expires_after_phase"`
	Extension         *LegacyPathExtension `yaml:"extension,omitempty"`
}

// LegacyPathExtension makes an expiry extension reviewable instead of letting
// a caller move the milestone number without recording the remaining work.
type LegacyPathExtension struct {
	ReviewedBy         string   `yaml:"reviewed_by"`
	ReviewedAt         string   `yaml:"reviewed_at"`
	Rationale          string   `yaml:"rationale"`
	ReplacementAPIs    []string `yaml:"replacement_apis"`
	RemainingCallSites []string `yaml:"remaining_call_sites"`
}

type DurableEventPolicy struct {
	IdempotencyKey string `yaml:"idempotency_key"`
	ActorScope     string `yaml:"actor_scope"`
	MaxHops        int    `yaml:"max_hops"`
	ReentryPolicy  string `yaml:"reentry_policy"`
}

type AnalysisMatrix struct {
	SchemaVersion int               `yaml:"schema_version"`
	Status        string            `yaml:"status"`
	Release       []AnalysisProfile `yaml:"release_targets"`
	Tagged        []AnalysisProfile `yaml:"tag_profiles"`
	AST           ASTProfile        `yaml:"ast_all_files"`
}

type AnalysisProfile struct {
	Name               string   `yaml:"name"`
	GOOS               string   `yaml:"goos"`
	GOARCH             string   `yaml:"goarch"`
	Tags               []string `yaml:"tags"`
	Race               bool     `yaml:"race,omitempty"`
	RequiredFiles      []string `yaml:"required_files,omitempty"`
	Enforced           bool     `yaml:"enforced"`
	Owner              string   `yaml:"owner,omitempty"`
	AcceptanceCriteria string   `yaml:"acceptance_criteria,omitempty"`
}

type ASTProfile struct {
	IncludeTests     bool              `yaml:"include_tests"`
	ExcludeGenerated bool              `yaml:"exclude_generated"`
	Ignore           []IgnoreException `yaml:"ignore"`
}

type IgnoreException struct {
	Path   string `yaml:"path"`
	Reason string `yaml:"reason"`
	Owner  string `yaml:"owner"`
	Expiry string `yaml:"expiry"`
}

func LoadBaseline(path string) (Baseline, error) {
	f, err := os.Open(path) //nolint:gosec // The caller supplies a repository-owned manifest path.
	if err != nil {
		return Baseline{}, err
	}
	defer f.Close()

	var value Baseline
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Baseline{}, fmt.Errorf("decode baseline: %w", err)
	}
	if err := requireEOF(decoder); err != nil {
		return Baseline{}, fmt.Errorf("decode baseline: %w", err)
	}
	if err := value.Validate(); err != nil {
		return Baseline{}, err
	}
	return value, nil
}

func LoadCapabilityGraph(path string) (CapabilityGraph, error) {
	var value CapabilityGraph
	if err := decodeYAML(path, &value); err != nil {
		return CapabilityGraph{}, fmt.Errorf("decode capability graph: %w", err)
	}
	if err := value.Validate(); err != nil {
		return CapabilityGraph{}, err
	}
	return value, nil
}

func LoadAnalysisMatrix(path string) (AnalysisMatrix, error) {
	var value AnalysisMatrix
	if err := decodeYAML(path, &value); err != nil {
		return AnalysisMatrix{}, fmt.Errorf("decode analysis matrix: %w", err)
	}
	if err := value.Validate(); err != nil {
		return AnalysisMatrix{}, err
	}
	return value, nil
}

func decodeYAML(path string, value any) error {
	f, err := os.Open(path) //nolint:gosec // The caller supplies a repository-owned manifest path.
	if err != nil {
		return err
	}
	defer f.Close()

	decoder := yaml.NewDecoder(f)
	decoder.KnownFields(true)
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple YAML documents are not allowed")
		}
		return err
	}
	return nil
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func (b Baseline) Validate() error {
	if b.SchemaVersion != SchemaVersion {
		return fmt.Errorf("baseline schema_version: got %d, want %d", b.SchemaVersion, SchemaVersion)
	}
	if b.AnalyzerVersion != AnalyzerVersion {
		return fmt.Errorf("baseline analyzer_version: got %q, want %q", b.AnalyzerVersion, AnalyzerVersion)
	}
	if err := validateSourceBaseline(b.Source); err != nil {
		return err
	}
	if err := validateValidationBaseline(b.Validation, b.Source); err != nil {
		return err
	}
	if err := validateStructuralBaseline(b.Structural); err != nil {
		return err
	}
	if err := validateCompositeStoreRatchet(b.Ratchets.CompositeStore); err != nil {
		return err
	}
	if err := validateLegacyImportRatchet(b.Ratchets.LegacyHandlerImports); err != nil {
		return err
	}
	if err := validateLegacyImportRatchet(b.Ratchets.LegacyHandlerServiceImports); err != nil {
		return err
	}
	if err := validateInventories(b.Inventories); err != nil {
		return err
	}
	return validateDecisions(b.Decisions)
}

func validateSourceBaseline(source SourceBaseline) error {
	for label, value := range map[string]string{
		"loom_head": source.LoomHead, "loom_v5_head": source.LoomV5Head,
		"fleetdb_head": source.FleetDBHead, "fleetdb_main_head": source.FleetDBMainHead,
	} {
		if !fullSHA.MatchString(value) {
			return fmt.Errorf("baseline source %s must be a 40-character lowercase SHA", label)
		}
	}
	if !sha256.MatchString(source.FleetDBOpenAPISHA256) {
		return errors.New("baseline source fleetdb_openapi_sha256 must be a 64-character lowercase SHA-256")
	}
	return nil
}

func validateStructuralBaseline(structural StructuralBaseline) error {
	if structural.GoPackages <= 0 || structural.InternalGoPackages <= 0 ||
		structural.FrontendProductionFiles <= 0 || structural.FrontendComponentDirs <= 0 ||
		structural.FrontendAppLines <= 0 || structural.PackageSizeCeiling <= 0 ||
		structural.InternalImportFanoutCeiling <= 0 {
		return errors.New("baseline structural measurements must all be positive")
	}
	return nil
}

func validateCompositeStoreRatchet(ratchet CompositeStoreRatchet) error {
	if ratchet.MaxProductionFiles != len(ratchet.AllowedProductionFileUses) {
		return fmt.Errorf("composite Store max_production_files %d does not match %d allowed files", ratchet.MaxProductionFiles, len(ratchet.AllowedProductionFileUses))
	}
	if ratchet.MaxOutsideComposition < 0 || ratchet.MaxOutsideComposition > ratchet.MaxProductionFiles {
		return errors.New("composite Store max_outside_composition is invalid")
	}
	if err := validateSortedUnique("composition prefix", ratchet.CompositionPrefixes); err != nil {
		return err
	}
	if err := validateSortedUnique("composite Store allowed file", ratchet.AllowedProductionFileUses); err != nil {
		return err
	}
	for _, path := range ratchet.AllowedProductionFileUses {
		if !strings.HasPrefix(path, "internal/") || !strings.HasSuffix(path, ".go") || strings.Contains(path, "..") {
			return fmt.Errorf("invalid composite Store allowed file %q", path)
		}
	}
	return nil
}

func validateLegacyImportRatchet(ratchet LegacyImportRatchet) error {
	if !safeInternalRoot(ratchet.Root) {
		return fmt.Errorf("legacy handler root must be a safe path under internal, got %q", ratchet.Root)
	}
	if err := validateSortedUnique("legacy handler denied prefix", ratchet.DeniedPrefixes); err != nil {
		return err
	}
	keys := make([]string, 0, len(ratchet.Allowed))
	for _, use := range ratchet.Allowed {
		if !strings.HasPrefix(use.File, ratchet.Root+"/") || !strings.HasSuffix(use.File, ".go") {
			return fmt.Errorf("legacy handler import has invalid file %q", use.File)
		}
		if !matchesImportPrefix(use.Import, ratchet.DeniedPrefixes) {
			return fmt.Errorf("legacy handler import %q does not match a denied prefix", use.Import)
		}
		keys = append(keys, use.File+"\x00"+use.Import)
	}
	return validateSortedUnique("legacy handler allowed import", keys)
}

func matchesImportPrefix(importPath string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
			return true
		}
	}
	return false
}

func validateInventories(values []InventoryBaseline) error {
	want := []string{
		"legacy-handler-imports",
		"graph-approval-contract",
		"package-import-graph",
		"direct-persistence-writes",
		"generic-lease-action-ledger",
		"mutation-owner",
		"authority",
		"transaction-process-manager",
		"named-runtime-loops",
		"startup-performance",
		"supervisor-disabled-matrix",
	}
	if len(values) != len(want) {
		return fmt.Errorf("baseline must record exactly %d Phase 1 inventories; got %d", len(want), len(values))
	}
	seen := map[string]struct{}{}
	for _, value := range values {
		if value.ID == "" || value.Owner == "" || value.AcceptanceCriteria == "" {
			return errors.New("every inventory requires id, owner, and acceptance_criteria")
		}
		if value.Status != "complete" {
			return fmt.Errorf("phase 1 inventory %s must be complete; status is %q", value.ID, value.Status)
		}
		if _, ok := seen[value.ID]; ok {
			return fmt.Errorf("duplicate inventory %q", value.ID)
		}
		seen[value.ID] = struct{}{}
	}
	for _, id := range want {
		if _, ok := seen[id]; !ok {
			return fmt.Errorf("baseline is missing Phase 1 inventory %s", id)
		}
	}
	return nil
}

func validateDecisions(values []DecisionBaseline) error {
	want := migrationDecisionIDs()
	if len(values) != len(want) {
		return fmt.Errorf("baseline must record exactly MM-1 through MM-7; got %d decisions", len(values))
	}
	seen := map[string]struct{}{}
	for _, value := range values {
		if value.Owner == "" || value.Rationale == "" || value.ReviewedBy == "" || value.ReviewedAt == "" {
			return fmt.Errorf("decision %s requires owner, rationale, reviewer, and review date", value.ID)
		}
		if value.Status != "approved" && value.Status != "rejected" {
			return fmt.Errorf("decision %s has unsupported status %q", value.ID, value.Status)
		}
		seen[value.ID] = struct{}{}
	}
	for _, id := range want {
		if _, ok := seen[id]; !ok {
			return fmt.Errorf("baseline is missing decision %s", id)
		}
	}
	return nil
}

func (g CapabilityGraph) Validate() error {
	if g.SchemaVersion != SchemaVersion {
		return fmt.Errorf("capability graph schema_version: got %d, want %d", g.SchemaVersion, SchemaVersion)
	}
	if g.Status != "approved" {
		return fmt.Errorf("phase 1 capability graph status must be approved, got %q", g.Status)
	}
	if g.CompletedPhase < 1 || g.CompletedPhase > 7 {
		return fmt.Errorf("capability graph completed_phase must be between 1 and 7, got %d", g.CompletedPhase)
	}
	if err := validateGraphRootsAndRestrictions(g); err != nil {
		return err
	}
	capabilities, err := validateCapabilities(g.Capabilities)
	if err != nil {
		return err
	}
	if err := validateAggregateOwnership(g.AggregateOwnership, capabilities); err != nil {
		return err
	}
	if err := validateLegacyPaths(g.LegacyPaths, g.CompletedPhase); err != nil {
		return err
	}
	if err := validateDecisionDependencies(g.DecisionDependencies); err != nil {
		return err
	}
	if err := validateGraphEdges(g.Edges, capabilities); err != nil {
		return err
	}
	if cycle := synchronousCycle(g.Edges); len(cycle) > 0 {
		return fmt.Errorf("synchronous capability cycle: %s", strings.Join(cycle, " -> "))
	}
	return nil
}

func validateGraphRootsAndRestrictions(graph CapabilityGraph) error {
	if !safeInternalRoot(graph.ModuleRoot) {
		return fmt.Errorf("capability graph module_root must be a safe path under internal, got %q", graph.ModuleRoot)
	}
	if !safeInternalRoot(graph.AppRoot) || !safeInternalRoot(graph.PlatformRoot) {
		return errors.New("capability graph app_root and platform_root must be safe paths under internal")
	}
	if graph.AppRoot == graph.ModuleRoot || graph.PlatformRoot == graph.ModuleRoot || graph.AppRoot == graph.PlatformRoot {
		return errors.New("capability graph module, app, and platform roots must be distinct")
	}
	restrictions := graph.Restrictions
	if restrictions.PlatformImportsCapabilities || !restrictions.ServeCompositionOnly ||
		!restrictions.NamedAppsPublicRootsOnly || !restrictions.NamedAppsOwnPortsOnly ||
		!restrictions.ModulesRejectLegacyTypes {
		return errors.New("capability graph must enable the Phase 1 app, platform, and module restrictions")
	}
	return validateExternalImportPolicy(graph.ExternalImports)
}

func validateExternalImportPolicy(policy ExternalImportPolicy) error {
	if policy.CoreAllowedPrefixes == nil || policy.AdapterAllowedPrefixes == nil ||
		policy.PlatformAllowedPrefixes == nil || policy.CoreDeniedStandardPrefixes == nil {
		return errors.New("capability graph external import policy must explicitly record core, adapter, platform, and denied-standard prefix lists")
	}
	for label, values := range map[string][]string{
		"core allowed external prefix":     policy.CoreAllowedPrefixes,
		"adapter allowed external prefix":  policy.AdapterAllowedPrefixes,
		"platform allowed external prefix": policy.PlatformAllowedPrefixes,
		"core denied standard prefix":      policy.CoreDeniedStandardPrefixes,
	} {
		if err := validateSortedUnique(label, values); err != nil {
			return err
		}
		for _, value := range values {
			if value == "" || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, "..") {
				return fmt.Errorf("capability graph %s %q is invalid", label, value)
			}
		}
	}
	wantDeniedStandard := []string{"database/sql", "net/http", "net/rpc", "os", "plugin", "syscall"}
	if !slices.Equal(policy.CoreDeniedStandardPrefixes, wantDeniedStandard) {
		return fmt.Errorf("capability graph core denied standard prefixes: got %v, want %v", policy.CoreDeniedStandardPrefixes, wantDeniedStandard)
	}
	approvedExternal := append(append([]string{}, policy.CoreAllowedPrefixes...), policy.AdapterAllowedPrefixes...)
	approvedExternal = append(approvedExternal, policy.PlatformAllowedPrefixes...)
	for _, value := range approvedExternal {
		if isStandardLibraryImport(value) || value == modulePath || strings.HasPrefix(value, modulePath+"/") {
			return fmt.Errorf("capability graph approved external prefix %q must name a third-party module", value)
		}
	}
	return nil
}

func validateCapabilities(values []Capability) (map[string]Capability, error) {
	want := map[string]string{
		"agents":          "agents",
		"artifacts":       "artifacts",
		"automation":      "automation",
		"connectors":      "connectors",
		"execution":       "execution",
		"interaction":     "interaction",
		"sourcecontrol":   "sourcecontrol",
		"workflowcatalog": "workflowcatalog",
		"workitems":       "workitems",
		"workspace":       "workspace",
	}
	if len(values) != len(want) {
		return nil, fmt.Errorf("capability graph must declare the exact ten approved capabilities; got %d", len(values))
	}
	byName := map[string]Capability{}
	byRoot := map[string]string{}
	for _, capability := range values {
		if capability.Name == "" || capability.Root == "" {
			return nil, errors.New("every capability requires name and root")
		}
		if strings.Contains(capability.Root, "/") || strings.Contains(capability.Root, "..") {
			return nil, fmt.Errorf("capability %s has unsafe root %q", capability.Name, capability.Root)
		}
		if capability.Status != "planned" && capability.Status != "active" {
			return nil, fmt.Errorf("capability %s has unsupported status %q", capability.Name, capability.Status)
		}
		if _, ok := byName[capability.Name]; ok {
			return nil, fmt.Errorf("duplicate capability %q", capability.Name)
		}
		if prior, ok := byRoot[capability.Root]; ok {
			return nil, fmt.Errorf("capabilities %s and %s share root %q", prior, capability.Name, capability.Root)
		}
		byName[capability.Name] = capability
		byRoot[capability.Root] = capability.Name
	}
	for name, root := range want {
		capability, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("capability graph is missing approved capability %q", name)
		}
		if capability.Root != root {
			return nil, fmt.Errorf("approved capability %s root: got %q, want %q", name, capability.Root, root)
		}
	}
	return byName, nil
}

func validateDecisionDependencies(values []string) error {
	if err := validateSortedUnique("decision dependency", values); err != nil {
		return err
	}
	want := migrationDecisionIDs()
	if !slices.Equal(values, want) {
		return fmt.Errorf("capability graph decision dependencies: got %v, want %v", values, want)
	}
	return nil
}

func migrationDecisionIDs() []string {
	return []string{"MM-1", "MM-2", "MM-3", "MM-4", "MM-5", "MM-6", "MM-7"}
}

func safeInternalRoot(path string) bool {
	return strings.HasPrefix(path, "internal/") && !strings.Contains(path, "..") && !strings.HasSuffix(path, "/")
}

func validateGraphEdges(edges []GraphEdge, capabilities map[string]Capability) error {
	seen := map[string]struct{}{}
	for _, edge := range edges {
		if err := validateGraphEdge(edge, capabilities, seen); err != nil {
			return err
		}
	}
	return nil
}

func validateGraphEdge(edge GraphEdge, capabilities map[string]Capability, seen map[string]struct{}) error {
	if _, ok := capabilities[edge.From]; !ok {
		return fmt.Errorf("edge has unknown source capability %q", edge.From)
	}
	if _, ok := capabilities[edge.To]; !ok {
		return fmt.Errorf("edge has unknown target capability %q", edge.To)
	}
	if edge.From == edge.To {
		return fmt.Errorf("self edge is not allowed for %s", edge.From)
	}
	if len(edge.Kinds) == 0 {
		return fmt.Errorf("edge %s -> %s has no kinds", edge.From, edge.To)
	}
	if !slices.IsSorted(edge.Kinds) {
		return fmt.Errorf("edge %s -> %s kinds must be sorted", edge.From, edge.To)
	}
	hasDurable, err := validateEdgeKinds(edge, seen)
	if err != nil {
		return err
	}
	if !hasDurable && edge.Durable != nil {
		return fmt.Errorf("non-durable edge %s -> %s cannot declare durable policy", edge.From, edge.To)
	}
	if hasDurable && (edge.Durable == nil || edge.Durable.IdempotencyKey == "" || edge.Durable.ActorScope == "" ||
		edge.Durable.MaxHops <= 0 || edge.Durable.ReentryPolicy == "") {
		return fmt.Errorf("durable edge %s -> %s requires idempotency, actor scope, max hops, and re-entry policy", edge.From, edge.To)
	}
	return nil
}

func validateEdgeKinds(edge GraphEdge, seen map[string]struct{}) (bool, error) {
	hasDurable := false
	for _, kind := range edge.Kinds {
		if kind != "import" && kind != "command" && kind != "query" && kind != "durable_event" {
			return false, fmt.Errorf("edge %s -> %s has unsupported kind %q", edge.From, edge.To, kind)
		}
		hasDurable = hasDurable || kind == "durable_event"
		key := edge.From + "\x00" + edge.To + "\x00" + kind
		if _, ok := seen[key]; ok {
			return false, fmt.Errorf("duplicate edge kind %s -> %s (%s)", edge.From, edge.To, kind)
		}
		seen[key] = struct{}{}
	}
	return hasDurable, nil
}

func validateAggregateOwnership(values []AggregateOwnership, capabilities map[string]Capability) error {
	want := approvedAggregateOwnership()
	if len(values) != len(want) {
		return fmt.Errorf("capability graph must declare the exact approved aggregate-owner matrix; got %d rows, want %d", len(values), len(want))
	}
	seen := map[string]struct{}{}
	for _, value := range values {
		if value.Record == "" || value.Owner == "" || value.CrossCapabilityRule == "" {
			return errors.New("every aggregate ownership row requires record, owner, and cross_capability_rule")
		}
		if _, ok := seen[value.Record]; ok {
			return fmt.Errorf("duplicate aggregate ownership row %q", value.Record)
		}
		seen[value.Record] = struct{}{}
		if !value.Mechanism {
			if _, ok := capabilities[value.Owner]; !ok && value.Owner != "read_projection" && value.Owner != "legacy_tombstone" {
				return fmt.Errorf("aggregate %s has unknown owner %q", value.Record, value.Owner)
			}
			if value.Discriminator != "" {
				return fmt.Errorf("aggregate %s declares a discriminator without mechanism=true", value.Record)
			}
		} else if value.Discriminator == "" {
			return fmt.Errorf("mechanism %s requires an owner discriminator mapping", value.Record)
		}
		identity, ok := want[value.Record]
		if !ok {
			return fmt.Errorf("aggregate ownership row %q is not in the approved matrix", value.Record)
		}
		if value.Owner != identity.owner || value.Mechanism != identity.mechanism || value.Discriminator != identity.discriminator {
			return fmt.Errorf("aggregate ownership row %q drifted: got owner=%q mechanism=%t discriminator=%q, want owner=%q mechanism=%t discriminator=%q",
				value.Record, value.Owner, value.Mechanism, value.Discriminator,
				identity.owner, identity.mechanism, identity.discriminator)
		}
	}
	return nil
}

type aggregateOwnershipIdentity struct {
	owner         string
	mechanism     bool
	discriminator string
}

func approvedAggregateOwnership() map[string]aggregateOwnershipIdentity {
	return map[string]aggregateOwnershipIdentity{
		"ActionLedger":             {owner: "execution"},
		"Activity, history, usage": {owner: "read_projection"},
		"Agent, Role, desired state, AgentOwnershipLease":  {owner: "agents"},
		"AgentSession, TerminalSession, AgentLease, inbox": {owner: "interaction"},
		"Artifact": {owner: "artifacts"},
		"Connector, Grant, secret and audit state": {owner: "connectors"},
		"Driver, DriverVersion, trust state":       {owner: "workflowcatalog"},
		"DriverRun, DriverStep, TaskRun, TaskRunEvent, Node, Worker, WorkerProfile, Await, lead-delivery Outbox": {owner: "execution"},
		"Generic fleet-db Lease": {
			owner: "fleet-db", mechanism: true,
			discriminator: "agent_service=agents;driver_run|task_run=execution;terminal=interaction;artifact_upload=artifacts",
		},
		"Issue, dependency, status, comment": {owner: "workitems"},
		"PlatformEvent and general mutation journal": {
			owner: "fleet-db", mechanism: true, discriminator: "mechanism_and_read_projection_only",
		},
		"TriggerBinding, Event, Delivery": {owner: "automation"},
		"WorkflowProcessState": {
			owner: "named_application_workflow", mechanism: true, discriminator: "app_workflow_name",
		},
		"Workspace, Repository":                      {owner: "workspace"},
		"Worktree, stack lineage, publication state": {owner: "sourcecontrol"},
	}
}

func validateLegacyPaths(values []LegacyPath, completedPhase int) error {
	if values == nil {
		return errors.New("capability graph must explicitly record legacy_paths, using [] after the final path is retired")
	}
	paths := make([]string, 0, len(values))
	for _, value := range values {
		if !safeInternalRoot(value.Path) || value.Owner == "" || value.RemovalIssue == "" || value.ExpiresAfterPhase < 2 {
			return fmt.Errorf("legacy path %q requires a safe path, owner, removal issue, and Phase 2-or-later expiry", value.Path)
		}
		if value.ExpiresAfterPhase <= completedPhase {
			return fmt.Errorf("legacy path %s expired after Phase %d but completed_phase is %d", value.Path, value.ExpiresAfterPhase, completedPhase)
		}
		if err := validateLegacyPathExtension(value); err != nil {
			return err
		}
		paths = append(paths, value.Path)
	}
	return validateSortedUnique("legacy path", paths)
}

func validateLegacyPathExtension(value LegacyPath) error {
	if value.Extension == nil {
		return nil
	}
	extension := value.Extension
	if extension.ReviewedBy == "" || extension.ReviewedAt == "" || extension.Rationale == "" ||
		len(extension.ReplacementAPIs) == 0 || len(extension.RemainingCallSites) == 0 {
		return fmt.Errorf("legacy path %s extension requires reviewer, date, rationale, replacement APIs, and remaining call sites", value.Path)
	}
	if err := validateSortedUnique("legacy path "+value.Path+" replacement API", extension.ReplacementAPIs); err != nil {
		return err
	}
	for _, replacement := range extension.ReplacementAPIs {
		if !safeInternalRoot(replacement) {
			return fmt.Errorf("legacy path %s replacement API %q must be a safe internal path", value.Path, replacement)
		}
	}
	if err := validateSortedUnique("legacy path "+value.Path+" remaining call site", extension.RemainingCallSites); err != nil {
		return err
	}
	for _, caller := range extension.RemainingCallSites {
		if !validRequiredGoSource(caller) {
			return fmt.Errorf("legacy path %s remaining call site %q must be a clean relative Go source path", value.Path, caller)
		}
	}
	return nil
}

func (m AnalysisMatrix) Validate() error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("analysis matrix schema_version: got %d, want %d", m.SchemaVersion, SchemaVersion)
	}
	if m.Status != "enforced" {
		return fmt.Errorf("analysis matrix status %q is unsupported", m.Status)
	}
	wantRelease := map[string]profileTuple{
		"darwin-amd64": {goos: "darwin", goarch: "amd64"},
		"darwin-arm64": {goos: "darwin", goarch: "arm64"},
		"linux-amd64":  {goos: "linux", goarch: "amd64"},
		"linux-arm64":  {goos: "linux", goarch: "arm64"},
	}
	wantTagged := map[string]profileTuple{
		"container":        {goos: "linux", goarch: "amd64", tags: []string{"container"}},
		"e2e":              {goos: "linux", goarch: "amd64", tags: []string{"e2e"}},
		"integration":      {goos: "linux", goarch: "amd64", tags: []string{"integration"}},
		"issuebackend-e2e": {goos: "linux", goarch: "amd64", tags: []string{"issuebackend_e2e"}},
		"playground":       {goos: "linux", goarch: "amd64", tags: []string{"playground"}},
		"race":             {goos: "linux", goarch: "amd64", race: true},
		"testbackend":      {goos: "linux", goarch: "amd64", tags: []string{"testbackend"}},
	}
	if err := validateProfiles("release", m.Release, wantRelease); err != nil {
		return err
	}
	if err := validateProfiles("tag", m.Tagged, wantTagged); err != nil {
		return err
	}
	for _, profile := range append(append([]AnalysisProfile{}, m.Release...), m.Tagged...) {
		if !profile.Enforced {
			return fmt.Errorf("analysis profile %s must be enforced for Phase 1 completion", profile.Name)
		}
	}
	if !m.AST.IncludeTests || !m.AST.ExcludeGenerated {
		return errors.New("AST all-files profile must include tests and exclude generated files")
	}
	for _, exception := range m.AST.Ignore {
		if exception.Path == "" || exception.Reason == "" || exception.Owner == "" || exception.Expiry == "" {
			return errors.New("every AST ignore exception requires path, reason, owner, and expiry")
		}
	}
	return nil
}

type profileTuple struct {
	goos   string
	goarch string
	tags   []string
	race   bool
}

func validateProfiles(label string, values []AnalysisProfile, want map[string]profileTuple) error {
	names := make([]string, 0, len(values))
	for _, value := range values {
		if err := validateProfile(label, value, want); err != nil {
			return err
		}
		names = append(names, value.Name)
	}
	slices.Sort(names)
	wantNames := make([]string, 0, len(want))
	for name := range want {
		wantNames = append(wantNames, name)
	}
	slices.Sort(wantNames)
	if !slices.Equal(names, wantNames) {
		return fmt.Errorf("%s profile names: got %v, want %v", label, names, wantNames)
	}
	return nil
}

func validateProfile(label string, value AnalysisProfile, want map[string]profileTuple) error {
	if value.Name == "" || value.GOOS == "" || value.GOARCH == "" {
		return fmt.Errorf("every %s profile requires name, goos, and goarch", label)
	}
	if !value.Enforced && (value.Owner == "" || value.AcceptanceCriteria == "") {
		return fmt.Errorf("deferred %s profile %s requires owner and acceptance_criteria", label, value.Name)
	}
	tuple, ok := want[value.Name]
	if !ok {
		return fmt.Errorf("unexpected %s profile %s", label, value.Name)
	}
	if value.GOOS != tuple.goos || value.GOARCH != tuple.goarch || value.Race != tuple.race || !slices.Equal(value.Tags, tuple.tags) {
		return fmt.Errorf("%s profile %s has tuple %s/%s tags=%v race=%t; want %s/%s tags=%v race=%t", label, value.Name, value.GOOS, value.GOARCH, value.Tags, value.Race, tuple.goos, tuple.goarch, tuple.tags, tuple.race)
	}
	if label == "tag" {
		return validateTaggedProfile(value)
	}
	if len(value.RequiredFiles) != 0 {
		return fmt.Errorf("release profile %s must not declare tagged source-selection sentinels", value.Name)
	}
	return nil
}

func validateTaggedProfile(value AnalysisProfile) error {
	if len(value.RequiredFiles) == 0 {
		return fmt.Errorf("tag profile %s requires at least one source-selection sentinel", value.Name)
	}
	if err := validateSortedUnique("tag profile "+value.Name+" required file", value.RequiredFiles); err != nil {
		return err
	}
	for _, required := range value.RequiredFiles {
		if !validRequiredGoSource(required) {
			return fmt.Errorf("tag profile %s required file %q must be a clean relative Go source path", value.Name, required)
		}
	}
	return nil
}

func validRequiredGoSource(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") || !strings.HasSuffix(path, ".go") {
		return false
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	return path == cleaned && path != "." && !strings.HasPrefix(path, "../")
}

func validateSortedUnique(label string, values []string) error {
	if !slices.IsSorted(values) {
		return fmt.Errorf("%s values must be sorted", label)
	}
	for i := 1; i < len(values); i++ {
		if values[i] == values[i-1] {
			return fmt.Errorf("duplicate %s %q", label, values[i])
		}
	}
	return nil
}

func synchronousCycle(edges []GraphEdge) []string {
	adjacency := map[string][]string{}
	for _, edge := range edges {
		synchronous := false
		for _, kind := range edge.Kinds {
			if kind == "import" || kind == "command" || kind == "query" {
				synchronous = true
				break
			}
		}
		if synchronous {
			adjacency[edge.From] = append(adjacency[edge.From], edge.To)
		}
	}
	walk := cycleWalk{adjacency: adjacency, state: map[string]uint8{}}
	for node := range adjacency {
		if walk.state[node] == 0 {
			if cycle := walk.visit(node); len(cycle) > 0 {
				return cycle
			}
		}
	}
	return nil
}

type cycleWalk struct {
	adjacency map[string][]string
	state     map[string]uint8
	stack     []string
}

func (w *cycleWalk) visit(node string) []string {
	w.state[node] = 1
	w.stack = append(w.stack, node)
	for _, next := range w.adjacency[node] {
		if w.state[next] == 0 {
			if cycle := w.visit(next); len(cycle) > 0 {
				return cycle
			}
		}
		if w.state[next] == 1 {
			return w.cycleFrom(next)
		}
	}
	w.stack = w.stack[:len(w.stack)-1]
	w.state[node] = 2
	return nil
}

func (w *cycleWalk) cycleFrom(node string) []string {
	for i, candidate := range w.stack {
		if candidate == node {
			return append(append([]string{}, w.stack[i:]...), node)
		}
	}
	return nil
}
