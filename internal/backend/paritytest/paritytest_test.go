//go:build parity

package paritytest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParityTest_PackageBuilds is a smoke test confirming the package
// compiles under the parity tag and exported types round-trip through JSON
// in the same shape fleet-db emits.
func TestParityTest_PackageBuilds(t *testing.T) {
	r := NewReport("1.0.0", "dual_run")
	r.AddFixture("smoke", "smoke fixture", []DiffEntry{
		{
			FixtureID: "smoke",
			StepID:    "step_01",
			Method:    "issue.create",
			Field:     "title",
			DriftTag:  "strict",
			FleetDB:   "hello",
			Beads:     "hello",
			Verdict:   "pass",
		},
	}, 1)
	r.Finalize()

	if r.Verdict != "pass" {
		t.Fatalf("verdict: got %q want pass", r.Verdict)
	}
	if r.Summary.FixturesRun != 1 || r.Summary.TotalComparisons != 1 {
		t.Fatalf("summary counts wrong: %+v", r.Summary)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "diff-report.json")
	if err := r.WriteJSON(path); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Confirm wire-format keys match fleet-db's report shape.
	for _, key := range []string{
		`"version"`, `"generated_at"`, `"contract_version"`, `"mode"`,
		`"beads_available"`, `"summary"`, `"verdict"`, `"fixtures"`,
		`"fixture_id"`, `"step_id"`, `"method"`, `"field"`,
		`"drift_tag"`, `"fleet_db"`, `"beads"`,
	} {
		if !strings.Contains(string(data), key) {
			t.Errorf("output missing expected JSON key %s", key)
		}
	}

	var roundtrip map[string]any
	if err := json.Unmarshal(data, &roundtrip); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
}

// TestParityTest_DualRunnerSkeleton confirms the runner skeleton type wires
// together; the actual fixture execution is pending implementation.
func TestParityTest_DualRunnerSkeleton(t *testing.T) {
	r := NewReport("1.0.0", "dual_run")
	dr := New(nil, nil, r) // backends nil for skeleton test; runner does not dereference yet
	_, err := dr.RunFixture(t.Context(), "smoke")
	if err == nil {
		t.Fatal("expected not-implemented error from skeleton RunFixture")
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("unexpected error message: %v", err)
	}
}
