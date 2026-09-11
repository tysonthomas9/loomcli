package driver

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/taskcontent"
)

// A row with a title and nothing else carries no work; the drain must step over
// it exactly the way it steps over an excluded label.
func TestClaimReadyTask_SkipsBodylessTask(t *testing.T) {
	ready := []backend.IssueData{
		{ID: "scratch", Status: "open", Title: "tester control subject"},
		{ID: "real", Status: "open", Title: "real work"},
	}
	var attempted []string
	m := clitest.NewMockIssueBackend()
	m.ReadyFn = func(_ context.Context, _ backend.ReadyOpts) ([]backend.IssueData, error) { return ready, nil }
	m.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
		d := &backend.IssueDetailData{IssueData: backend.IssueData{ID: id}}
		if id == "real" {
			d.Description = "there is something to do here"
		}
		return d, nil
	}
	m.ClaimIssueFn = func(_ context.Context, id string, _ time.Duration) error {
		attempted = append(attempted, id)
		return nil
	}

	got, err := ClaimReadyTask(context.Background(), m, TaskClaimOptions{ContentGate: taskcontent.NewGate()})
	if err != nil {
		t.Fatalf("ClaimReadyTask: %v", err)
	}
	if got == nil || got.ID != "real" {
		t.Fatalf("claimed = %+v, want real (scratch is bodyless)", got)
	}
	for _, id := range attempted {
		if id == "scratch" {
			t.Fatal("must not even attempt to claim a bodyless task")
		}
	}
}

func TestClaimReadyTask_ContentGateFailsOpen(t *testing.T) {
	ready := []backend.IssueData{{ID: "T-1", Status: "open", Title: "unknown content"}}
	m := clitest.NewMockIssueBackend()
	m.ReadyFn = func(_ context.Context, _ backend.ReadyOpts) ([]backend.IssueData, error) { return ready, nil }
	m.GetFn = func(_ context.Context, _ string) (*backend.IssueDetailData, error) {
		return nil, fmt.Errorf("backend unreachable")
	}
	m.ClaimIssueFn = func(_ context.Context, _ string, _ time.Duration) error { return nil }

	got, err := ClaimReadyTask(context.Background(), m, TaskClaimOptions{ContentGate: taskcontent.NewGate()})
	if err != nil {
		t.Fatalf("ClaimReadyTask: %v", err)
	}
	if got == nil || got.ID != "T-1" {
		t.Fatalf("claimed = %+v, want T-1: a read failure must never stop the drain", got)
	}
}

// A nil ContentGate still gates — ClaimReadyTask constructs a call-local one —
// so every existing caller gets the invariant without a source change.
func TestClaimReadyTask_NilOptionStillGates(t *testing.T) {
	ready := []backend.IssueData{{ID: "scratch", Status: "open", Title: "tester control subject"}}
	m := clitest.NewMockIssueBackend()
	m.ReadyFn = func(_ context.Context, _ backend.ReadyOpts) ([]backend.IssueData, error) { return ready, nil }
	m.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
		return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id}}, nil
	}
	m.ClaimIssueFn = func(_ context.Context, _ string, _ time.Duration) error { return nil }

	got, err := ClaimReadyTask(context.Background(), m, TaskClaimOptions{})
	if err != nil {
		t.Fatalf("ClaimReadyTask: %v", err)
	}
	if got != nil {
		t.Fatalf("claimed = %+v, want nil (the only ready row is bodyless)", got)
	}
}
