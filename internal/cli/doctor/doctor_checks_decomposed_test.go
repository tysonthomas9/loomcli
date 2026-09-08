package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// testDecomposedLabel is the workspace label the decomposed tests pretend is
// configured. It is deliberately NOT the old hardcoded word: the check must work
// off whatever the workspace declares, and a fixture that reused the old
// hardcoded literal would pass just as well against the bug this replaced.
const testDecomposedLabel = "split"

// decomposedListFn builds a ListFn that answers the label query with parents
// and the ParentID query from kids, so a test only has to declare the shape of
// the board it wants.
func decomposedListFn(parents []backend.IssueData, kids map[string][]backend.IssueData) func(context.Context, backend.ListOpts) ([]backend.IssueData, error) {
	return func(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
		if opts.ParentID != "" {
			return kids[opts.ParentID], nil
		}
		if len(opts.Labels) == 1 && opts.Labels[0] == testDecomposedLabel {
			return parents, nil
		}
		return nil, fmt.Errorf("unexpected list opts: %+v", opts)
	}
}

func TestCheckDecomposedWithoutChildren(t *testing.T) {
	t.Parallel()

	t.Run("skipped when no issue backend", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		deps.IssueBackend = nil

		for _, scan := range []*decomposedScan{newDecomposedScan(deps), newDecomposedScan(nil)} {
			if result := checkDecomposedWithoutChildren(scan); result != (CheckResult{}) {
				t.Errorf("expected empty (skipped) result, got %+v", result)
			}
			if result := checkDecomposedChildrenAllClosed(scan); result != (CheckResult{}) {
				t.Errorf("expected empty (skipped) result, got %+v", result)
			}
		}
	})

	t.Run("skipped when list fails", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, mockBackend := NewTestDeps(t)
		mockBackend.ListErr = errors.New("fleet-db unreachable")

		if result := checkDecomposedWithoutChildren(&decomposedScan{deps: deps, label: testDecomposedLabel}); result != (CheckResult{}) {
			t.Errorf("expected empty (skipped) result, got %+v", result)
		}
	})

	t.Run("skipped when no decomposed issues", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, mockBackend := NewTestDeps(t)
		mockBackend.ListFn = decomposedListFn(nil, nil)

		if result := checkDecomposedWithoutChildren(&decomposedScan{deps: deps, label: testDecomposedLabel}); result != (CheckResult{}) {
			t.Errorf("expected empty (skipped) result, got %+v", result)
		}
	})

	t.Run("pass when every decomposed issue has children", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, mockBackend := NewTestDeps(t)
		mockBackend.ListFn = decomposedListFn(
			[]backend.IssueData{{ID: "PUPPET-1", Status: "blocked"}},
			map[string][]backend.IssueData{"PUPPET-1": {{ID: "PUPPET-2", Status: "open"}}},
		)

		result := checkDecomposedWithoutChildren(&decomposedScan{deps: deps, label: testDecomposedLabel})
		if result.Status != StatusPass {
			t.Fatalf("expected pass, got %v: %s", result.Status, result.Summary)
		}
		if result.Name != "decomposed_without_children" {
			t.Errorf("unexpected name: %s", result.Name)
		}
		if result.Summary != "no decomposed issues without children (1 checked)" {
			t.Errorf("unexpected summary: %s", result.Summary)
		}
	})

	t.Run("closed and tombstoned parents are ignored", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, mockBackend := NewTestDeps(t)
		mockBackend.ListFn = decomposedListFn(
			[]backend.IssueData{
				{ID: "PUPPET-1", Status: "closed"},
				{ID: "PUPPET-2", Status: "tombstone"},
			},
			nil,
		)

		result := checkDecomposedWithoutChildren(&decomposedScan{deps: deps, label: testDecomposedLabel})
		if result.Status != StatusPass {
			t.Fatalf("expected pass, got %v: %s", result.Status, result.Summary)
		}
		if result.Summary != "no decomposed issues without children (0 checked)" {
			t.Errorf("unexpected summary: %s", result.Summary)
		}
	})

	t.Run("warn lists offenders with remediation", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, mockBackend := NewTestDeps(t)
		mockBackend.ListFn = decomposedListFn(
			[]backend.IssueData{
				{ID: "PUPPET-295", Status: "blocked"},
				{ID: "PUPPET-285", Status: "in_progress"},
				{ID: "PUPPET-300", Status: "blocked"},
				{ID: "PUPPET-9", Status: "closed"},
			},
			map[string][]backend.IssueData{"PUPPET-300": {{ID: "PUPPET-301"}}},
		)

		result := checkDecomposedWithoutChildren(&decomposedScan{deps: deps, label: testDecomposedLabel})
		if result.Status != StatusWarn {
			t.Fatalf("expected warn, got %v: %s", result.Status, result.Summary)
		}
		if result.Summary != "2 decomposed issue(s) have no children" {
			t.Errorf("unexpected summary: %s", result.Summary)
		}
		for _, want := range []string{
			"issue=PUPPET-285 status=in_progress children=0",
			"issue=PUPPET-295 status=blocked children=0",
			"remediation: the split lost its parent links",
			"`loom data update <child> --parent <parent>`",
		} {
			if !strings.Contains(result.Detail, want) {
				t.Errorf("detail missing %q:\n%s", want, result.Detail)
			}
		}
		if strings.Contains(result.Detail, "PUPPET-300") || strings.Contains(result.Detail, "PUPPET-9") {
			t.Errorf("detail names an issue that is not an offender:\n%s", result.Detail)
		}
	})

	t.Run("truncates rather than fanning out unbounded child queries", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, mockBackend := NewTestDeps(t)
		parents := make([]backend.IssueData, 0, maxDecomposedScan+1)
		for i := 0; i <= maxDecomposedScan; i++ {
			parents = append(parents, backend.IssueData{ID: fmt.Sprintf("PUPPET-%d", i), Status: "blocked"})
		}
		childQueries := 0
		base := decomposedListFn(parents, nil)
		mockBackend.ListFn = func(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
			if opts.ParentID != "" {
				childQueries++
			}
			return base(ctx, opts)
		}

		result := checkDecomposedWithoutChildren(&decomposedScan{deps: deps, label: testDecomposedLabel})
		if result.Status != StatusWarn {
			t.Fatalf("expected warn, got %v: %s", result.Status, result.Summary)
		}
		if childQueries != 0 {
			t.Errorf("expected no child queries when truncating, got %d", childQueries)
		}
		if !strings.Contains(result.Summary, "too many decomposed issues") {
			t.Errorf("summary does not name the cap: %s", result.Summary)
		}
		if !strings.Contains(result.Detail, "truncated:") {
			t.Errorf("detail does not name the truncation:\n%s", result.Detail)
		}
	})
}

