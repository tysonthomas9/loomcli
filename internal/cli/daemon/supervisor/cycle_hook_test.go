package supervisor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
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

// The ship branch writes NO counter — it only ever removes them. "N rounds ran"
// is observable from comments, not labels. Asserted as a property rather than an
// exact op string, because the ship path also clears the re-arm label and the
// counters it finds.
func TestAdvanceReviewCycle_ShipWritesNoCounter(t *testing.T) {
	m, ops := cycleRecorder([]string{"plan", "criticized", "review-cycle=2"})
	s := &Supervisor{IssueBackend: m}

	if err := s.advanceReviewCycle(context.Background(), "T-1", testCycle(3)); err != nil {
		t.Fatalf("advanceReviewCycle: %v", err)
	}

	for _, op := range *ops {
		if strings.HasPrefix(op, "add:") && strings.Contains(op, "review-cycle") {
			t.Errorf("the ship branch must not write a counter: ops = %v", *ops)
		}
	}
}

// The regression test for the re-ship spin: shipping must clear the re-arm
// label, or the previous stage's filter still matches and it re-claims the task.
//
// Order is asserted, not just the set: remove-before-stamp is the crash-safety
// property, and the inverse order reproduces the both-labels-present bug on
// every crash.
func TestAdvanceReviewCycle_ShipClearsTheRearmLabel(t *testing.T) {
	m, ops := cycleRecorder([]string{"plan", "criticized", "review-cycle=2"})
	s := &Supervisor{IssueBackend: m}

	if err := s.advanceReviewCycle(context.Background(), "T-1", testCycle(3)); err != nil {
		t.Fatalf("advanceReviewCycle: %v", err)
	}

	got := *ops
	if len(got) < 2 || got[0] != "remove:criticized" || got[1] != "add:ready" {
		t.Fatalf("ops = %v, want the re-arm cleared before the ship label is stamped", got)
	}
	for _, op := range got {
		if op == "add:criticized" {
			t.Errorf("the ship branch must never re-add the re-arm label: %v", got)
		}
	}
}

// The end-to-end property, and the one that earns this change: a second pass
// over the shipped label set must NOT ship again.
//
// The single-call assertions above would all pass against the buggy code paired
// with a lenient matcher; only running the loop twice against a stateful label
// set exposes the spin.
func TestAdvanceReviewCycle_ShipDoesNotReShip(t *testing.T) {
	labels := []string{"plan", "criticized", "review-cycle=2"}
	var ops []string
	m := clitest.NewMockIssueBackend()
	m.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
		snapshot := append([]string(nil), labels...)
		return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Status: "open", Labels: snapshot}}, nil
	}
	m.AddLabelFn = func(_ context.Context, _ string, l string) error {
		ops = append(ops, "add:"+l)
		if !containsLabel(labels, l) {
			labels = append(labels, l)
		}
		return nil
	}
	m.RemoveLabelFn = func(_ context.Context, _ string, l string) error {
		ops = append(ops, "remove:"+l)
		kept := labels[:0]
		for _, existing := range labels {
			if existing != l {
				kept = append(kept, existing)
			}
		}
		labels = kept
		return nil
	}
	s := &Supervisor{IssueBackend: m}

	if err := s.advanceReviewCycle(context.Background(), "T-1", testCycle(3)); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if !containsLabel(labels, "ready") || containsLabel(labels, "criticized") {
		t.Fatalf("labels after ship = %v, want the ship label and no re-arm label", labels)
	}

	// The previous stage is gone, but simulate it re-claiming anyway: with the
	// re-arm label and the counters cleared, CompletedRounds is 0, so the second
	// pass must re-arm at round 1 rather than re-enter the ship branch.
	ops = nil
	if err := s.advanceReviewCycle(context.Background(), "T-1", testCycle(3)); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	for _, op := range ops {
		if op == "add:ready" {
			t.Fatalf("the shipped task re-shipped on the next pass: ops = %v", ops)
		}
	}
	if got := strings.Join(ops, " "); got != "remove:criticized add:review-cycle=1" {
		t.Errorf("second-pass ops = %q, want a re-arm at round 1", got)
	}
}

// Counters are the cycle's memory. A survivor makes any later re-entry compute
// threshold-1 + 1 and ship with zero rounds run — a skipped review. They are
// cleared AFTER the ship label lands, so a crash mid-cleanup leaves a completed
// hand-off with cosmetic litter rather than an unshipped task.
func TestAdvanceReviewCycle_ShipClearsCounters(t *testing.T) {
	m, ops := cycleRecorder([]string{"criticized", "review-cycle=1", "review-cycle=2"})
	s := &Supervisor{IssueBackend: m}

	if err := s.advanceReviewCycle(context.Background(), "T-1", testCycle(3)); err != nil {
		t.Fatalf("advanceReviewCycle: %v", err)
	}

	got := *ops
	if len(got) < 2 || got[0] != "remove:criticized" || got[1] != "add:ready" {
		t.Fatalf("ops = %v, want re-arm cleared then ship stamped first", got)
	}
	rest := strings.Join(got[2:], " ")
	for _, counter := range []string{"remove:review-cycle=1", "remove:review-cycle=2"} {
		if !strings.Contains(rest, counter) {
			t.Errorf("counter not cleared after ship: %v", got)
		}
	}
}

// Counter cleanup on the ship path is best-effort for the same reason as the
// re-arm branch's: the hand-off is already complete, so failing the run would
// demote a run that actually succeeded.
func TestAdvanceReviewCycle_ShipCounterCleanupFailureDoesNotFailTheRun(t *testing.T) {
	m, _ := cycleRecorder([]string{"criticized", "review-cycle=2"})
	m.RemoveLabelFn = func(_ context.Context, _ string, l string) error {
		if strings.HasPrefix(l, "review-cycle=") {
			return fmt.Errorf("boom")
		}
		return nil
	}
	s := &Supervisor{IssueBackend: m}

	if err := s.advanceReviewCycle(context.Background(), "T-1", testCycle(3)); err != nil {
		t.Errorf("a failed counter cleanup must not fail the ship: %v", err)
	}
}

