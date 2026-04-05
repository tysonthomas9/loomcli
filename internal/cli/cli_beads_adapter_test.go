package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// mockBDRunner implements BDRunner for testing cliBeadsAdapter.
type mockBDRunner struct {
	calls []mockBDRunnerCall
	fn    func(dir string, args ...string) CommandResult
}

type mockBDRunnerCall struct {
	Dir  string
	Args []string
}

func (m *mockBDRunner) Run(dir string, args ...string) CommandResult {
	m.calls = append(m.calls, mockBDRunnerCall{Dir: dir, Args: args})
	if m.fn != nil {
		return m.fn(dir, args...)
	}
	return CommandResult{}
}

func TestCliBeadsAdapter_ClaimIssue_Success(t *testing.T) {
	runner := &mockBDRunner{}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	err := a.ClaimIssue(context.Background(), "T-10", 0)
	if err != nil {
		t.Fatalf("ClaimIssue() error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
	got := strings.Join(runner.calls[0].Args, " ")
	want := "update T-10 --claim"
	if got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestCliBeadsAdapter_ClaimIssue_EmptyID(t *testing.T) {
	runner := &mockBDRunner{}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	err := a.ClaimIssue(context.Background(), "", 0)
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
	if len(runner.calls) != 0 {
		t.Error("runner should not be called for empty ID")
	}
	var be *backend.BackendError
	if !errors.As(err, &be) {
		t.Fatalf("expected *backend.BackendError, got %T", err)
	}
	if be.Kind != backend.KindValidation {
		t.Errorf("BackendError.Kind = %q, want %q", be.Kind, backend.KindValidation)
	}
	if msg := err.Error(); !strings.Contains(msg, "id must not be empty") {
		t.Errorf("error message = %q, want it to contain %q", msg, "id must not be empty")
	}
}

func TestCliBeadsAdapter_ClaimIssue_NegativeTTL(t *testing.T) {
	runner := &mockBDRunner{}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	err := a.ClaimIssue(context.Background(), "T-10", time.Duration(-1))
	if err == nil {
		t.Fatal("expected error for negative lockTTL")
	}
	if len(runner.calls) != 0 {
		t.Error("runner should not be called for negative TTL")
	}
	var be *backend.BackendError
	if !errors.As(err, &be) {
		t.Fatalf("expected *backend.BackendError, got %T", err)
	}
	if be.Kind != backend.KindValidation {
		t.Errorf("BackendError.Kind = %q, want %q", be.Kind, backend.KindValidation)
	}
	if msg := err.Error(); !strings.Contains(msg, "lockTTL must not be negative") {
		t.Errorf("error message = %q, want it to contain %q", msg, "lockTTL must not be negative")
	}
}

func TestCliBeadsAdapter_ClaimIssue_RunnerError(t *testing.T) {
	runner := &mockBDRunner{
		fn: func(_ string, _ ...string) CommandResult {
			return CommandResult{
				Err:    fmt.Errorf("exit status 1"),
				Stderr: "already claimed by other-agent",
			}
		},
	}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	err := a.ClaimIssue(context.Background(), "T-10", 0)
	if err == nil {
		t.Fatal("expected error for runner failure")
	}
	if !strings.Contains(err.Error(), "already claimed") {
		t.Errorf("error should contain 'already claimed', got %q", err.Error())
	}
}

func TestCliBeadsAdapter_Reopen_Success(t *testing.T) {
	runner := &mockBDRunner{}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	err := a.Reopen(context.Background(), "T-11", backend.ReopenParams{Reason: "regression found"})
	if err != nil {
		t.Fatalf("Reopen() error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
	got := strings.Join(runner.calls[0].Args, " ")
	want := "reopen T-11 --reason regression found"
	if got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestCliBeadsAdapter_Reopen_NoReason(t *testing.T) {
	runner := &mockBDRunner{}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	err := a.Reopen(context.Background(), "T-11", backend.ReopenParams{})
	if err != nil {
		t.Fatalf("Reopen() error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
	got := strings.Join(runner.calls[0].Args, " ")
	want := "reopen T-11"
	if got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestCliBeadsAdapter_Reopen_RunnerError(t *testing.T) {
	runner := &mockBDRunner{
		fn: func(_ string, _ ...string) CommandResult {
			return CommandResult{
				Err:    fmt.Errorf("exit status 1"),
				Stderr: "issue not found",
			}
		},
	}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	err := a.Reopen(context.Background(), "T-11", backend.ReopenParams{Reason: "test"})
	if err == nil {
		t.Fatal("expected error for runner failure")
	}
	if !strings.Contains(err.Error(), "issue not found") {
		t.Errorf("error should contain 'issue not found', got %q", err.Error())
	}
}

func TestCliBeadsAdapter_Stats_AllFields(t *testing.T) {
	jsonOutput := `{
		"summary": {
			"total_issues": 100,
			"open_issues": 40,
			"closed_issues": 30,
			"in_progress_issues": 20,
			"blocked_issues": 5,
			"deferred_issues": 3,
			"ready_issues": 15,
			"tombstone_issues": 2,
			"pinned_issues": 4,
			"epics_eligible_for_closure": 1,
			"average_lead_time_hours": 48.5
		}
	}`
	runner := &mockBDRunner{
		fn: func(_ string, _ ...string) CommandResult {
			return CommandResult{Stdout: jsonOutput}
		},
	}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	got, err := a.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
	checks := []struct {
		name string
		got  int
		want int
	}{
		{"TotalIssues", got.TotalIssues, 100},
		{"OpenIssues", got.OpenIssues, 40},
		{"ClosedIssues", got.ClosedIssues, 30},
		{"InProgressIssues", got.InProgressIssues, 20},
		{"BlockedIssues", got.BlockedIssues, 5},
		{"DeferredIssues", got.DeferredIssues, 3},
		{"ReadyIssues", got.ReadyIssues, 15},
		{"TombstoneIssues", got.TombstoneIssues, 2},
		{"PinnedIssues", got.PinnedIssues, 4},
		{"EpicsEligibleForClosure", got.EpicsEligibleForClosure, 1},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	if got.AverageLeadTime != 48.5 {
		t.Errorf("AverageLeadTime = %f, want 48.5", got.AverageLeadTime)
	}
}

func TestCliBeadsAdapter_Stats_MissingFields(t *testing.T) {
	// Old bd binary that only outputs 3 fields — missing fields should be zero-valued.
	jsonOutput := `{"summary": {"total_issues": 10, "open_issues": 3, "closed_issues": 7}}`
	runner := &mockBDRunner{
		fn: func(_ string, _ ...string) CommandResult {
			return CommandResult{Stdout: jsonOutput}
		},
	}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	got, err := a.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
	if got.TotalIssues != 10 {
		t.Errorf("TotalIssues = %d, want 10", got.TotalIssues)
	}
	if got.ReadyIssues != 0 {
		t.Errorf("ReadyIssues = %d, want 0", got.ReadyIssues)
	}
	if got.EpicsEligibleForClosure != 0 {
		t.Errorf("EpicsEligibleForClosure = %d, want 0", got.EpicsEligibleForClosure)
	}
	if got.AverageLeadTime != 0.0 {
		t.Errorf("AverageLeadTime = %f, want 0.0", got.AverageLeadTime)
	}
}

func TestCliBeadsAdapter_Reopen_EmptyID(t *testing.T) {
	runner := &mockBDRunner{}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	err := a.Reopen(context.Background(), "", backend.ReopenParams{Reason: "test"})
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
	if len(runner.calls) != 0 {
		t.Error("runner should not be called for empty ID")
	}
	var be *backend.BackendError
	if !errors.As(err, &be) {
		t.Fatalf("expected *backend.BackendError, got %T", err)
	}
	if be.Kind != backend.KindValidation {
		t.Errorf("BackendError.Kind = %q, want %q", be.Kind, backend.KindValidation)
	}
}

// --- List method tests ---

// emptyJSONRunner returns a mock runner whose fn returns an empty JSON array.
func emptyJSONRunner() *mockBDRunner {
	return &mockBDRunner{
		fn: func(_ string, _ ...string) CommandResult {
			return CommandResult{Stdout: "[]"}
		},
	}
}

// listArgs is a convenience that calls List with the given opts and returns the
// captured args slice. It fails the test on error.
func listArgs(t *testing.T, opts backend.ListOpts) []string {
	t.Helper()
	runner := emptyJSONRunner()
	a := newCliBeadsAdapter(runner, "/tmp/test")
	_, err := a.List(context.Background(), opts)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
	return runner.calls[0].Args
}

// containsFlag checks that args contains the given flag (and optional value).
// For a flag-only arg (e.g. "--deferred"), pass value "".
// For a flag+value pair (e.g. "--status open"), pass value "open".
func containsFlag(args []string, flag, value string) bool {
	for i, a := range args {
		if a == flag {
			if value == "" {
				return true
			}
			if i+1 < len(args) && args[i+1] == value {
				return true
			}
		}
		// Handle --key=value style.
		if value != "" && a == flag+"="+value {
			return true
		}
	}
	return false
}

// containsFlagOnly checks that a flag-only arg is present (no value following).
func containsFlagOnly(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// assertNoFlag checks that the flag does NOT appear in args.
func assertNoFlag(t *testing.T, args []string, flag string) {
	t.Helper()
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			t.Errorf("expected no %s flag, but found it in args: %v", flag, args)
			return
		}
	}
}

func TestCliBeadsAdapter_List_ZeroOpts(t *testing.T) {
	args := listArgs(t, backend.ListOpts{})
	got := strings.Join(args, " ")
	want := "list --json"
	if got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestCliBeadsAdapter_List_BasicFilters(t *testing.T) {
	args := listArgs(t, backend.ListOpts{
		Status:    "open",
		Assignee:  "agent-1",
		IssueType: "task",
		ParentID:  "E-100",
		Limit:     25,
	})
	checks := []struct {
		flag, value string
	}{
		{"--status", "open"},
		{"--assignee", "agent-1"},
		{"--type", "task"},
		{"--parent", "E-100"},
		{"--limit", "25"},
	}
	for _, c := range checks {
		if !containsFlag(args, c.flag, c.value) {
			t.Errorf("expected %s %s in args: %v", c.flag, c.value, args)
		}
	}
}

func TestCliBeadsAdapter_List_PriorityNilVsZero(t *testing.T) {
	t.Run("nil_no_flag", func(t *testing.T) {
		args := listArgs(t, backend.ListOpts{Priority: nil})
		assertNoFlag(t, args, "--priority")
	})

	t.Run("zero_emits_flag", func(t *testing.T) {
		args := listArgs(t, backend.ListOpts{Priority: intPtr(0)})
		if !containsFlag(args, "--priority", "0") {
			t.Errorf("expected --priority 0 in args: %v", args)
		}
	})

	t.Run("nonzero_emits_flag", func(t *testing.T) {
		args := listArgs(t, backend.ListOpts{Priority: intPtr(2)})
		if !containsFlag(args, "--priority", "2") {
			t.Errorf("expected --priority 2 in args: %v", args)
		}
	})
}

func TestCliBeadsAdapter_List_PriorityRange(t *testing.T) {
	t.Run("min_and_max", func(t *testing.T) {
		args := listArgs(t, backend.ListOpts{
			PriorityMin: intPtr(1),
			PriorityMax: intPtr(5),
		})
		if !containsFlag(args, "--priority-min", "1") {
			t.Errorf("expected --priority-min 1 in args: %v", args)
		}
		if !containsFlag(args, "--priority-max", "5") {
			t.Errorf("expected --priority-max 5 in args: %v", args)
		}
	})

	t.Run("zero_values", func(t *testing.T) {
		args := listArgs(t, backend.ListOpts{
			PriorityMin: intPtr(0),
			PriorityMax: intPtr(0),
		})
		if !containsFlag(args, "--priority-min", "0") {
			t.Errorf("expected --priority-min 0 in args: %v", args)
		}
		if !containsFlag(args, "--priority-max", "0") {
			t.Errorf("expected --priority-max 0 in args: %v", args)
		}
	})

	t.Run("nil_no_flags", func(t *testing.T) {
		args := listArgs(t, backend.ListOpts{})
		assertNoFlag(t, args, "--priority-min")
		assertNoFlag(t, args, "--priority-max")
	})
}

func TestCliBeadsAdapter_List_Labels(t *testing.T) {
	args := listArgs(t, backend.ListOpts{
		Labels:    []string{"repo:fe", "urgent"},
		LabelsAny: []string{"A", "B"},
	})
	// Verify --label repo:fe and --label urgent
	if !containsFlag(args, "--label", "repo:fe") {
		t.Errorf("expected --label repo:fe in args: %v", args)
	}
	if !containsFlag(args, "--label", "urgent") {
		t.Errorf("expected --label urgent in args: %v", args)
	}
	// Verify --label-any A and --label-any B
	if !containsFlag(args, "--label-any", "A") {
		t.Errorf("expected --label-any A in args: %v", args)
	}
	if !containsFlag(args, "--label-any", "B") {
		t.Errorf("expected --label-any B in args: %v", args)
	}
}

func TestCliBeadsAdapter_List_PatternMatching(t *testing.T) {
	args := listArgs(t, backend.ListOpts{
		TitleContains:       "login bug",
		DescriptionContains: "null pointer",
		NotesContains:       "workaround",
	})
	checks := []struct {
		flag, value string
	}{
		{"--title-contains", "login bug"},
		{"--desc-contains", "null pointer"},
		{"--notes-contains", "workaround"},
	}
	for _, c := range checks {
		if !containsFlag(args, c.flag, c.value) {
			t.Errorf("expected %s %s in args: %v", c.flag, c.value, args)
		}
	}
}

func TestCliBeadsAdapter_List_DateFilters(t *testing.T) {
	args := listArgs(t, backend.ListOpts{
		CreatedAfter:  "2026-01-01",
		CreatedBefore: "2026-03-01",
		UpdatedAfter:  "2026-02-01",
		UpdatedBefore: "2026-02-28",
		ClosedAfter:   "2026-01-15",
		ClosedBefore:  "2026-03-15",
	})
	checks := []struct {
		flag, value string
	}{
		{"--created-after", "2026-01-01"},
		{"--created-before", "2026-03-01"},
		{"--updated-after", "2026-02-01"},
		{"--updated-before", "2026-02-28"},
		{"--closed-after", "2026-01-15"},
		{"--closed-before", "2026-03-15"},
	}
	for _, c := range checks {
		if !containsFlag(args, c.flag, c.value) {
			t.Errorf("expected %s %s in args: %v", c.flag, c.value, args)
		}
	}
}

func TestCliBeadsAdapter_List_SchedulingFilters(t *testing.T) {
	args := listArgs(t, backend.ListOpts{
		Deferred:    true,
		DeferAfter:  "2026-04-01",
		DeferBefore: "2026-05-01",
		DueAfter:    "2026-04-10",
		DueBefore:   "2026-04-30",
		Overdue:     true,
	})
	checks := []struct {
		flag  string
		value string
	}{
		{"--deferred", ""},
		{"--defer-after", "2026-04-01"},
		{"--defer-before", "2026-05-01"},
		{"--due-after", "2026-04-10"},
		{"--due-before", "2026-04-30"},
		{"--overdue", ""},
	}
	for _, c := range checks {
		if c.value == "" {
			if !containsFlagOnly(args, c.flag) {
				t.Errorf("expected %s flag in args: %v", c.flag, args)
			}
		} else {
			if !containsFlag(args, c.flag, c.value) {
				t.Errorf("expected %s %s in args: %v", c.flag, c.value, args)
			}
		}
	}
}

func TestCliBeadsAdapter_List_BooleanFlags(t *testing.T) {
	args := listArgs(t, backend.ListOpts{
		Overdue:          true,
		Deferred:         true,
		EmptyDescription: true,
		NoAssignee:       true,
		NoLabels:         true,
		IncludeTemplates: true,
		AllowStale:       true,
	})
	flags := []string{
		"--overdue",
		"--deferred",
		"--empty-description",
		"--no-assignee",
		"--no-labels",
		"--include-templates",
		"--allow-stale",
	}
	for _, flag := range flags {
		if !containsFlagOnly(args, flag) {
			t.Errorf("expected %s flag in args: %v", flag, args)
		}
	}
}

func TestCliBeadsAdapter_List_PinnedVariants(t *testing.T) {
	t.Run("nil_no_flag", func(t *testing.T) {
		args := listArgs(t, backend.ListOpts{Pinned: nil})
		assertNoFlag(t, args, "--pinned")
		assertNoFlag(t, args, "--no-pinned")
	})

	t.Run("true_pinned", func(t *testing.T) {
		args := listArgs(t, backend.ListOpts{Pinned: boolPtr(true)})
		if !containsFlagOnly(args, "--pinned") {
			t.Errorf("expected --pinned in args: %v", args)
		}
		assertNoFlag(t, args, "--no-pinned")
	})

	t.Run("false_no_pinned", func(t *testing.T) {
		args := listArgs(t, backend.ListOpts{Pinned: boolPtr(false)})
		if !containsFlagOnly(args, "--no-pinned") {
			t.Errorf("expected --no-pinned in args: %v", args)
		}
		// --pinned should not be present (but --no-pinned starts with --pinned prefix,
		// so check carefully).
		found := false
		for _, a := range args {
			if a == "--pinned" {
				found = true
			}
		}
		if found {
			t.Errorf("expected no --pinned (exact) in args: %v", args)
		}
	})
}

func TestCliBeadsAdapter_List_IDs(t *testing.T) {
	args := listArgs(t, backend.ListOpts{
		IDs: []string{"id1", "id2"},
	})
	if !containsFlag(args, "--id", "id1,id2") {
		t.Errorf("expected --id id1,id2 in args: %v", args)
	}
}

func TestCliBeadsAdapter_List_SourceRepos(t *testing.T) {
	args := listArgs(t, backend.ListOpts{
		SourceRepos: []string{"repo1", "repo2"},
	})
	// The implementation uses --source-repos=repo1,repo2 (= style)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--source-repos=repo1,repo2") {
		t.Errorf("expected --source-repos=repo1,repo2 in args: %v", args)
	}
}

func TestCliBeadsAdapter_List_MolType(t *testing.T) {
	args := listArgs(t, backend.ListOpts{
		MolType: "swarm",
	})
	if !containsFlag(args, "--mol-type", "swarm") {
		t.Errorf("expected --mol-type swarm in args: %v", args)
	}
}

func TestCliBeadsAdapter_List_EphemeralSkipped(t *testing.T) {
	args := listArgs(t, backend.ListOpts{
		Ephemeral: boolPtr(true),
	})
	got := strings.Join(args, " ")
	want := "list --json"
	if got != want {
		t.Errorf("Ephemeral should not produce any CLI flag; args = %q, want %q", got, want)
	}
}

func TestCliBeadsAdapter_List_QuerySkipped(t *testing.T) {
	// Query is a full-text search field used by bd search, not bd list.
	// cliBeadsAdapter.List intentionally skips it.
	args := listArgs(t, backend.ListOpts{
		Query: "authentication bug",
	})
	got := strings.Join(args, " ")
	want := "list --json"
	if got != want {
		t.Errorf("Query should not produce any CLI flag; args = %q, want %q", got, want)
	}
}

// --- Close method tests ---

func TestCliBeadsAdapter_Close_Args(t *testing.T) {
	runner := &mockBDRunner{
		fn: func(_ string, _ ...string) CommandResult {
			return CommandResult{Stdout: `{"closed": [{"id": "T-1", "title": "Task One", "status": "closed", "priority": 2, "issue_type": "task"}]}`}
		},
	}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	_, err := a.Close(context.Background(), "T-1", backend.CloseParams{Reason: "done"})
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
	got := strings.Join(runner.calls[0].Args, " ")
	want := "close T-1 --suggest-next --json --reason done"
	if got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestCliBeadsAdapter_Close_NoReason(t *testing.T) {
	runner := &mockBDRunner{
		fn: func(_ string, _ ...string) CommandResult {
			return CommandResult{Stdout: `{"closed": [{"id": "T-1", "title": "Task One", "status": "closed", "priority": 2, "issue_type": "task"}]}`}
		},
	}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	_, err := a.Close(context.Background(), "T-1", backend.CloseParams{})
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
	got := strings.Join(runner.calls[0].Args, " ")
	want := "close T-1 --suggest-next --json"
	if got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestCliBeadsAdapter_Close_FormatA_WithUnblocked(t *testing.T) {
	jsonOut := `{"closed": [{"id": "T-1", "title": "Task One", "status": "closed", "priority": 2, "issue_type": "task"}], "unblocked": [{"id": "T-2", "title": "Task Two", "status": "open", "priority": 1, "issue_type": "bug"}, {"id": "T-3", "title": "Task Three", "status": "open", "priority": 3, "issue_type": "feature"}]}`
	runner := &mockBDRunner{
		fn: func(_ string, _ ...string) CommandResult {
			return CommandResult{Stdout: jsonOut}
		},
	}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	cr, err := a.Close(context.Background(), "T-1", backend.CloseParams{Reason: "complete"})
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if cr == nil {
		t.Fatal("expected non-nil CloseResult")
	}
	if cr.Closed == nil {
		t.Fatal("expected non-nil Closed")
	}
	if cr.Closed.ID != "T-1" {
		t.Errorf("Closed.ID = %q, want %q", cr.Closed.ID, "T-1")
	}
	if cr.Closed.Title != "Task One" {
		t.Errorf("Closed.Title = %q, want %q", cr.Closed.Title, "Task One")
	}
	if cr.Closed.Status != "closed" {
		t.Errorf("Closed.Status = %q, want %q", cr.Closed.Status, "closed")
	}
	if cr.Closed.Priority != 2 {
		t.Errorf("Closed.Priority = %d, want %d", cr.Closed.Priority, 2)
	}
	if cr.Closed.IssueType != "task" {
		t.Errorf("Closed.IssueType = %q, want %q", cr.Closed.IssueType, "task")
	}
	if len(cr.Unblocked) != 2 {
		t.Fatalf("expected 2 unblocked issues, got %d", len(cr.Unblocked))
	}
	if cr.Unblocked[0].ID != "T-2" {
		t.Errorf("Unblocked[0].ID = %q, want %q", cr.Unblocked[0].ID, "T-2")
	}
	if cr.Unblocked[0].IssueType != "bug" {
		t.Errorf("Unblocked[0].IssueType = %q, want %q", cr.Unblocked[0].IssueType, "bug")
	}
	if cr.Unblocked[1].ID != "T-3" {
		t.Errorf("Unblocked[1].ID = %q, want %q", cr.Unblocked[1].ID, "T-3")
	}
	if cr.Unblocked[1].IssueType != "feature" {
		t.Errorf("Unblocked[1].IssueType = %q, want %q", cr.Unblocked[1].IssueType, "feature")
	}
}

func TestCliBeadsAdapter_Close_FormatB_Array(t *testing.T) {
	jsonOut := `[{"id": "T-1", "title": "Task One", "status": "closed", "priority": 2, "issue_type": "task"}]`
	runner := &mockBDRunner{
		fn: func(_ string, _ ...string) CommandResult {
			return CommandResult{Stdout: jsonOut}
		},
	}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	cr, err := a.Close(context.Background(), "T-1", backend.CloseParams{})
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if cr == nil {
		t.Fatal("expected non-nil CloseResult")
	}
	if cr.Closed == nil {
		t.Fatal("expected non-nil Closed")
	}
	if cr.Closed.ID != "T-1" {
		t.Errorf("Closed.ID = %q, want %q", cr.Closed.ID, "T-1")
	}
	if cr.Closed.Status != "closed" {
		t.Errorf("Closed.Status = %q, want %q", cr.Closed.Status, "closed")
	}
	if cr.Unblocked != nil {
		t.Errorf("expected nil Unblocked, got %v", cr.Unblocked)
	}
}

func TestCliBeadsAdapter_Close_EmptyStdout(t *testing.T) {
	runner := &mockBDRunner{
		fn: func(_ string, _ ...string) CommandResult {
			return CommandResult{Stdout: ""}
		},
	}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	cr, err := a.Close(context.Background(), "T-1", backend.CloseParams{})
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if cr == nil {
		t.Fatal("expected non-nil CloseResult")
	}
	if cr.Closed != nil {
		t.Errorf("expected nil Closed for empty stdout, got %v", cr.Closed)
	}
	if cr.Unblocked != nil {
		t.Errorf("expected nil Unblocked for empty stdout, got %v", cr.Unblocked)
	}
}

func TestCliBeadsAdapter_Close_UnparsableJSON(t *testing.T) {
	runner := &mockBDRunner{
		fn: func(_ string, _ ...string) CommandResult {
			return CommandResult{Stdout: "not json at all"}
		},
	}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	cr, err := a.Close(context.Background(), "T-1", backend.CloseParams{})
	if err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
	if cr == nil {
		t.Fatal("expected non-nil CloseResult")
	}
	if cr.Closed != nil {
		t.Errorf("expected nil Closed for unparsable stdout, got %v", cr.Closed)
	}
	if cr.Unblocked != nil {
		t.Errorf("expected nil Unblocked for unparsable stdout, got %v", cr.Unblocked)
	}
}

func TestCliBeadsAdapter_Close_RunnerError(t *testing.T) {
	runner := &mockBDRunner{
		fn: func(_ string, _ ...string) CommandResult {
			return CommandResult{
				Err:    fmt.Errorf("exit status 1"),
				Stderr: "unexpected error",
			}
		},
	}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	cr, err := a.Close(context.Background(), "T-1", backend.CloseParams{Reason: "done"})
	if err == nil {
		t.Fatal("expected error for runner failure")
	}
	if cr != nil {
		t.Errorf("expected nil CloseResult on error, got %v", cr)
	}
}

func TestCliBeadsAdapter_Close_Force(t *testing.T) {
	runner := &mockBDRunner{
		fn: func(_ string, _ ...string) CommandResult {
			return CommandResult{Stdout: `[{"id":"T-1","title":"Pinned","status":"closed","priority":1,"issue_type":"task"}]`}
		},
	}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	_, err := a.Close(context.Background(), "T-1", backend.CloseParams{Reason: "force it", Force: true})
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
	got := strings.Join(runner.calls[0].Args, " ")
	want := "close T-1 --suggest-next --json --reason force it --force"
	if got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestCliBeadsAdapter_Close_FormatA_EmptyClosedWithUnblocked(t *testing.T) {
	jsonOutput := `{"closed": [], "unblocked": [{"id":"T-2","title":"Unblocked One","status":"open","priority":1,"issue_type":"bug"}]}`
	runner := &mockBDRunner{
		fn: func(_ string, _ ...string) CommandResult {
			return CommandResult{Stdout: jsonOutput}
		},
	}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	cr, err := a.Close(context.Background(), "T-1", backend.CloseParams{})
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if cr == nil {
		t.Fatal("expected non-nil CloseResult")
	}
	if cr.Closed != nil {
		t.Errorf("expected nil Closed when closed array is empty, got %v", cr.Closed)
	}
	if len(cr.Unblocked) != 1 {
		t.Fatalf("expected 1 unblocked issue, got %d", len(cr.Unblocked))
	}
	if cr.Unblocked[0].ID != "T-2" {
		t.Errorf("Unblocked[0].ID = %q, want T-2", cr.Unblocked[0].ID)
	}
}

func TestCliBeadsAdapter_Close_NotFoundError(t *testing.T) {
	runner := &mockBDRunner{
		fn: func(_ string, _ ...string) CommandResult {
			return CommandResult{
				Err:    fmt.Errorf("exit status 1"),
				Stderr: "issue not found",
			}
		},
	}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	cr, err := a.Close(context.Background(), "T-99", backend.CloseParams{})
	if err == nil {
		t.Fatal("expected error for not found")
	}
	if cr != nil {
		t.Errorf("expected nil CloseResult on error, got %v", cr)
	}
	var be *backend.BackendError
	if !errors.As(err, &be) {
		t.Fatalf("expected *backend.BackendError, got %T", err)
	}
	if be.Kind != backend.KindNotFound {
		t.Errorf("BackendError.Kind = %q, want %q", be.Kind, backend.KindNotFound)
	}
}

func TestCliBeadsAdapter_List_AllFields(t *testing.T) {
	args := listArgs(t, backend.ListOpts{
		// Basic filters.
		Status:    "in_progress",
		Assignee:  "agent-99",
		IssueType: "epic",
		ParentID:  "E-1",
		Limit:     50,
		IDs:       []string{"T-1", "T-2", "T-3"},

		// Priority.
		Priority:    intPtr(3),
		PriorityMin: intPtr(1),
		PriorityMax: intPtr(5),

		// Labels.
		Labels:    []string{"repo:be", "critical"},
		LabelsAny: []string{"p0", "p1"},

		// Pattern matching.
		TitleContains:       "auth",
		DescriptionContains: "token refresh",
		NotesContains:       "retry",

		// Date range filters.
		CreatedAfter:  "2026-01-01T00:00:00Z",
		CreatedBefore: "2026-06-01T00:00:00Z",
		UpdatedAfter:  "2026-03-01T00:00:00Z",
		UpdatedBefore: "2026-04-01T00:00:00Z",
		ClosedAfter:   "2026-02-01T00:00:00Z",
		ClosedBefore:  "2026-05-01T00:00:00Z",

		// Empty/null checks.
		EmptyDescription: true,
		NoAssignee:       true,
		NoLabels:         true,

		// Pinned.
		Pinned: boolPtr(true),

		// Ephemeral (should be ignored).
		Ephemeral: boolPtr(true),

		// Templates/mol.
		IncludeTemplates: true,
		MolType:          "swarm",

		// Scheduling.
		Deferred:    true,
		DeferAfter:  "2026-04-01",
		DeferBefore: "2026-05-01",
		DueAfter:    "2026-04-10",
		DueBefore:   "2026-04-30",
		Overdue:     true,

		// Multi-repo.
		SourceRepos: []string{"repoA", "repoB"},

		// Performance hints.
		AllowStale: true,
	})

	// All expected flag/value pairs.
	flagValueChecks := []struct {
		flag, value string
	}{
		{"--status", "in_progress"},
		{"--assignee", "agent-99"},
		{"--type", "epic"},
		{"--parent", "E-1"},
		{"--limit", "50"},
		{"--id", "T-1,T-2,T-3"},
		{"--priority", "3"},
		{"--priority-min", "1"},
		{"--priority-max", "5"},
		{"--label", "repo:be"},
		{"--label", "critical"},
		{"--label-any", "p0"},
		{"--label-any", "p1"},
		{"--title-contains", "auth"},
		{"--desc-contains", "token refresh"},
		{"--notes-contains", "retry"},
		{"--created-after", "2026-01-01T00:00:00Z"},
		{"--created-before", "2026-06-01T00:00:00Z"},
		{"--updated-after", "2026-03-01T00:00:00Z"},
		{"--updated-before", "2026-04-01T00:00:00Z"},
		{"--closed-after", "2026-02-01T00:00:00Z"},
		{"--closed-before", "2026-05-01T00:00:00Z"},
		{"--defer-after", "2026-04-01"},
		{"--defer-before", "2026-05-01"},
		{"--due-after", "2026-04-10"},
		{"--due-before", "2026-04-30"},
		{"--mol-type", "swarm"},
	}
	for _, c := range flagValueChecks {
		if !containsFlag(args, c.flag, c.value) {
			t.Errorf("expected %s %s in args: %v", c.flag, c.value, args)
		}
	}

	// Boolean-only flags.
	boolFlags := []string{
		"--deferred",
		"--overdue",
		"--empty-description",
		"--no-assignee",
		"--no-labels",
		"--pinned",
		"--include-templates",
		"--allow-stale",
	}
	for _, flag := range boolFlags {
		if !containsFlagOnly(args, flag) {
			t.Errorf("expected %s flag in args: %v", flag, args)
		}
	}

	// --source-repos uses = style.
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--source-repos=repoA,repoB") {
		t.Errorf("expected --source-repos=repoA,repoB in args: %v", args)
	}

	// Ephemeral should NOT produce any flag.
	assertNoFlag(t, args, "--ephemeral")
	assertNoFlag(t, args, "--no-ephemeral")

	// --no-pinned should NOT be present since Pinned=true.
	assertNoFlag(t, args, "--no-pinned")
}

// --- Update with AgentState tests ---

func TestCliBeadsAdapter_Update_AgentState_LogsWarningAndContinues(t *testing.T) {
	runner := &mockBDRunner{}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	state := "running"
	status := "in_progress"
	err := a.Update(context.Background(), "T-42", backend.UpdateParams{
		Status:     &status,
		AgentState: &state,
	})
	if err != nil {
		t.Fatalf("Update() error = %v, expected nil (AgentState should be logged but not cause error)", err)
	}
	// Runner should still be called with the other fields.
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
	got := strings.Join(runner.calls[0].Args, " ")
	if !strings.Contains(got, "--status") {
		t.Errorf("expected --status in args: %q", got)
	}
}

func TestCliBeadsAdapter_Update_AgentState_OnlyField(t *testing.T) {
	runner := &mockBDRunner{}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	state := "idle"
	err := a.Update(context.Background(), "T-43", backend.UpdateParams{
		AgentState: &state,
	})
	if err != nil {
		t.Fatalf("Update() error = %v, expected nil", err)
	}
	// Runner should be called even if AgentState is the only field
	// (bd update T-43 with no extra flags).
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
}

func TestCliBeadsAdapter_Update_NilAgentState(t *testing.T) {
	runner := &mockBDRunner{}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	status := "open"
	err := a.Update(context.Background(), "T-44", backend.UpdateParams{
		Status: &status,
		// AgentState is nil — no warning should be logged
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
}
