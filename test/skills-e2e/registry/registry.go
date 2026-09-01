// Package registry validates coverage declared beside executable tests and
// renders the owning repository's versioned, generated coverage report.
package registry

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	SchemaVersion                 = "skills-edge-coverage/v1"
	MinEdgeCaseID                 = 1
	MaxEdgeCaseID                 = 95
	FirstStrictCutoverExclusionID = 72
	LastStrictCutoverExclusionID  = 77
)

var scenarioIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var executedCoverage coverageRecorder

type Repository string

const (
	RepositoryLoom  Repository = "loom"
	RepositoryFleet Repository = "fleet"
)

type Owner string

const (
	OwnerLoom  Owner = "loom"
	OwnerFleet Owner = "fleet"
)

type Seam string

const (
	SeamLoomDomain   Seam = "loom-domain"
	SeamFleetDomain  Seam = "fleet-domain"
	SeamLoomFleetE2E Seam = "loom-fleet-e2e"
	SeamReleaseCI    Seam = "release-ci"
)

type ExclusionDecision string

const StrictCutoverNoMigration ExclusionDecision = "strict-cutover-no-migration"

// Report contains only cases covered by passing executable tests in one
// owning repository. A partial report is valid but does not claim readiness.
type Report struct {
	SchemaVersion string         `json:"schema_version" yaml:"schema_version"`
	Repository    Repository     `json:"repository" yaml:"repository"`
	Revision      string         `json:"revision" yaml:"revision"`
	Cases         []CaseCoverage `json:"cases" yaml:"cases"`
}

type CaseCoverage struct {
	ID        int      `json:"id" yaml:"id"`
	Behavior  string   `json:"behavior" yaml:"behavior"`
	Owner     Owner    `json:"owner" yaml:"owner"`
	Seam      Seam     `json:"seam" yaml:"seam"`
	Test      string   `json:"test" yaml:"test"`
	Backends  []string `json:"backends,omitempty" yaml:"backends,omitempty"`
	Providers []string `json:"providers,omitempty" yaml:"providers,omitempty"`
}

type Exclusion struct {
	ID        int               `json:"id" yaml:"id"`
	Decision  ExclusionDecision `json:"decision" yaml:"decision"`
	Rationale string            `json:"rationale" yaml:"rationale"`
}

// EdgeCase keeps canonical metadata beside its executable scenario. Rationale
// documents why that scenario is sufficient but is not repeated in the wire report.
type EdgeCase struct {
	ID        int
	Behavior  string
	Rationale string
}

// Scenario describes one readable public E2E journey. A scenario may be a
// useful regression without claiming any canonical 1-95 case IDs.
type Scenario struct {
	ID        string
	Behavior  string
	Test      string
	Owner     Owner
	Seam      Seam
	Backends  []string
	Providers []string
	Cases     []EdgeCase
}

type coverageRecorder struct {
	mu        sync.Mutex
	scenarios []Scenario
}

type coverageTest interface {
	Helper()
	Name() string
	Failed() bool
	Cleanup(func())
	Fatalf(string, ...any)
	Errorf(string, ...any)
}

// Covers binds metadata to the top-level test and records it only after the
// complete test passes.
func (s Scenario) Covers(t *testing.T) {
	t.Helper()
	executedCoverage.covers(t, s)
}

func (r *coverageRecorder) covers(t coverageTest, scenario Scenario) {
	t.Helper()
	scenario.Test = t.Name()
	if err := validateScenario(scenario); err != nil {
		t.Fatalf("invalid scenario %q: %v", scenario.ID, err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			return
		}
		if err := r.record(scenario); err != nil {
			t.Errorf("record E2E coverage: %v", err)
		}
	})
}

func (r *coverageRecorder) record(scenario Scenario) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	updated := append(slices.Clone(r.scenarios), scenario)
	if err := ValidateScenarios(updated); err != nil {
		return err
	}
	r.scenarios = updated
	return nil
}

func (r *coverageRecorder) snapshot() []Scenario {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.scenarios)
}

func (r *coverageRecorder) report(revision string) (Report, error) {
	report := Report{SchemaVersion: SchemaVersion, Repository: RepositoryLoom, Revision: revision}
	for _, scenario := range r.snapshot() {
		for _, edgeCase := range scenario.Cases {
			report.Cases = append(report.Cases, CaseCoverage{
				ID: edgeCase.ID, Behavior: edgeCase.Behavior,
				Owner: scenario.Owner, Seam: scenario.Seam, Test: scenario.Test,
				Backends: slices.Clone(scenario.Backends), Providers: slices.Clone(scenario.Providers),
			})
		}
	}
	sort.Slice(report.Cases, func(i, j int) bool { return report.Cases[i].ID < report.Cases[j].ID })
	if err := ValidateReport(report); err != nil {
		return Report{}, err
	}
	return report, nil
}

