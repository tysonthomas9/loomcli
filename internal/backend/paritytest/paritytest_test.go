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

// TestParityTest_FixtureLoader exercises the fixture JSON loader against
// the MVP fixture. Keeps error paths covered without requiring subprocess
// spawn.
func TestParityTest_FixtureLoader(t *testing.T) {
	fx, err := LoadFixture("testdata/fixtures/crud_create_show.json")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if fx.ID != "crud_create_show" {
		t.Errorf("id: got %q want %q", fx.ID, "crud_create_show")
	}
	if len(fx.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(fx.Steps))
	}
	if fx.Steps[0].Method != "issue.create" {
		t.Errorf("step 0 method: got %q want issue.create", fx.Steps[0].Method)
	}
	if fx.Steps[1].Method != "issue.show" {
		t.Errorf("step 1 method: got %q want issue.show", fx.Steps[1].Method)
	}

	// Error paths.
	if _, err := LoadFixture("testdata/fixtures/does_not_exist.json"); err == nil {
		t.Error("expected error for missing fixture")
	}
}

// TestParityTest_CrudCreateShow is the flagship end-to-end harness run: it
// spawns bd + fleet-db subprocesses, loads a 2-step fixture, executes both
// steps against both backends, and emits a diff report.
//
// Semantic: subprocess spawn failures, fixture load failures, and panics
// fail the Go test. Diffs in the report are DATA — a nonzero diff count
// does NOT fail the test. Callers inspect the report to triage signal.
//
// This is intentionally the only orchestration test in the MVP so that
// failures are easy to bisect: all wiring is under one function.
func TestParityTest_CrudCreateShow(t *testing.T) {
	// Spawn backends. Each helper calls t.Skip() or t.Fatal() itself if its
	// prerequisites aren't met, so we get structured failures.
	beadsBE, _ := spawnBeads(t)
	fleetBE, _ := spawnFleetDB(t)

	fx, err := LoadFixture("testdata/fixtures/crud_create_show.json")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}

	report := NewReport("1.0.0", "dual_run")
	runner := New(beadsBE, fleetBE, report)

	diffs, err := runner.RunFixture(t.Context(), *fx)
	if err != nil {
		t.Fatalf("RunFixture: %v", err)
	}

	report.AddFixture(fx.ID, fx.Title, diffs, len(fx.Steps))
	report.Finalize()

	// Write the report to a temp location so operators can inspect it.
	outPath := filepath.Join(t.TempDir(), "parity-report.json")
	if err := report.WriteJSON(outPath); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	// Log a compact summary — diffs are expected but non-fatal.
	t.Logf("fixture %s: %d diffs, verdict=%s, report=%s", fx.ID, len(diffs), report.Verdict, outPath)
	for _, d := range diffs {
		fleetJSON, _ := json.Marshal(d.FleetDB)
		beadsJSON, _ := json.Marshal(d.Beads)
		t.Logf("  diff: step=%s field=%s fleet=%s beads=%s verdict=%s",
			d.StepID, d.Field, string(fleetJSON), string(beadsJSON), d.Verdict)
	}

	// Structural assertions — shape/counts must be sane even if values drift.
	if report.Summary.FixturesRun != 1 {
		t.Errorf("FixturesRun: got %d want 1", report.Summary.FixturesRun)
	}
	if report.Summary.StepsExecuted != len(fx.Steps) {
		t.Errorf("StepsExecuted: got %d want %d", report.Summary.StepsExecuted, len(fx.Steps))
	}
	if len(report.Fixtures) != 1 {
		t.Fatalf("expected 1 fixture in report, got %d", len(report.Fixtures))
	}
	if report.Fixtures[0].FixtureID != fx.ID {
		t.Errorf("FixtureID: got %q want %q", report.Fixtures[0].FixtureID, fx.ID)
	}
}
