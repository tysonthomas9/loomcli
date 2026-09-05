package registry

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestFleetGoldenEvidenceV2DecodesAndValidates(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/fleet-evidence-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var report EvidenceReport
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEvidenceReport(report); err != nil {
		t.Fatalf("Fleet golden report: %v", err)
	}
	if got := len(report.Evidence); got != 2 {
		t.Fatalf("Fleet golden evidence = %d, want 2", got)
	}
	var encoded bytes.Buffer
	if err := WriteEvidenceReport(&encoded, report); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded.Bytes(), body) {
		t.Fatalf("Fleet golden wire drifted:\n%s", encoded.String())
	}
}

func TestValidateReadinessMergesShardEvidenceByExplicitCoordinate(t *testing.T) {
	t.Parallel()
	catalog := CanonicalCatalog()
	reports := completeEvidenceReports(catalog)
	merged, err := ValidateReadiness(catalog, reports)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(merged.Evidence), requiredEvidenceCount(catalog); got != want {
		t.Fatalf("merged evidence = %d, want %d", got, want)
	}
}

func TestValidateReadinessTreatsEmptyRequiredDimensionsAsWildcards(t *testing.T) {
	t.Parallel()
	catalog := CanonicalCatalog()
	reports := completeEvidenceReports(catalog)
	for reportIndex := range reports {
		for evidenceIndex := range reports[reportIndex].Evidence {
			if reports[reportIndex].Evidence[evidenceIndex].ID == 4 {
				reports[reportIndex].Evidence[evidenceIndex].Backend = BackendRedis
				reports[reportIndex].Evidence[evidenceIndex].Provider = ProviderLocal
			}
		}
	}
	if _, err := ValidateReadiness(catalog, reports); err != nil {
		t.Fatal(err)
	}
}

func TestValidateReadinessRejectsMissingRequiredCoordinate(t *testing.T) {
	t.Parallel()
	catalog := CanonicalCatalog()
	reports := completeEvidenceReports(catalog)
	for reportIndex := range reports {
		items := reports[reportIndex].Evidence[:0]
		for _, item := range reports[reportIndex].Evidence {
			if item.ID != 50 || item.Backend != BackendPostgres {
				items = append(items, item)
			}
		}
		reports[reportIndex].Evidence = items
	}
	result, err := ValidateReadiness(catalog, reports)
	if err != nil {
		t.Fatal(err)
	}
	if result.Ready || len(result.Missing) == 0 {
		t.Fatalf("result = %#v, want missing PostgreSQL coordinate", result)
	}
	missing := result.Missing[0]
	if missing.ID != 50 || missing.Required != (EvidenceCoordinate{Repository: RepositoryFleet, Backend: BackendPostgres}) {
		t.Fatalf("first missing = %#v", missing)
	}
}