// WriteCoverageFile generates the report from passing tests. The harness sets
// SKILLS_EDGE_REVISION to the exact Loom SHA printed in its evidence banner.
func WriteCoverageFile(path string) error {
	report, err := executedCoverage.report(os.Getenv("SKILLS_EDGE_REVISION"))
	if err != nil {
		return err
	}
	var output strings.Builder
	if err := WriteYAML(&output, report); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(output.String()), 0o644)
}

// WriteYAML is output-only; YAML is never the authored coverage source.
func WriteYAML(w io.Writer, report Report) error {
	if err := ValidateReport(report); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "# Code generated by skills edge coverage; DO NOT EDIT.\n"); err != nil {
		return err
	}
	encoder := yaml.NewEncoder(w)
	encoder.SetIndent(2)
	if err := encoder.Encode(report); err != nil {
		return err
	}
	return encoder.Close()
}

func ValidateScenarios(scenarios []Scenario) error {
	seenScenarios := make(map[string]struct{}, len(scenarios))
	caseOwners := make(map[int]string)
	for _, scenario := range scenarios {
		if err := validateScenario(scenario); err != nil {
			return fmt.Errorf("scenario %q: %w", scenario.ID, err)
		}
		if _, duplicate := seenScenarios[scenario.ID]; duplicate {
			return fmt.Errorf("duplicate scenario id %q", scenario.ID)
		}
		seenScenarios[scenario.ID] = struct{}{}
		for _, edgeCase := range scenario.Cases {
			if owner, duplicate := caseOwners[edgeCase.ID]; duplicate {
				return fmt.Errorf("edge case %d is owned by both %q and %q", edgeCase.ID, owner, scenario.ID)
			}
			caseOwners[edgeCase.ID] = scenario.ID
		}
	}
	return nil
}

func validateScenario(s Scenario) error {
	if !scenarioIDPattern.MatchString(s.ID) {
		return fmt.Errorf("id must be lowercase kebab-case")
	}
	if strings.TrimSpace(s.Behavior) == "" {
		return fmt.Errorf("behavior is required")
	}
	if !strings.HasPrefix(s.Test, "Test") {
		return fmt.Errorf("top-level Go test is required")
	}
	if s.Owner != OwnerLoom && s.Owner != OwnerFleet {
		return fmt.Errorf("invalid owner %q", s.Owner)
	}
	if !validSeam(s.Seam) {
		return fmt.Errorf("invalid seam %q", s.Seam)
	}
	requiresMatrix := s.Seam == SeamLoomFleetE2E
	if err := validateDimension("backend", s.Backends, map[string]bool{"redis": true, "postgres": true}, requiresMatrix); err != nil {
		return err
	}
	if err := validateDimension("provider", s.Providers, map[string]bool{"local": true, "minio": true, "gcs": true}, requiresMatrix); err != nil {
		return err
	}
	seenCases := make(map[int]struct{}, len(s.Cases))
	for _, edgeCase := range s.Cases {
		if err := validateCaseID(edgeCase.ID); err != nil {
			return err
		}
		if strings.TrimSpace(edgeCase.Behavior) == "" || strings.TrimSpace(edgeCase.Rationale) == "" {
			return fmt.Errorf("edge case %d behavior and rationale are required", edgeCase.ID)
		}
		if _, duplicate := seenCases[edgeCase.ID]; duplicate {
			return fmt.Errorf("duplicate edge case %d", edgeCase.ID)
		}
		seenCases[edgeCase.ID] = struct{}{}
	}
	return nil
}

