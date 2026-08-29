package monitor

// Tests for the ready-by-priority histogram that backs the loom_ready_tasks
// Prometheus gauge. Kept in their own file: monitor_test.go is at its recorded
// LOC ceiling, and that ratchet is meant to be shrunk, not raised.

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
)

// TestCollectTaskStatusReadyByPriority verifies that the priority histogram is
// built from the single scoped Ready() call that also feeds the summary counts
// -- it used to have a second, unscoped Ready() of its own, which is how the
// loom_ready_tasks gauge drifted away from `loom data ready`.
func TestCollectTaskStatusReadyByPriority(t *testing.T) {
	// not parallel: uses setDefaultIssueBackend
	mock := NewMockIssueBackend()
	readyCalls := 0
	var capturedOpts backend.ReadyOpts
	mock.ReadyFn = func(_ context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
		readyCalls++
		capturedOpts = opts
		return []backend.IssueData{
			{ID: "T-1", Title: "P1 task", Status: "open", Priority: 1, Design: "plan", IssueType: "task"},
			{ID: "T-2", Title: "P2 task", Status: "open", Priority: 2, Design: "", IssueType: "task"},
		}, nil
	}
	mock.ListFn = func(_ context.Context, _ backend.ListOpts) ([]backend.IssueData, error) {
		return nil, nil
	}
	setDefaultIssueBackend(mock)
	defer resetDefaultIssueBackend()

	summary, _, _, _, _, _, _, _ := collectTaskStatus(50)

	if capturedOpts.Limit != 50 {
		t.Errorf("Ready() called with Limit=%d, want 50", capturedOpts.Limit)
	}
	if readyCalls != 1 {
		t.Errorf("Ready() called %d times, want exactly 1 scoped call", readyCalls)
	}
	if summary.ReadyByPriority[1] != 1 {
		t.Errorf("ReadyByPriority[1] = %d, want 1", summary.ReadyByPriority[1])
	}
	if summary.ReadyByPriority[2] != 1 {
		t.Errorf("ReadyByPriority[2] = %d, want 1", summary.ReadyByPriority[2])
	}
}

// TestProcessReadyIssuesReadyByPriority covers the histogram's exclusion set
// and its clamping of out-of-range priorities.
func TestProcessReadyIssuesReadyByPriority(t *testing.T) {
	t.Parallel()

	issues := []backend.IssueData{
		{ID: "T-0", Status: "open", Priority: 0, Design: "plan", IssueType: "task"},
		{ID: "T-1", Status: "open", Priority: 2, Design: "plan", IssueType: "task"},
		{ID: "T-2", Status: "open", Priority: 2, Design: "", IssueType: "task"},
		// Out of range in both directions: both fold into bucket 4.
		{ID: "T-3", Status: "open", Priority: 9, Design: "plan", IssueType: "task"},
		{ID: "T-4", Status: "open", Priority: -1, Design: "plan", IssueType: "task"},
		// Excluded: epic, non-work type, blocked, needs-revision, closed.
		{ID: "T-5", Status: "open", Priority: 1, IssueType: "epic"},
		{ID: "T-6", Status: "open", Priority: 1, IssueType: "gate"},
		{ID: "T-7", Status: "open", Priority: 1, Design: "plan", IssueType: "task"},
		{ID: "T-8", Status: "open", Priority: 1, Design: "plan", IssueType: "task", Labels: []string{cli.NeedsRevisionLabel}},
		{ID: "T-9", Status: "closed", Priority: 1, Design: "plan", IssueType: "task"},
	}

	var summary TaskSummary
	processReadyIssues(issues, nil, &summary, map[string]bool{"T-7": true})

	got := summary.ReadyByPriority
	if got == nil {
		t.Fatal("ReadyByPriority is nil, want all five buckets present")
	}
	want := map[int]int{0: 1, 1: 0, 2: 2, 3: 0, 4: 2}
	for p := 0; p <= 4; p++ {
		if _, ok := got[p]; !ok {
			t.Errorf("bucket %d missing; every bucket must be present even at 0", p)
			continue
		}
		if got[p] != want[p] {
			t.Errorf("ReadyByPriority[%d] = %d, want %d", p, got[p], want[p])
		}
	}
	if len(got) != 5 {
		t.Errorf("len(ReadyByPriority) = %d, want 5", len(got))
	}
}

// TestProcessReadyIssuesReadyByPriorityConsistency pins the one deliberate
// divergence between the histogram and the summary counts: ReadyToImplement and
// NeedsPlanning count needs-revision issues, the histogram does not. Anything
// else diverging means the two have drifted apart again.
func TestProcessReadyIssuesReadyByPriorityConsistency(t *testing.T) {
	t.Parallel()

	issues := []backend.IssueData{
		{ID: "T-1", Status: "open", Priority: 0, Design: "plan", IssueType: "task"},
		{ID: "T-2", Status: "open", Priority: 1, Design: "", IssueType: "task"},
		{ID: "T-3", Status: "open", Priority: 3, Design: "plan", IssueType: "task"},
		{ID: "T-4", Status: "open", Priority: 2, Design: "plan", IssueType: "task", Labels: []string{cli.NeedsRevisionLabel}},
		{ID: "T-5", Status: "open", Priority: 2, Design: "", IssueType: "task", Labels: []string{cli.NeedsRevisionLabel}},
		{ID: "T-6", Status: "open", Priority: 1, IssueType: "epic"},
	}

	var summary TaskSummary
	processReadyIssues(issues, nil, &summary, nil)

	sum := 0
	for _, count := range summary.ReadyByPriority {
		sum += count
	}
	needsRevision := 2
	want := summary.ReadyToImplement + summary.NeedsPlanning - needsRevision
	if sum != want {
		t.Errorf("sum(ReadyByPriority) = %d, want ReadyToImplement(%d)+NeedsPlanning(%d)-needsRevision(%d) = %d",
			sum, summary.ReadyToImplement, summary.NeedsPlanning, needsRevision, want)
	}
	if sum == 0 {
		t.Fatal("fixture produced an all-zero histogram; an empty fixture is what let the zero gauge ship")
	}
}
