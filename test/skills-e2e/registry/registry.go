// Package registry binds canonical case IDs to passing executable tests and
// produces generated evidence shards. Canonical semantics live in catalog_v2.
package registry

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

const (
	MinEdgeCaseID                 = 1
	MaxEdgeCaseID                 = 95
	FirstStrictCutoverExclusionID = 72
	LastStrictCutoverExclusionID  = 77
	loomE2EPackage                = "github.com/tysonthomas9/loomcli/test/skills-e2e"
)

var scenarioIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var executedCoverage coverageRecorder

type Repository string

const (
	RepositoryLoom  Repository = "loom"
	RepositoryFleet Repository = "fleet"
)

// EdgeCase is only a catalog reference; canonical facts never live here.
type EdgeCase struct {
	ID int
}

// Scenario stays readable without becoming a second semantic ledger.
type Scenario struct {
	ID       string
	Behavior string
	Test     string
	Cases    []EdgeCase
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
		if !t.Failed() {
			if err := r.record(scenario); err != nil {
				t.Errorf("record E2E evidence: %v", err)
			}
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

func (r *coverageRecorder) report(revision string, backend Backend, provider Provider) (EvidenceReport, error) {
	report := EvidenceReport{SchemaVersion: EvidenceSchemaVersion, Repository: RepositoryLoom, Revision: revision}
	for _, scenario := range r.snapshot() {
		for _, edgeCase := range scenario.Cases {
			report.Evidence = append(report.Evidence, Evidence{ID: edgeCase.ID, Package: loomE2EPackage, Test: scenario.Test, Backend: backend, Provider: provider})
		}
	}
	sort.Slice(report.Evidence, func(i, j int) bool {
		if report.Evidence[i].ID != report.Evidence[j].ID {
			return report.Evidence[i].ID < report.Evidence[j].ID
		}
		return report.Evidence[i].Test < report.Evidence[j].Test
	})
	if err := ValidateEvidenceReport(report); err != nil {
		return EvidenceReport{}, err
	}
	return report, nil
}

// WriteCoverageFile derives coordinates from the actual compatibility process.
func WriteCoverageFile(path string) error {
	backend, provider, err := RuntimeCoordinatesFromEnv()
	if err != nil {
		return err
	}
	report, err := executedCoverage.report(os.Getenv("SKILLS_EDGE_REVISION"), backend, provider)
	if err != nil {
		return err
	}
	// #nosec G304 -- path is the caller-selected generated evidence artifact.
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	writeErr := WriteEvidenceReport(file, report)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func RuntimeCoordinatesFromEnv() (Backend, Provider, error) {
	backend := Backend(os.Getenv("FLEET_E2E_BACKEND"))
	provider := Provider(os.Getenv("SKILLS_E2E_PROVIDER"))
	if provider == "" {
		switch os.Getenv("STORAGE_MODE") {
		case "local":
			provider = ProviderLocal
		case "s3":
			provider = ProviderMinIO
		}
	}
	if err := validateCoordinate(EvidenceCoordinate{Repository: RepositoryLoom, Backend: backend, Provider: provider}); err != nil {
		return "", "", err
	}
	return backend, provider, nil
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
				return fmt.Errorf("edge case %d is referenced by both %q and %q", edgeCase.ID, owner, scenario.ID)
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
	seen := make(map[int]struct{}, len(s.Cases))
	for _, edgeCase := range s.Cases {
		if err := validateCaseID(edgeCase.ID); err != nil {
			return err
		}
		if _, duplicate := seen[edgeCase.ID]; duplicate {
			return fmt.Errorf("duplicate edge case %d", edgeCase.ID)
		}
		seen[edgeCase.ID] = struct{}{}
	}
	return nil
}

func validateCaseID(id int) error {
	if id < MinEdgeCaseID || id > MaxEdgeCaseID {
		return fmt.Errorf("edge-case ID %d is outside %d-%d", id, MinEdgeCaseID, MaxEdgeCaseID)
	}
	return nil
}
