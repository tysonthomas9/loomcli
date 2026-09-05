package cireport_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cireport"
	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

func TestWorkflowRunObserverClassifiesInfrastructureWithoutReportingProductPass(t *testing.T) {
	registry.MarkEvidence(t, 83)
	tests := []struct {
		fixture    string
		category   cireport.Category
		conclusion cireport.CheckConclusion
	}{
		{fixture: "zero-jobs.json", category: cireport.CategoryZeroJobs, conclusion: cireport.ConclusionNeutral},
		{fixture: "action-required.json", category: cireport.CategoryActionRequired, conclusion: cireport.ConclusionNeutral},
		{fixture: "billing.json", category: cireport.CategoryBilling, conclusion: cireport.ConclusionNeutral},
		{fixture: "startup-failure.json", category: cireport.CategoryInfrastructure, conclusion: cireport.ConclusionNeutral},
		{fixture: "product-test-failure.json", category: cireport.CategoryProductFailure, conclusion: cireport.ConclusionFailure},
		{fixture: "product-success.json", category: cireport.CategoryObservedCompletion, conclusion: cireport.ConclusionNeutral},
	}

	for _, test := range tests {
		t.Run(test.fixture, func(t *testing.T) {
			observation := readObservation(t, test.fixture)
			result := cireport.Classify(observation)
			if result.Category != test.category || result.Conclusion != test.conclusion {
				t.Fatalf("Classify() = category %q conclusion %q, want %q/%q", result.Category, result.Conclusion, test.category, test.conclusion)
			}
			if result.Conclusion == cireport.CheckConclusion("success") {
				t.Fatal("observer must never publish a product-pass conclusion")
			}
		})
	}
}

func readObservation(t *testing.T, name string) cireport.Observation {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var observation cireport.Observation
	if err := json.Unmarshal(raw, &observation); err != nil {
		t.Fatal(err)
	}
	return observation
}