func TestValidateReadinessRejectsInvalidCatalogAndEvidence(t *testing.T) {
	t.Parallel()
	t.Run("catalog gap", func(t *testing.T) {
		_, err := ValidateReadiness(CanonicalCatalog()[1:], nil)
		if err == nil || !strings.Contains(err.Error(), "catalog missing edge-case ID 1") {
			t.Fatalf("ValidateReadiness error = %v, want catalog gap", err)
		}
	})
	t.Run("catalog duplicate", func(t *testing.T) {
		catalog := CanonicalCatalog()
		catalog[1].ID = 1
		_, err := ValidateReadiness(catalog, nil)
		if err == nil || !strings.Contains(err.Error(), "duplicate edge-case ID 1") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("catalog out of range", func(t *testing.T) {
		catalog := CanonicalCatalog()
		catalog[0].ID = 0
		_, err := ValidateReadiness(catalog, nil)
		if err == nil || !strings.Contains(err.Error(), "outside 1-95") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("not applicable outside cutover", func(t *testing.T) {
		catalog := CanonicalCatalog()
		catalog[70].Decision = DecisionNotApplicable
		catalog[70].RequiredEvidence = nil
		catalog[70].Rationale = "wrong"
		_, err := ValidateReadiness(catalog, nil)
		if err == nil || !strings.Contains(err.Error(), "case 71 cannot be not_applicable") {
			t.Fatalf("ValidateReadiness error = %v, want invalid N/A", err)
		}
	})
	t.Run("generic provider", func(t *testing.T) {
		report := validEvidenceReport(RepositoryFleet, "fleet-sha", Evidence{
			ID: 29, Package: "api", Test: "TestS3", Provider: Provider("s3"),
		})
		if err := ValidateEvidenceReport(report); err == nil || !strings.Contains(err.Error(), "invalid provider") {
			t.Fatalf("ValidateEvidenceReport error = %v, want invalid provider", err)
		}
	})
	t.Run("subtest evidence", func(t *testing.T) {
		report := validEvidenceReport(RepositoryFleet, "fleet-sha", Evidence{ID: 50, Package: "storage", Test: "TestPublish/redis", Backend: BackendRedis})
		if err := ValidateEvidenceReport(report); err == nil || !strings.Contains(err.Error(), "top-level") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("duplicate report evidence", func(t *testing.T) {
		item := Evidence{ID: 50, Package: "storage", Test: "TestPublish", Backend: BackendRedis}
		report := validEvidenceReport(RepositoryFleet, "fleet-sha", item, item)
		if err := ValidateEvidenceReport(report); err == nil || !strings.Contains(err.Error(), "duplicate evidence") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("out of range report evidence", func(t *testing.T) {
		report := validEvidenceReport(RepositoryFleet, "fleet-sha", Evidence{ID: 96, Package: "storage", Test: "TestPublish"})
		if err := ValidateEvidenceReport(report); err == nil || !strings.Contains(err.Error(), "outside 1-95") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestCanonicalCatalogHasOnlyStrictCutoverNotApplicableCases(t *testing.T) {
	t.Parallel()
	var got []int
	for _, definition := range CanonicalCatalog() {
		if definition.Decision == DecisionNotApplicable {
			got = append(got, definition.ID)
		}
	}
	want := []int{72, 73, 74, 75, 76, 77}
	if len(got) != len(want) {
		t.Fatalf("not applicable IDs = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("not applicable IDs = %v, want %v", got, want)
		}
	}
}

func TestCanonicalCatalogPinsSecurityWordingAndSelectedCoordinates(t *testing.T) {
	t.Parallel()
	catalog := CanonicalCatalog()
	if !strings.Contains(catalog[35].Behavior, "wrong-version") {
		t.Fatalf("case 36 wording = %q", catalog[35].Behavior)
	}
	wantLifecycle := []EvidenceCoordinate{loomMinIO(), loomPostgresMinIO()}
	if got := catalog[0].RequiredEvidence; !slices.Equal(got, wantLifecycle) {
		t.Fatalf("case 1 coordinates = %#v, want %#v", got, wantLifecycle)
	}
	if got := catalog[63].RequiredEvidence; !slices.Equal(got, []EvidenceCoordinate{loomMinIO()}) {
		t.Fatalf("case 64 coordinates = %#v", got)
	}
	if got := catalog[80].RequiredEvidence; !slices.Equal(got, []EvidenceCoordinate{loomMinIO(), loomGCS()}) {
		t.Fatalf("case 81 coordinates = %#v", got)
	}
	retentionParity := []EvidenceCoordinate{redisFleet(), postgresFleet()}
	for _, id := range []int{85, 86, 87} {
		if got := catalog[id-1].RequiredEvidence; !slices.Equal(got, retentionParity) {
			t.Fatalf("case %d coordinates = %#v, want %#v", id, got, retentionParity)
		}
	}
	if got, want := requiredEvidenceCount(catalog), 141; got != want {
		t.Fatalf("required evidence coordinates = %d, want %d", got, want)
	}
}

func validEvidenceReport(repository Repository, revision string, evidence ...Evidence) EvidenceReport {
	return EvidenceReport{SchemaVersion: EvidenceSchemaVersion, Repository: repository, Revision: revision, Evidence: evidence}
}

func completeEvidenceReports(catalog []CaseDefinition) []EvidenceReport {
	reports := make([]EvidenceReport, 0)
	for _, definition := range catalog {
		for index, coordinate := range definition.RequiredEvidence {
			item := Evidence{ID: definition.ID, Package: "example/package", Test: "TestCanonicalEvidence", Backend: coordinate.Backend, Provider: coordinate.Provider}
			reports = append(reports, validEvidenceReport(coordinate.Repository, string(coordinate.Repository)+"-sha", item))
			_ = index
		}
	}
	return reports
}

func requiredEvidenceCount(catalog []CaseDefinition) int {
	count := 0
	for _, definition := range catalog {
		count += len(definition.RequiredEvidence)
	}
	return count
}
