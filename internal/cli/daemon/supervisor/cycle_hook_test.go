package supervisor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// cycleRecorder captures the label operations in the order they were issued.
// Order is the entire crash-safety argument here, so asserting the resulting
// label set would miss the property under test.
func cycleRecorder(labels []string) (*clitest.MockIssueBackend, *[]string) {
	ops := &[]string{}
	m := clitest.NewMockIssueBackend()
	m.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
		return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Labels: labels}}, nil
	}
	m.AddLabelFn = func(_ context.Context, _ string, l string) error {
		*ops = append(*ops, "add:"+l)
		return nil
	}
	m.RemoveLabelFn = func(_ context.Context, _ string, l string) error {
		*ops = append(*ops, "remove:"+l)
		return nil
	}
	return m, ops
}

func testCycle(threshold int) *domain.AgentHookCycle {
	return &domain.AgentHookCycle{Threshold: threshold, RearmLabel: "criticized", ShipLabel: "ready"}
}

// Mid-loop, the re-arm label must be removed BEFORE the counter is bumped.
//
// Bumping first is wrong in a way that hides: a crash in between leaves the
// re-arm label present alongside the advanced counter, indistinguishable from a
// round that already ran, so the next pass skips a review and the task ships
// under-reviewed. Removing first can only repeat a round.
func TestAdvanceReviewCycle_RearmBeforeBump(t *testing.T) {
	m, ops := cycleRecorder([]string{"plan", "criticized"})
	s := &Supervisor{IssueBackend: m}

	if err := s.advanceReviewCycle(context.Background(), "T-1", testCycle(3)); err != nil {
		t.Fatalf("advanceReviewCycle: %v", err)
	}

	got := strings.Join(*ops, " ")
	want := "remove:criticized add:review-cycle=1"
	if got != want {
		t.Errorf("ops = %q, want %q", got, want)
	}
}

// The ship branch writes NO counter, so a shipped task's highest counter is
// threshold-1. "N rounds ran" is observable from comments, not labels.
func TestAdvanceReviewCycle_ShipsWithoutWritingACounter(t *testing.T) {
	m, ops := cycleRecorder([]string{"plan", "criticized", "review-cycle=2"})
	s := &Supervisor{IssueBackend: m}

	if err := s.advanceReviewCycle(context.Background(), "T-1", testCycle(3)); err != nil {
		t.Fatalf("advanceReviewCycle: %v", err)
	}

	got := strings.Join(*ops, " ")
	if got != "add:ready" {
		t.Errorf("ops = %q, want only the ship label", got)
	}
	if strings.Contains(got, "review-cycle") {
		t.Error("the ship branch must not write a counter")
	}
}

// Threshold 1 is the degenerate single-pass flow: ship immediately, no counter.
func TestAdvanceReviewCycle_ThresholdOneShipsImmediately(t *testing.T) {
	m, ops := cycleRecorder([]string{"plan", "criticized"})
	s := &Supervisor{IssueBackend: m}

	if err := s.advanceReviewCycle(context.Background(), "T-1", testCycle(1)); err != nil {
		t.Fatalf("advanceReviewCycle: %v", err)
	}

	if got := strings.Join(*ops, " "); got != "add:ready" {
		t.Errorf("ops = %q, want an immediate ship with no counter", got)
	}
}

// Stale counters are cleaned up after the bump, and the max drives the decision
// so a survivor is harmless.
func TestAdvanceReviewCycle_CleansStaleCountersAfterBump(t *testing.T) {
	m, ops := cycleRecorder([]string{"criticized", "review-cycle=1", "review-cycle=2"})
	s := &Supervisor{IssueBackend: m}

	if err := s.advanceReviewCycle(context.Background(), "T-1", testCycle(5)); err != nil {
		t.Fatalf("advanceReviewCycle: %v", err)
	}

	got := *ops
	if len(got) < 2 || got[0] != "remove:criticized" || got[1] != "add:review-cycle=3" {
		t.Fatalf("ops = %v, want re-arm then bump to 3 first", got)
	}
	rest := strings.Join(got[2:], " ")
	for _, stale := range []string{"remove:review-cycle=1", "remove:review-cycle=2"} {
		if !strings.Contains(rest, stale) {
			t.Errorf("stale counter not cleaned: %v", got)
		}
	}
}