// A failed re-arm removal on the ship path MUST fail the run, and must fail
// BEFORE the ship label is stamped: the alternative is the double-labeled
// reopen this whole change exists to prevent.
func TestAdvanceReviewCycle_ShipRearmRemovalFailureFailsTheRun(t *testing.T) {
	stamped := false
	m, _ := cycleRecorder([]string{"criticized", "review-cycle=2"})
	m.RemoveLabelFn = func(_ context.Context, _ string, l string) error {
		if l == "criticized" {
			return fmt.Errorf("boom")
		}
		return nil
	}
	m.AddLabelFn = func(_ context.Context, _ string, _ string) error {
		stamped = true
		return nil
	}
	s := &Supervisor{IssueBackend: m}

	err := s.advanceReviewCycle(context.Background(), "T-1", testCycle(3))
	if err == nil {
		t.Fatal("a failed re-arm removal must fail the ship")
	}
	if !strings.Contains(err.Error(), "re-arm") {
		t.Errorf("error should name the re-arm: %v", err)
	}
	if stamped {
		t.Error("the ship label must not be stamped once the re-arm removal failed")
	}
}

// Threshold 1 is the degenerate single-pass flow: ship immediately, no counter.
// It is also the case the re-ship spin hit hardest — every pass ships, and the
// re-arm label is always present because the stage that just ran matched it.
func TestAdvanceReviewCycle_ThresholdOneShipsImmediately(t *testing.T) {
	m, ops := cycleRecorder([]string{"plan", "criticized"})
	s := &Supervisor{IssueBackend: m}

	if err := s.advanceReviewCycle(context.Background(), "T-1", testCycle(1)); err != nil {
		t.Fatalf("advanceReviewCycle: %v", err)
	}

	if got := strings.Join(*ops, " "); got != "remove:criticized add:ready" {
		t.Errorf("ops = %q, want an immediate ship that clears the re-arm and writes no counter", got)
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

// The ship hand-off has the same claimability requirement as the re-arm: the
// worker picks the task up by label, and task_router scores anything that is not
// `open` as 0. Without this the loop bounds correctly and then stalls forever
// with the ship label stamped on a task nothing can claim.
func TestAdvanceReviewCycle_ShipLeavesTheTaskClaimable(t *testing.T) {
	for _, status := range []string{"review", "in_progress"} {
		t.Run(status, func(t *testing.T) {
			labels := []string{"criticized", "review-cycle=2"}
			var updated string
			m := clitest.NewMockIssueBackend()
			m.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
				return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Status: status, Labels: labels}}, nil
			}
			m.AddLabelFn = func(_ context.Context, _ string, l string) error {
				labels = append(labels, l)
				return nil
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
				t.Fatalf("status after ship = %q, want open so the next stage can claim", updated)
			}

			// Prove the end state is genuinely claimable rather than merely
			// "open": run the router the worker actually uses. `any` isolates
			// the status gate — the design/label filters are the caller's
			// business, not the cycle's.
			shipped := backend.IssueData{ID: "T-1", IssueType: "task", Status: updated, Labels: labels}
			match := cli.MatchTask(shipped, cli.RoleConstraints{TaskFilter: "any"})
			if match.Score == 0 {
				t.Fatalf("the shipped task is not claimable: %s", match.Reason)
			}
			if !containsLabel(labels, "ready") {
				t.Fatalf("labels = %v, want the ship label stamped", labels)
			}
		})
	}
}

// closed and blocked are decisions somebody made: closed is terminal, blocked is
// how a stage escalates to a human. The loop must not drag a task out of either.
//
// The backend here refuses label mutation on a terminal issue the way fleet-db
// does (ValidateModifiable → "issue is closed"), so the test fails unless the
// status is checked BEFORE any write. A permissive mock would let a
// check-after-write implementation pass while the real backend demoted the run.
func TestAdvanceReviewCycle_DoesNotOverrideADeliberateStop(t *testing.T) {
	for _, status := range []string{"closed", "blocked"} {
		t.Run(status, func(t *testing.T) {
			touched := false
			m := clitest.NewMockIssueBackend()
			m.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
				return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Status: status, Labels: []string{"criticized"}}}, nil
			}
			terminal := status == "closed"
			m.AddLabelFn = func(_ context.Context, _ string, _ string) error {
				touched = true
				if terminal {
					return backend.ErrConflict("AddLabel", "issue is closed")
				}
				return nil
			}
			m.RemoveLabelFn = func(_ context.Context, _ string, _ string) error {
				touched = true
				if terminal {
					return backend.ErrConflict("RemoveLabel", "issue is closed")
				}
				return nil
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
				t.Errorf("a %s task must not be written to by the loop", status)
			}
		})
	}
}

// Same guard, at the other branch: a task closed while the final round ran must
// not be written to by the ship path either.
func TestAdvanceReviewCycle_ShipDoesNotWriteToAClosedTask(t *testing.T) {
	touched := false
	m := clitest.NewMockIssueBackend()
	m.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
		return &backend.IssueDetailData{IssueData: backend.IssueData{
			ID: id, Status: "closed", Labels: []string{"criticized", "review-cycle=2"},
		}}, nil
	}
	m.AddLabelFn = func(_ context.Context, _ string, _ string) error {
		touched = true
		return backend.ErrConflict("AddLabel", "issue is closed")
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
		t.Error("the ship branch must not write to a closed task")
	}
}

func containsLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}