// closedAt is a helper for children that carry a close timestamp. A pointer
// field with no helper makes every fixture below three lines longer.
func closedAt(t *testing.T, iso string) *time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		t.Fatalf("parse %q: %v", iso, err)
	}
	return &ts
}

func strandScan(t *testing.T, parents []backend.IssueData, kids map[string][]backend.IssueData) *decomposedScan {
	t.Helper()
	return strandScanWithMarker(t, "", parents, kids)
}

// strandScanWithMarker pins the union marker on the scan itself. The marker is
// deliberately NOT overridden through the package-level resolver: parallel
// subtests would race on it.
func strandScanWithMarker(t *testing.T, marker string, parents []backend.IssueData, kids map[string][]backend.IssueData) *decomposedScan {
	t.Helper()
	deps, _, _, _, mockBackend := NewTestDeps(t)
	mockBackend.ListFn = decomposedListFn(parents, kids)
	return &decomposedScan{deps: deps, label: testDecomposedLabel, marker: marker}
}

func TestCheckDecomposedChildrenAllClosed(t *testing.T) {
	t.Parallel()

	t.Run("skipped when list fails", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, mockBackend := NewTestDeps(t)
		mockBackend.ListErr = errors.New("fleet-db unreachable")

		scan := &decomposedScan{deps: deps, label: testDecomposedLabel}
		if result := checkDecomposedChildrenAllClosed(scan); result != (CheckResult{}) {
			t.Errorf("expected empty (skipped) result, got %+v", result)
		}
	})

	// Both queries must ask for an explicit page. The server's default is 50,
	// which silently reduced the label query to a fraction of the board.
	t.Run("both queries request an explicit page", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, mockBackend := NewTestDeps(t)
		var labelLimit, childLimit int
		mockBackend.ListFn = func(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
			if opts.ParentID != "" {
				childLimit = opts.Limit
				return []backend.IssueData{{ID: "PUPPET-2", Status: "closed"}}, nil
			}
			labelLimit = opts.Limit
			return []backend.IssueData{{ID: "PUPPET-1", Status: "open"}}, nil
		}

		checkDecomposedChildrenAllClosed(&decomposedScan{deps: deps, label: testDecomposedLabel})
		if labelLimit != maxChildScan {
			t.Errorf("label query limit = %d, want %d", labelLimit, maxChildScan)
		}
		if childLimit != maxChildScan {
			t.Errorf("child query limit = %d, want %d", childLimit, maxChildScan)
		}
	})

	t.Run("skipped when no decomposed issues", func(t *testing.T) {
		t.Parallel()
		scan := strandScan(t, nil, nil)
		if result := checkDecomposedChildrenAllClosed(scan); result != (CheckResult{}) {
			t.Errorf("expected empty (skipped) result, got %+v", result)
		}
	})

	t.Run("skipped when a child query fails", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, mockBackend := NewTestDeps(t)
		mockBackend.ListFn = func(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
			if opts.ParentID != "" {
				return nil, errors.New("child query failed")
			}
			return []backend.IssueData{{ID: "PUPPET-1", Status: "blocked"}}, nil
		}

		scan := &decomposedScan{deps: deps, label: testDecomposedLabel}
		if result := checkDecomposedChildrenAllClosed(scan); result != (CheckResult{}) {
			t.Errorf("expected empty (skipped) result, got %+v", result)
		}
		// The half-scanned board must not produce a verdict from the sibling
		// either — one scan, one skip.
		if result := checkDecomposedWithoutChildren(scan); result != (CheckResult{}) {
			t.Errorf("expected the sibling check to skip too, got %+v", result)
		}
	})

	t.Run("warns when a parked parent has only finished children", func(t *testing.T) {
		t.Parallel()
		scan := strandScan(t,
			[]backend.IssueData{{ID: "PUPPET-284", Status: "blocked"}},
			map[string][]backend.IssueData{"PUPPET-284": {
				{ID: "PUPPET-290", Status: "closed", ClosedAt: closedAt(t, "2026-09-03T11:02:00Z")},
				{ID: "PUPPET-291", Status: "closed", ClosedAt: closedAt(t, "2026-09-02T09:00:00Z")},
			}},
		)

		result := checkDecomposedChildrenAllClosed(scan)
		if result.Status != StatusWarn {
			t.Fatalf("expected warn, got %v: %s", result.Status, result.Summary)
		}
		if result.Name != decomposedChildrenCheckName {
			t.Errorf("unexpected name: %s", result.Name)
		}
		if result.Summary != "1 decomposed parent(s) parked with every child finished" {
			t.Errorf("unexpected summary: %s", result.Summary)
		}
		for _, want := range []string{
			"issue=PUPPET-284 status=blocked children=2 closed=2",
			"loom data update <parent> --status open",
			"or close it if the split is done",
		} {
			if !strings.Contains(result.Detail, want) {
				t.Errorf("detail missing %q:\n%s", want, result.Detail)
			}
		}

		report, ok := result.Data.(decomposedStrandReport)
		if !ok {
			t.Fatalf("expected a decomposedStrandReport payload, got %T", result.Data)
		}
		if len(report.Stranded) != 1 || report.Stranded[0].ID != "PUPPET-284" {
			t.Fatalf("unexpected stranded set: %+v", report.Stranded)
		}
		if report.Stranded[0].LastChildClosedAt != "2026-09-03T11:02:00Z" {
			t.Errorf("expected the LATEST child close, got %q", report.Stranded[0].LastChildClosedAt)
		}
	})

	t.Run("a live child keeps the park honest", func(t *testing.T) {
		t.Parallel()
		scan := strandScan(t,
			[]backend.IssueData{{ID: "PUPPET-284", Status: "blocked"}},
			map[string][]backend.IssueData{"PUPPET-284": {
				{ID: "PUPPET-290", Status: "closed"},
				{ID: "PUPPET-291", Status: "open"},
			}},
		)

		result := checkDecomposedChildrenAllClosed(scan)
		if result.Status != StatusPass {
			t.Fatalf("expected pass, got %v: %s", result.Status, result.Summary)
		}
		if result.Data != nil {
			t.Errorf("expected no payload when there is nothing to report, got %+v", result.Data)
		}
	})

	// A live parent whose children have all finished is the NORMAL end of a
	// split. This is the anti-noise assertion: it fails if somebody
	// "simplifies" the predicate back to "the parent is not closed".
	t.Run("a live parent with all children finished is completable, not stranded", func(t *testing.T) {
		t.Parallel()
		scan := strandScan(t,
			[]backend.IssueData{{ID: "PUPPET-301", Status: "open"}},
			map[string][]backend.IssueData{"PUPPET-301": {
				{ID: "PUPPET-302", Status: "closed"},
				{ID: "PUPPET-303", Status: "closed"},
			}},
		)

		result := checkDecomposedChildrenAllClosed(scan)
		if result.Status != StatusPass {
			t.Fatalf("expected pass, got %v: %s", result.Status, result.Summary)
		}
		report, ok := result.Data.(decomposedStrandReport)
		if !ok {
			t.Fatalf("expected a payload naming the completable parent, got %T", result.Data)
		}
		if len(report.Stranded) != 0 {
			t.Errorf("a live parent must never be stranded: %+v", report.Stranded)
		}
		if len(report.Completable) != 1 || report.Completable[0].ID != "PUPPET-301" {
			t.Fatalf("unexpected completable set: %+v", report.Completable)
		}
	})

	t.Run("deferred is parked too", func(t *testing.T) {
		t.Parallel()
		scan := strandScan(t,
			[]backend.IssueData{{ID: "PUPPET-400", Status: "deferred"}},
			map[string][]backend.IssueData{"PUPPET-400": {{ID: "PUPPET-401", Status: "closed"}}},
		)

		result := checkDecomposedChildrenAllClosed(scan)
		if result.Status != StatusWarn {
			t.Fatalf("expected warn for a deferred parent, got %v: %s", result.Status, result.Summary)
		}
	})

	t.Run("tombstone counts as finished", func(t *testing.T) {
		t.Parallel()
		scan := strandScan(t,
			[]backend.IssueData{{ID: "PUPPET-410", Status: "blocked"}},
			map[string][]backend.IssueData{"PUPPET-410": {
				{ID: "PUPPET-411", Status: "closed"},
				{ID: "PUPPET-412", Status: "tombstone"},
			}},
		)

		if result := checkDecomposedChildrenAllClosed(scan); result.Status != StatusWarn {
			t.Fatalf("expected warn, got %v: %s", result.Status, result.Summary)
		}
	})

	// The two checks partition the board rather than overlapping: a childless
	// parent belongs to the sibling and to nobody else.
	t.Run("a childless parent is the sibling's, off the same scan", func(t *testing.T) {
		t.Parallel()
		scan := strandScan(t, []backend.IssueData{{ID: "PUPPET-500", Status: "blocked"}}, nil)

		strand := checkDecomposedChildrenAllClosed(scan)
		if strand.Status != StatusPass {
			t.Fatalf("expected pass here, got %v: %s", strand.Status, strand.Summary)
		}
		sibling := checkDecomposedWithoutChildren(scan)
		if sibling.Status != StatusWarn {
			t.Fatalf("expected the sibling to warn, got %v: %s", sibling.Status, sibling.Summary)
		}
		if !strings.Contains(sibling.Detail, "issue=PUPPET-500") {
			t.Errorf("sibling detail does not name the childless parent:\n%s", sibling.Detail)
		}
	})

	t.Run("a clamped child page is reported, never asserted over", func(t *testing.T) {
		t.Parallel()
		kids := make([]backend.IssueData, 0, maxChildScan)
		for i := 0; i < maxChildScan; i++ {
			kids = append(kids, backend.IssueData{ID: fmt.Sprintf("PUPPET-%d", 1000+i), Status: "closed"})
		}
		scan := strandScan(t,
			[]backend.IssueData{{ID: "PUPPET-999", Status: "blocked"}},
			map[string][]backend.IssueData{"PUPPET-999": kids},
		)

		result := checkDecomposedChildrenAllClosed(scan)
		if result.Status != StatusPass {
			t.Fatalf("a clamped page must not produce a stranding verdict, got %v: %s", result.Status, result.Summary)
		}
		report, ok := result.Data.(decomposedStrandReport)
		if !ok {
			t.Fatalf("expected a payload naming the truncated parent, got %T", result.Data)
		}
		if len(report.Stranded) != 0 || len(report.Completable) != 0 {
			t.Errorf("a truncated parent belongs to neither bucket: %+v", report)
		}
		if len(report.TruncatedParents) != 1 || report.TruncatedParents[0] != "PUPPET-999" {
			t.Fatalf("unexpected truncated set: %+v", report.TruncatedParents)
		}
		if !strings.Contains(result.Detail, "PUPPET-999") {
			t.Errorf("detail does not name the truncated parent:\n%s", result.Detail)
		}
	})

	t.Run("closed and tombstoned parents are ignored", func(t *testing.T) {
		t.Parallel()
		scan := strandScan(t,
			[]backend.IssueData{
				{ID: "PUPPET-1", Status: "closed"},
				{ID: "PUPPET-2", Status: "tombstone"},
			},
			nil,
		)

		result := checkDecomposedChildrenAllClosed(scan)
		if result.Status != StatusPass {
			t.Fatalf("expected pass, got %v: %s", result.Status, result.Summary)
		}
		if !strings.Contains(result.Summary, "0 checked") {
			t.Errorf("summary should report zero checked: %s", result.Summary)
		}
	})

	t.Run("truncates rather than fanning out unbounded child queries", func(t *testing.T) {
		t.Parallel()
		parents := make([]backend.IssueData, 0, maxDecomposedScan+1)
		for i := 0; i <= maxDecomposedScan; i++ {
			parents = append(parents, backend.IssueData{ID: fmt.Sprintf("PUPPET-%d", i), Status: "blocked"})
		}
		deps, _, _, _, mockBackend := NewTestDeps(t)
		childQueries := 0
		base := decomposedListFn(parents, nil)
		mockBackend.ListFn = func(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
			if opts.ParentID != "" {
				childQueries++
			}
			return base(ctx, opts)
		}

		scan := &decomposedScan{deps: deps, label: testDecomposedLabel}
		for _, result := range []CheckResult{
			checkDecomposedChildrenAllClosed(scan),
			checkDecomposedWithoutChildren(scan),
		} {
			if result.Status != StatusWarn {
				t.Fatalf("expected warn from %s, got %v: %s", result.Name, result.Status, result.Summary)
			}
			if !strings.Contains(result.Summary, "too many decomposed issues") {
				t.Errorf("%s summary does not name the cap: %s", result.Name, result.Summary)
			}
		}
		if childQueries != 0 {
			t.Errorf("expected no child queries when truncating, got %d", childQueries)
		}
	})

	t.Run("finished children still carrying the union marker are named", func(t *testing.T) {
		t.Parallel()
		scan := strandScanWithMarker(t, "union-pending",
			[]backend.IssueData{{ID: "PUPPET-284", Status: "blocked", Notes: "waiting on the owner"}},
			map[string][]backend.IssueData{"PUPPET-284": {
				{ID: "PUPPET-290", Status: "closed", Labels: []string{"union-pending"}},
				{ID: "PUPPET-291", Status: "closed"},
			}},
		)

		result := checkDecomposedChildrenAllClosed(scan)
		if result.Status != StatusWarn {
			t.Fatalf("expected warn, got %v: %s", result.Status, result.Summary)
		}
		if !strings.Contains(result.Detail, "unshipped=PUPPET-290") {
			t.Errorf("detail does not name the unshipped child:\n%s", result.Detail)
		}
		if !strings.Contains(result.Detail, "do not close this parent") {
			t.Errorf("detail does not carry the unshipped remediation:\n%s", result.Detail)
		}
		report := result.Data.(decomposedStrandReport)
		if !report.Stranded[0].NotesPresent {
			t.Errorf("expected notes_present for a parent carrying a blocker note")
		}
	})
}