// Cleanup is best-effort: CompletedRounds takes the max, so a survivor cannot
// change the next decision. Failing the pipeline over it would demote a run that
// actually succeeded.
func TestAdvanceReviewCycle_CleanupFailureDoesNotFailTheRun(t *testing.T) {
	m, _ := cycleRecorder([]string{"criticized", "review-cycle=1"})
	m.RemoveLabelFn = func(_ context.Context, _ string, l string) error {
		if strings.HasPrefix(l, "review-cycle=") {
			return fmt.Errorf("boom")
		}
		return nil
	}
	s := &Supervisor{IssueBackend: m}

	if err := s.advanceReviewCycle(context.Background(), "T-1", testCycle(4)); err != nil {
		t.Errorf("a failed stale-counter cleanup must not fail the run: %v", err)
	}
}

// A failed re-arm MUST fail the run: the previous stage was never handed the
// task back, so silently continuing would strand the loop.
func TestAdvanceReviewCycle_RearmFailureFailsTheRun(t *testing.T) {
	m, _ := cycleRecorder([]string{"criticized"})
	m.RemoveLabelFn = func(_ context.Context, _ string, _ string) error { return fmt.Errorf("boom") }
	s := &Supervisor{IssueBackend: m}

	err := s.advanceReviewCycle(context.Background(), "T-1", testCycle(3))
	if err == nil {
		t.Fatal("a failed re-arm must fail the run")
	}
	if !strings.Contains(err.Error(), "re-arm") {
		t.Errorf("error should name the re-arm: %v", err)
	}
}

// The re-arm must leave the task claimable, or the loop stalls with correct
// labels and nothing able to act on them: task_router rejects anything not
// `open`. A planning stage routinely finishes at `review`, so that state has to
// advance rather than gate.
func TestAdvanceReviewCycle_ReopensSoThePreviousStageCanClaim(t *testing.T) {
	for _, status := range []string{"review", "in_progress"} {
		t.Run(status, func(t *testing.T) {
			var updated string
			m := clitest.NewMockIssueBackend()
			m.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
				return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Status: status, Labels: []string{"criticized"}}}, nil
			}
			m.UpdateFn = func(_ context.Context, _ string, p backend.UpdateParams) error {
				if p.Status != nil {
					updated = *p.Status
				}
				return nil
			}
			s := &Supervisor{IssueBackend: m}

			if err := s.advanceReviewCycle(context.Background(), "T-1", testCycle(3)); err != nil {
				t.Fatalf("advanceReviewCycle: %v", err)
			}
			if updated != "open" {
				t.Errorf("status after re-arm = %q, want open so the previous stage can claim", updated)
			}
		})
	}
}

// closed and blocked are decisions somebody made: closed is terminal, blocked is
// how a stage escalates to a human. The loop must not drag a task out of either.
func TestAdvanceReviewCycle_DoesNotOverrideADeliberateStop(t *testing.T) {
	for _, status := range []string{"closed", "blocked"} {
		t.Run(status, func(t *testing.T) {
			touched := false
			m := clitest.NewMockIssueBackend()
			m.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
				return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Status: status, Labels: []string{"criticized"}}}, nil
			}
			m.UpdateFn = func(_ context.Context, _ string, _ backend.UpdateParams) error {
				touched = true
				return nil
			}
			s := &Supervisor{IssueBackend: m}

			if err := s.advanceReviewCycle(context.Background(), "T-1", testCycle(3)); err != nil {
				t.Fatalf("advanceReviewCycle: %v", err)
			}
			if touched {
				t.Errorf("a %s task must not be reopened by the loop", status)
			}
		})
	}
}
