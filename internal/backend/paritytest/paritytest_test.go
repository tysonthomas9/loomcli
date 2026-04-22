//go:build parity

package paritytest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
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

// TestIssueDataToMap_NilReturnsNoData exercises the nil-in/nil-out path —
// a nil IssueData is "nothing to report", not an error.
func TestIssueDataToMap_NilReturnsNoData(t *testing.T) {
	m, err := issueDataToMap(nil)
	if err != nil {
		t.Fatalf("nil input should not error: %v", err)
	}
	if m != nil {
		t.Errorf("nil input should yield nil map; got %v", m)
	}
}

// TestIssueDataToMap_RoundTrip confirms the normal path emits a flat
// JSON-shaped map with expected keys. If the shape ever changes the
// diff layer's field list has to change with it, so this test is the
// canary.
func TestIssueDataToMap_RoundTrip(t *testing.T) {
	prio := 2
	d := &backend.IssueData{
		ID:       "PARITY-1",
		Title:    "hello",
		Priority: prio,
		Status:   "open",
	}
	m, err := issueDataToMap(d)
	if err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if m["title"] != "hello" {
		t.Errorf("title: got %v want hello", m["title"])
	}
	if m["id"] != "PARITY-1" {
		t.Errorf("id: got %v want PARITY-1", m["id"])
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

// TestParityTest_AllFixtures auto-discovers every fixture JSON file under
// testdata/fixtures/ and runs each as a subtest against a single shared
// bd + fleet-db pair. Each fixture gets an isolated beads workspace / fleet
// workspace so state bleed between fixtures is impossible.
//
// Semantic (same as TestParityTest_CrudCreateShow):
//   - infra failures (spawn, fixture load) fail the subtest
//   - diff entries are DATA, not failures — a fixture with 10 diffs still
//     passes the subtest; operators inspect the report JSON for signal
//   - the test failing indicates broken wiring, not backend drift
//
// The aggregated report is written to one file per invocation so operators
// can triage cross-fixture drift in a single pass. See doc.go for the wire
// format.
func TestParityTest_AllFixtures(t *testing.T) {
	fixtures, err := discoverFixtures("testdata/fixtures")
	if err != nil {
		t.Fatalf("discoverFixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no fixtures found under testdata/fixtures — expected at least one")
	}

	report := NewReport("1.0.0", "dual_run")

	for _, path := range fixtures {
		fx, err := LoadFixture(path)
		if err != nil {
			t.Fatalf("LoadFixture(%s): %v", path, err)
		}

		t.Run(fx.ID, func(t *testing.T) {
			// Spawn backends per-subtest so workspaces are isolated and a
			// failure in one fixture can't poison another. spawnBeads +
			// spawnFleetDB both register t.Cleanup, so this is safe.
			beadsBE, _ := spawnBeads(t)
			fleetBE, _ := spawnFleetDB(t)

			runner := New(beadsBE, fleetBE, report)
			diffs, err := runner.RunFixture(t.Context(), *fx)
			if err != nil {
				t.Fatalf("RunFixture: %v", err)
			}

			report.AddFixture(fx.ID, fx.Title, diffs, len(fx.Steps))

			// Compact per-fixture log: one line per diff so operators can
			// eyeball the report without opening the JSON.
			t.Logf("fixture %s: %d diffs", fx.ID, len(diffs))
			for _, d := range diffs {
				fleetJSON, _ := json.Marshal(d.FleetDB)
				beadsJSON, _ := json.Marshal(d.Beads)
				t.Logf("  diff: step=%s method=%s field=%s fleet=%s beads=%s verdict=%s",
					d.StepID, d.Method, d.Field, string(fleetJSON), string(beadsJSON), d.Verdict)
			}
		})
	}

	report.Finalize()
	outPath := filepath.Join(t.TempDir(), "parity-report-all.json")
	if err := report.WriteJSON(outPath); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	t.Logf("aggregate report: fixtures=%d steps=%d diffs=%d verdict=%s path=%s",
		report.Summary.FixturesRun, report.Summary.StepsExecuted,
		report.Summary.DiffsFound, report.Verdict, outPath)

	// Structural assertion — we should have exactly one FixtureReport per
	// discovered fixture.
	if report.Summary.FixturesRun != len(fixtures) {
		t.Errorf("FixturesRun: got %d want %d", report.Summary.FixturesRun, len(fixtures))
	}
}

// discoverFixtures returns sorted absolute paths to every *.json file under
// dir. Sort order keeps test output deterministic so flaky runs are easy
// to bisect.
func discoverFixtures(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	sort.Strings(paths)
	return paths, nil
}