// TestDecomposedStrandReportJSON pins the payload shape the sibling PM2 watcher
// parses. A field rename here is a contract break, not a refactor.
func TestDecomposedStrandReportJSON(t *testing.T) {
	t.Parallel()

	report := decomposedStrandReport{
		Scanned: 1,
		Stranded: []strandedParent{{
			ID: "PUPPET-284", Status: "blocked", Children: 3, ClosedChildren: 3,
			LastChildClosedAt: "2026-09-03T11:02:00Z",
			UnshippedChildren: []string{"PUPPET-290"},
		}},
		Completable: []strandedParent{{ID: "PUPPET-301", Status: "open", Children: 2, ClosedChildren: 2}},
	}
	got, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"scanned":1,` +
		`"stranded":[{"id":"PUPPET-284","status":"blocked","children":3,"closed_children":3,` +
		`"last_child_closed_at":"2026-09-03T11:02:00Z","unshipped_children":["PUPPET-290"],"notes_present":false}],` +
		`"completable":[{"id":"PUPPET-301","status":"open","children":2,"closed_children":2,"notes_present":false}],` +
		`"childless_parents":0}`
	if string(got) != want {
		t.Fatalf("payload shape changed:\n got: %s\nwant: %s", got, want)
	}

	// scanned and childless_parents are ALWAYS present: a measured zero must be
	// distinguishable from a key that was never asked.
	empty, err := json.Marshal(decomposedStrandReport{})
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if string(empty) != `{"scanned":0,"childless_parents":0}` {
		t.Fatalf("unexpected empty payload: %s", empty)
	}
}

// TestCheckDecomposedChildrenAllClosedUnconfigured: an unconfigured workspace
// must SEE that the check did not run, under this check's own name.
func TestCheckDecomposedChildrenAllClosedUnconfigured(t *testing.T) {
	setupUnionWorkspace(t, "defaults:\n  labels:\n    marker: union-pending\n")
	deps, _, _, _, mockBackend := NewTestDeps(t)
	mockBackend.ListFn = func(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
		t.Fatalf("the backend must not be queried without a label: %+v", opts)
		return nil, nil
	}

	result := checkDecomposedChildrenAllClosed(newDecomposedScan(deps))
	if result.Status != StatusWarn {
		t.Fatalf("expected warn (skipped), got %v: %s", result.Status, result.Summary)
	}
	if result.Name != decomposedChildrenCheckName {
		t.Errorf("unexpected name: %s", result.Name)
	}
	if !strings.HasPrefix(result.Summary, decomposedChildrenCheckName+" skipped") {
		t.Errorf("summary does not name this check as skipped: %s", result.Summary)
	}
}

func TestUnionMarkerLabelFromContract(t *testing.T) {
	t.Run("reads defaults.labels.marker", func(t *testing.T) {
		setupUnionWorkspace(t, "defaults:\n  labels:\n    marker: ledger-pending\n")
		if got := unionMarkerLabel(); got != "ledger-pending" {
			t.Fatalf("expected the configured marker, got %q", got)
		}
	})

	t.Run("falls back when the key is absent", func(t *testing.T) {
		setupUnionWorkspace(t, "defaults:\n  labels:\n    decomposed: split\n")
		if got := unionMarkerLabel(); got != defaultUnionMarkerLabel {
			t.Fatalf("expected the fallback marker, got %q", got)
		}
	})
}
