package registry

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const EvidenceSchemaVersion = "skills-edge-evidence/v2"

type Backend string

const (
	BackendRedis    Backend = "redis"
	BackendPostgres Backend = "postgres"
)

type Provider string

const (
	ProviderLocal Provider = "local"
	ProviderMinIO Provider = "minio"
	ProviderGCS   Provider = "gcs"
)

type Decision string

const (
	DecisionApplicable    Decision = "applicable"
	DecisionNotApplicable Decision = "not_applicable"
)

// EvidenceReport is one repository/revision shard of observed passing tests.
// It intentionally contains no canonical behavior, seam, or intended matrix.
type EvidenceReport struct {
	SchemaVersion string     `json:"schema_version"`
	Repository    Repository `json:"repository"`
	Revision      string     `json:"revision"`
	Evidence      []Evidence `json:"evidence"`
}

// Evidence records an actual execution coordinate. Empty backend/provider are
// meaningful: the owning test does not depend on that dimension.
type Evidence struct {
	ID       int      `json:"id"`
	Package  string   `json:"package"`
	Test     string   `json:"test"`
	Backend  Backend  `json:"backend,omitempty"`
	Provider Provider `json:"provider,omitempty"`
}

type EvidenceCoordinate struct {
	Repository Repository `json:"repository"`
	Backend    Backend    `json:"backend,omitempty"`
	Provider   Provider   `json:"provider,omitempty"`
}

type CaseDefinition struct {
	ID               int                  `json:"id"`
	Behavior         string               `json:"behavior"`
	Owner            string               `json:"owner"`
	Seam             string               `json:"seam"`
	RequiredEvidence []EvidenceCoordinate `json:"required_evidence,omitempty"`
	Decision         Decision             `json:"decision"`
	Rationale        string               `json:"rationale,omitempty"`
}

type ReadinessResult struct {
	Ready         bool               `json:"ready"`
	Evidence      []VerifiedEvidence `json:"evidence"`
	NotApplicable []CaseDefinition   `json:"not_applicable"`
	Missing       []MissingEvidence  `json:"missing"`
}

type VerifiedEvidence struct {
	Repository Repository `json:"repository"`
	Evidence
}

type MissingEvidence struct {
	ID       int                `json:"id"`
	Required EvidenceCoordinate `json:"required"`
}

func DecodeEvidenceReport(r io.Reader) (EvidenceReport, error) {
	var report EvidenceReport
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return EvidenceReport{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return EvidenceReport{}, fmt.Errorf("evidence report must contain exactly one JSON value")
	}
	if err := ValidateEvidenceReport(report); err != nil {
		return EvidenceReport{}, err
	}
	return report, nil
}

