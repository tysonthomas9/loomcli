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
