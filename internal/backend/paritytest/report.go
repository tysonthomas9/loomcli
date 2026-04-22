//go:build parity

package paritytest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Report mirrors fleet-db's parity.Report shape (test/parity/report.go) so
// loomcli-side output can be consumed by the same tools.
type Report struct {
	Version         int             `json:"version"`
	GeneratedAt     time.Time       `json:"generated_at"`
	ContractVersion string          `json:"contract_version"`
	Mode            string          `json:"mode"`
	BeadsAvailable  bool            `json:"beads_available"`
	Summary         ReportSummary   `json:"summary"`
	Verdict         string          `json:"verdict"`
	Fixtures        []FixtureReport `json:"fixtures"`
	WaiversUsed     []WaiverUsage   `json:"waivers_used"`
	Warnings        []string        `json:"warnings"`

	mu sync.Mutex
}

// ReportSummary aggregates counts across all fixtures.
type ReportSummary struct {
	FixturesRun      int `json:"fixtures_run"`
	StepsExecuted    int `json:"steps_executed"`
	TotalComparisons int `json:"total_comparisons"`
	DiffsFound       int `json:"diffs_found"`
	UnapprovedDiffs  int `json:"unapproved_diffs"`
	WaivedDiffs      int `json:"waived_diffs"`
	NormalizedDiffs  int `json:"normalized_diffs"`
	ExpiredWaivers   int `json:"expired_waivers"`
}

// FixtureReport records diff results for a single fixture.
type FixtureReport struct {
	FixtureID string      `json:"fixture_id"`
	Title     string      `json:"title"`
	Diffs     []DiffEntry `json:"diffs"`
	Verdict   string      `json:"verdict"`
}

// DiffEntry records one field-level comparison result. Field shape matches
// fleet-db's test/parity/differ.go DiffEntry.
type DiffEntry struct {
	FixtureID string `json:"fixture_id"`
	StepID    string `json:"step_id"`
	Method    string `json:"method"`
	Field     string `json:"field"`
	DriftTag  string `json:"drift_tag"`
	FleetDB   any    `json:"fleet_db"`
	Beads     any    `json:"beads,omitempty"`
	WaiverID  string `json:"waiver_id,omitempty"`
	Verdict   string `json:"verdict"` // pass | fail | waived | normalized
}

// WaiverUsage records that a waiver was applied.
type WaiverUsage struct {
	WaiverID  string `json:"waiver_id"`
	Operation string `json:"operation"`
	Field     string `json:"field"`
	Count     int    `json:"count"`
}

// NewReport returns an empty Report scoped to the given mode
// ("dual_run" | "fleet_db_only" | "beads_only").
func NewReport(contractVersion, mode string) *Report {
	return &Report{
		Version:         1,
		GeneratedAt:     time.Now().UTC(),
		ContractVersion: contractVersion,
		Mode:            mode,
		BeadsAvailable:  mode == "dual_run" || mode == "beads_only",
	}
}

// AddFixture appends a fixture result to the report. Safe for concurrent
// use.
//
// Only the structural counters (FixturesRun, StepsExecuted) are advanced
// here — diff-derived counters (TotalComparisons, DiffsFound,
// UnapprovedDiffs, etc.) are computed in Finalize by walking the
// accumulated Fixtures. This mirrors fleet-db's canonical report pattern
// and avoids the bug where TotalComparisons and DiffsFound always moved
// in lockstep (since both were `+= len(diffs)`).
func (r *Report) AddFixture(fixtureID, title string, diffs []DiffEntry, stepsExecuted int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	verdict := "pass"
	for _, d := range diffs {
		if d.Verdict == "fail" {
			verdict = "fail"
			break
		}
	}
	r.Fixtures = append(r.Fixtures, FixtureReport{
		FixtureID: fixtureID,
		Title:     title,
		Diffs:     diffs,
		Verdict:   verdict,
	})

	r.Summary.FixturesRun++
	r.Summary.StepsExecuted += stepsExecuted
}

// AddWarning appends a non-fatal warning (e.g. expired waiver). Safe for
// concurrent use.
func (r *Report) AddWarning(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Warnings = append(r.Warnings, msg)
}

// Finalize recomputes diff-derived summary counters by walking the
// accumulated fixtures, then stamps the overall verdict. Idempotent —
// callers may invoke it multiple times without skewing the counts.
func (r *Report) Finalize() {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Reset diff-derived counters so Finalize is safely idempotent.
	r.Summary.TotalComparisons = 0
	r.Summary.DiffsFound = 0
	r.Summary.UnapprovedDiffs = 0
	r.Summary.WaivedDiffs = 0
	r.Summary.NormalizedDiffs = 0

	for _, fr := range r.Fixtures {
		for _, d := range fr.Diffs {
			r.Summary.TotalComparisons++
			r.Summary.DiffsFound++
			switch d.Verdict {
			case "fail":
				r.Summary.UnapprovedDiffs++
			case "waived":
				r.Summary.WaivedDiffs++
			case "normalized":
				r.Summary.NormalizedDiffs++
			}
		}
	}

	if r.Summary.UnapprovedDiffs > 0 {
		r.Verdict = "fail"
	} else {
		r.Verdict = "pass"
	}
}

// WriteJSON writes the report as JSON to the given path, creating parent
// directories as needed.
func (r *Report) WriteJSON(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp) // #nosec G304 — path is a caller-supplied report destination
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