func WriteEvidenceReport(w io.Writer, report EvidenceReport) error {
	if err := ValidateEvidenceReport(report); err != nil {
		return err
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func ValidateEvidenceReport(report EvidenceReport) error {
	if report.SchemaVersion != EvidenceSchemaVersion {
		return fmt.Errorf("unsupported schema_version %q; want %q", report.SchemaVersion, EvidenceSchemaVersion)
	}
	if !validRepository(report.Repository) {
		return fmt.Errorf("repository %q must be loom or fleet", report.Repository)
	}
	if strings.TrimSpace(report.Revision) == "" {
		return fmt.Errorf("revision is required")
	}
	seen := make(map[string]struct{}, len(report.Evidence))
	for index, item := range report.Evidence {
		if err := validateEvidence(item); err != nil {
			return fmt.Errorf("evidence[%d]: %w", index, err)
		}
		key := evidenceKey(report.Repository, item)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("evidence[%d]: duplicate evidence for case %d at %s", index, item.ID, coordinateLabel(EvidenceCoordinate{Repository: report.Repository, Backend: item.Backend, Provider: item.Provider}))
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateEvidence(item Evidence) error {
	if err := validateCaseID(item.ID); err != nil {
		return err
	}
	if strings.TrimSpace(item.Package) == "" {
		return fmt.Errorf("case %d package is required", item.ID)
	}
	if strings.TrimSpace(item.Test) == "" {
		return fmt.Errorf("case %d test is required", item.ID)
	}
	if strings.Contains(item.Test, "/") {
		return fmt.Errorf("case %d evidence must name a top-level test, got %q", item.ID, item.Test)
	}
	if item.Backend != "" && item.Backend != BackendRedis && item.Backend != BackendPostgres {
		return fmt.Errorf("case %d has invalid backend %q", item.ID, item.Backend)
	}
	if item.Provider != "" && item.Provider != ProviderLocal && item.Provider != ProviderMinIO && item.Provider != ProviderGCS {
		return fmt.Errorf("case %d has invalid provider %q", item.ID, item.Provider)
	}
	return nil
}

func ValidateReadiness(catalog []CaseDefinition, reports []EvidenceReport) (ReadinessResult, error) {
	if err := validateCatalog(catalog); err != nil {
		return ReadinessResult{}, err
	}
	revisions := make(map[Repository]string)
	definitions := make(map[int]CaseDefinition, len(catalog))
	for _, definition := range catalog {
		definitions[definition.ID] = definition
	}
	var result ReadinessResult
	seen := make(map[string]struct{})
	for _, report := range reports {
		if err := ValidateEvidenceReport(report); err != nil {
			return ReadinessResult{}, err
		}
		if revision, ok := revisions[report.Repository]; ok && revision != report.Revision {
			return ReadinessResult{}, fmt.Errorf("repository %s has conflicting revisions %q and %q", report.Repository, revision, report.Revision)
		}
		revisions[report.Repository] = report.Revision
		for _, item := range report.Evidence {
			definition := definitions[item.ID]
			if definition.Decision != DecisionApplicable {
				return ReadinessResult{}, fmt.Errorf("case %d is %s and cannot have evidence", item.ID, definition.Decision)
			}
			key := evidenceKey(report.Repository, item)
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			result.Evidence = append(result.Evidence, VerifiedEvidence{Repository: report.Repository, Evidence: item})
		}
	}

	for _, definition := range catalog {
		if definition.Decision == DecisionNotApplicable {
			result.NotApplicable = append(result.NotApplicable, definition)
			continue
		}
		for _, required := range definition.RequiredEvidence {
			if !hasCoordinate(result.Evidence, definition.ID, required) {
				result.Missing = append(result.Missing, MissingEvidence{ID: definition.ID, Required: required})
			}
		}
	}
	result.Ready = len(result.Missing) == 0
	sort.Slice(result.Evidence, func(i, j int) bool {
		if result.Evidence[i].ID != result.Evidence[j].ID {
			return result.Evidence[i].ID < result.Evidence[j].ID
		}
		return evidenceKey(result.Evidence[i].Repository, result.Evidence[i].Evidence) < evidenceKey(result.Evidence[j].Repository, result.Evidence[j].Evidence)
	})
	return result, nil
}

func validateCatalog(catalog []CaseDefinition) error {
	seen := make(map[int]struct{}, len(catalog))
	for _, definition := range catalog {
		if err := validateCaseID(definition.ID); err != nil {
			return fmt.Errorf("catalog: %w", err)
		}
		if _, duplicate := seen[definition.ID]; duplicate {
			return fmt.Errorf("catalog duplicate edge-case ID %d", definition.ID)
		}
		seen[definition.ID] = struct{}{}
		if strings.TrimSpace(definition.Behavior) == "" || strings.TrimSpace(definition.Owner) == "" || strings.TrimSpace(definition.Seam) == "" {
			return fmt.Errorf("case %d behavior, owner, and seam are required", definition.ID)
		}
		if definition.Owner != "loom" && definition.Owner != "fleet" && definition.Owner != "shared" && definition.Owner != "none" {
			return fmt.Errorf("case %d has invalid owner %q", definition.ID, definition.Owner)
		}
		if definition.Decision == DecisionNotApplicable {
			if definition.ID < FirstStrictCutoverExclusionID || definition.ID > LastStrictCutoverExclusionID {
				return fmt.Errorf("case %d cannot be not_applicable", definition.ID)
			}
			if strings.TrimSpace(definition.Rationale) == "" {
				return fmt.Errorf("case %d not_applicable rationale is required", definition.ID)
			}
			if len(definition.RequiredEvidence) != 0 {
				return fmt.Errorf("case %d not_applicable cannot require evidence", definition.ID)
			}
		} else if definition.Decision != DecisionApplicable {
			return fmt.Errorf("case %d has invalid decision %q", definition.ID, definition.Decision)
		} else if len(definition.RequiredEvidence) == 0 {
			return fmt.Errorf("case %d applicable requires evidence", definition.ID)
		}
		coordSeen := make(map[EvidenceCoordinate]struct{}, len(definition.RequiredEvidence))
		for _, coordinate := range definition.RequiredEvidence {
			if err := validateCoordinate(coordinate); err != nil {
				return fmt.Errorf("case %d: %w", definition.ID, err)
			}
			if _, duplicate := coordSeen[coordinate]; duplicate {
				return fmt.Errorf("case %d duplicate required evidence %s", definition.ID, coordinateLabel(coordinate))
			}
			coordSeen[coordinate] = struct{}{}
		}
	}
	for id := MinEdgeCaseID; id <= MaxEdgeCaseID; id++ {
		if _, ok := seen[id]; !ok {
			return fmt.Errorf("catalog missing edge-case ID %d", id)
		}
	}
	return nil
}

func validateCoordinate(coordinate EvidenceCoordinate) error {
	if !validRepository(coordinate.Repository) {
		return fmt.Errorf("invalid repository %q", coordinate.Repository)
	}
	if coordinate.Backend != "" && coordinate.Backend != BackendRedis && coordinate.Backend != BackendPostgres {
		return fmt.Errorf("invalid backend %q", coordinate.Backend)
	}
	if coordinate.Provider != "" && coordinate.Provider != ProviderLocal && coordinate.Provider != ProviderMinIO && coordinate.Provider != ProviderGCS {
		return fmt.Errorf("invalid provider %q", coordinate.Provider)
	}
	return nil
}

func validRepository(repository Repository) bool {
	return repository == RepositoryLoom || repository == RepositoryFleet
}

func hasCoordinate(evidence []VerifiedEvidence, id int, required EvidenceCoordinate) bool {
	for _, item := range evidence {
		if item.ID == id && item.Repository == required.Repository &&
			(required.Backend == "" || item.Backend == required.Backend) &&
			(required.Provider == "" || item.Provider == required.Provider) {
			return true
		}
	}
	return false
}

func evidenceKey(repository Repository, item Evidence) string {
	return fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%s\x00%s", repository, item.ID, item.Package, item.Test, item.Backend, item.Provider)
}

func coordinateLabel(c EvidenceCoordinate) string {
	backend, provider := string(c.Backend), string(c.Provider)
	if backend == "" {
		backend = "-"
	}
	if provider == "" {
		provider = "-"
	}
	return fmt.Sprintf("%s/%s/%s", c.Repository, backend, provider)
}