func validateDimension(label string, values []string, allowed map[string]bool, required bool) error {
	if required && len(values) == 0 {
		return fmt.Errorf("at least one %s is required", label)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !allowed[value] {
			return fmt.Errorf("unknown %s %q", label, value)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("duplicate %s %q", label, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

// ValidateReport checks one owning repository's generated partial report.
func ValidateReport(report Report) error {
	if report.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %q; want %q", report.SchemaVersion, SchemaVersion)
	}
	if report.Repository != RepositoryLoom && report.Repository != RepositoryFleet {
		return fmt.Errorf("repository %q must be loom or fleet", report.Repository)
	}
	if strings.TrimSpace(report.Revision) == "" {
		return fmt.Errorf("revision is required")
	}
	seen := make(map[int]struct{}, len(report.Cases))
	for index, coverage := range report.Cases {
		if err := validateCoverage(coverage); err != nil {
			return fmt.Errorf("cases[%d]: %w", index, err)
		}
		if coverage.Owner != Owner(report.Repository) {
			return fmt.Errorf("cases[%d]: owner %q does not match repository %q", index, coverage.Owner, report.Repository)
		}
		if _, duplicate := seen[coverage.ID]; duplicate {
			return fmt.Errorf("duplicate edge-case ID %d", coverage.ID)
		}
		seen[coverage.ID] = struct{}{}
	}
	return nil
}

func validateCoverage(coverage CaseCoverage) error {
	if err := validateCaseID(coverage.ID); err != nil {
		return err
	}
	if strings.TrimSpace(coverage.Behavior) == "" {
		return fmt.Errorf("edge-case ID %d behavior is required", coverage.ID)
	}
	if coverage.Owner != OwnerLoom && coverage.Owner != OwnerFleet {
		return fmt.Errorf("edge-case ID %d has invalid owner %q", coverage.ID, coverage.Owner)
	}
	if !validSeam(coverage.Seam) {
		return fmt.Errorf("edge-case ID %d has invalid seam %q", coverage.ID, coverage.Seam)
	}
	if strings.TrimSpace(coverage.Test) == "" {
		return fmt.Errorf("edge-case ID %d test is required", coverage.ID)
	}
	if err := validateDimensions("backend", coverage.Backends); err != nil {
		return fmt.Errorf("edge-case ID %d: %w", coverage.ID, err)
	}
	if err := validateDimensions("provider", coverage.Providers); err != nil {
		return fmt.Errorf("edge-case ID %d: %w", coverage.ID, err)
	}
	return nil
}

func validateDimensions(name string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("%s dimension must not be empty", name)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("duplicate %s dimension %q", name, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

// ValidateReady requires the exact 1-95 union across paired reports and the
// six accepted strict-cutover exclusions. It is not a per-repository gate.
func ValidateReady(reports []Report, exclusions []Exclusion) error {
	seenRepositories := make(map[Repository]struct{}, len(reports))
	seenCases := make(map[int]string, MaxEdgeCaseID)
	for _, report := range reports {
		if err := ValidateReport(report); err != nil {
			return fmt.Errorf("%s report: %w", report.Repository, err)
		}
		if _, duplicate := seenRepositories[report.Repository]; duplicate {
			return fmt.Errorf("duplicate repository report %q", report.Repository)
		}
		seenRepositories[report.Repository] = struct{}{}
		for _, coverage := range report.Cases {
			if previous, duplicate := seenCases[coverage.ID]; duplicate {
				return fmt.Errorf("duplicate edge-case ID %d in %s and %s", coverage.ID, previous, report.Repository)
			}
			seenCases[coverage.ID] = string(report.Repository)
		}
	}
	for _, repository := range []Repository{RepositoryLoom, RepositoryFleet} {
		if _, present := seenRepositories[repository]; !present {
			return fmt.Errorf("missing repository report %q", repository)
		}
	}
	for index, exclusion := range exclusions {
		if err := validateCaseID(exclusion.ID); err != nil {
			return fmt.Errorf("exclusions[%d]: %w", index, err)
		}
		if previous, duplicate := seenCases[exclusion.ID]; duplicate {
			return fmt.Errorf("duplicate edge-case ID %d in %s and exclusions", exclusion.ID, previous)
		}
		if exclusion.ID < FirstStrictCutoverExclusionID || exclusion.ID > LastStrictCutoverExclusionID {
			return fmt.Errorf("edge-case ID %d cannot be excluded", exclusion.ID)
		}
		if exclusion.Decision != StrictCutoverNoMigration {
			return fmt.Errorf("edge-case ID %d has invalid exclusion decision %q", exclusion.ID, exclusion.Decision)
		}
		if strings.TrimSpace(exclusion.Rationale) == "" {
			return fmt.Errorf("edge-case ID %d exclusion rationale is required", exclusion.ID)
		}
		seenCases[exclusion.ID] = "exclusions"
	}
	missing := make([]int, 0)
	for id := MinEdgeCaseID; id <= MaxEdgeCaseID; id++ {
		if _, present := seenCases[id]; !present {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing edge-case IDs: %s", joinIDs(missing))
	}
	return nil
}

func validateCaseID(id int) error {
	if id < MinEdgeCaseID || id > MaxEdgeCaseID {
		return fmt.Errorf("edge-case ID %d is outside %d-%d", id, MinEdgeCaseID, MaxEdgeCaseID)
	}
	return nil
}

func validSeam(seam Seam) bool {
	switch seam {
	case SeamLoomDomain, SeamFleetDomain, SeamLoomFleetE2E, SeamReleaseCI:
		return true
	default:
		return false
	}
}

func joinIDs(ids []int) string {
	sort.Ints(ids)
	parts := make([]string, len(ids))
	for index, id := range ids {
		parts[index] = fmt.Sprint(id)
	}
	return strings.Join(parts, ",")
}
