package supervisor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// `deferred` is the human hold: a human can move a task there while its run is
// in flight, and only an explicit deferred -> open releases it. The tests below
// pin that no automated supervisor path releases it, and that honoring the hold
// never demotes the run that happened to be holding the task.

func deferredGet(id string) (*backend.IssueDetailData, error) {
	return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Status: "deferred"}}, nil
}

// A set_status hook finds the task deferred: no status write, and the run keeps
// its clean exit. Failing instead would demote the run, burn the agent's block
// budget, and hand the task to crash recovery for yet another status write.
func TestRunCompletionHooks_SetStatusLeavesADeferredTaskDeferred(t *testing.T) {
	for _, action := range []domain.AgentHookAction{
		{Type: domain.AgentHookActionSetStatus, Value: "open"},
		{Type: domain.AgentHookActionSetStatus, Value: "review"},
		{Type: domain.AgentHookActionSetStatus, Value: "blocked", Reason: "needs a human"},
	} {
		t.Run(action.Value, func(t *testing.T) {
			hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
				{Type: domain.AgentHookActionAddLabel, Value: "planned"},
				action,
			}}
			ap := newHookAgentProcess(t, "T-7", hooks)
			ap.AgentSessionID = "sess-1"
			r := newFieldOpRecorder(nil)
			r.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) { return deferredGet(id) }
			s := &Supervisor{IssueBackend: r}

			if got := s.runCompletionHooks(ap, 0); got != 0 {
				t.Fatalf("exit code = %d, want the clean exit preserved", got)
			}
			if ap.LastError != nil {
				t.Fatalf("LastError = %+v, want nil: honoring the hold is not a hook failure", ap.LastError)
			}
			if got := r.seq(); !equalStrings(got, []string{"add:planned"}) {
				t.Fatalf("write sequence = %v, want the label only and no status write on a deferred task", got)
			}
		})
	}
}

// The hold check is only as good as the status read. When the read fails the
// write goes ahead exactly as it did before the check existed, so a flaky read
// cannot turn into a new way to demote runs.
func TestRunCompletionHooks_SetStatusWritesWhenTheStatusReadFails(t *testing.T) {
	hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionSetStatus, Value: "open"},
	}}
	ap := newHookAgentProcess(t, "T-8", hooks)
	ap.AgentSessionID = "sess-1"
	r := newFieldOpRecorder(nil)
	r.GetErr = errors.New("read boom")
	s := &Supervisor{IssueBackend: r}

	if got := s.runCompletionHooks(ap, 0); got != 0 {
		t.Fatalf("exit code = %d, want 0", got)
	}
	if got := r.seq(); !equalStrings(got, []string{"status:open"}) {
		t.Fatalf("write sequence = %v, want the status write to proceed", got)
	}
}

// The critic's cycle hook through the production entry point: a deferred task
// gets no label write and no reopen, and the run stays clean.
func TestRunCompletionHooks_CycleLeavesADeferredTaskDeferred(t *testing.T) {
	hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionCycle, Cycle: testCycle(3)},
	}}
	ap := newHookAgentProcess(t, "T-9", hooks)
	ap.AgentSessionID = "sess-1"
	r := newFieldOpRecorder(nil)
	r.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
		d, _ := deferredGet(id)
		d.Labels = []string{"criticized"}
		return d, nil
	}
	s := &Supervisor{IssueBackend: r}

	if got := s.runCompletionHooks(ap, 0); got != 0 {
		t.Fatalf("exit code = %d, want the clean exit preserved", got)
	}
	if ap.LastError != nil {
		t.Fatalf("LastError = %+v, want nil", ap.LastError)
	}
	if got := r.seq(); len(got) != 0 {
		t.Fatalf("writes = %v, want none on a deferred task", got)
	}
}

func resumeAgent() *AgentProcess {
	return &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig:   cfgpkg.RoleConfig{TaskFilter: "has_design"},
		ResumeTaskID: "T-held",
	}
}

// The resume path claims by id and bypasses the ready queue, and fleet-db still
// lets a claim take a task deferred -> in_progress. A task a human deferred
// mid-run must not be resumed: not by a fresh claim, and not through the
// "our own lock is still live" conflict branch either. The agent cold-starts
// onto ready work instead.
func TestClaimTask_ResumeRefusesADeferredTask(t *testing.T) {
	for _, tc := range []struct {
		name     string
		claimErr func(id string) error
	}{
		{name: "claim would succeed", claimErr: func(string) error { return nil }},
		{name: "own lock still live", claimErr: func(string) error {
			e := backend.ErrConflict("ClaimIssue", "claimed")
			e.Meta = map[string]string{"existing_owner": "falcon"}
			return e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := clitest.NewMockIssueBackend()
			mock.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
				if id == "T-held" {
					return deferredGet(id)
				}
				return nil, errors.New("unexpected Get " + id)
			}
			mock.ReadyResult = []backend.IssueData{
				{ID: "T-ready", IssueType: "task", Status: "open", Priority: 1, Title: "Ready", Design: "plan"},
			}
			var claimed []string
			mock.ClaimIssueFn = func(_ context.Context, id string, _ time.Duration) error {
				claimed = append(claimed, id)
				if id == "T-held" {
					return tc.claimErr(id)
				}
				return nil
			}
			s := &Supervisor{IssueBackend: mock}
			ap := resumeAgent()

			if !s.claimTask(ap, "") {
				t.Fatalf("claimTask returned false, want a cold-start onto ready work (LastError %+v)", ap.LastError)
			}
			if !equalStrings(claimed, []string{"T-ready"}) {
				t.Fatalf("claims = %v, want only the ready task: a deferred task is not ours to resume", claimed)
			}
			if ap.AssignedTaskID != "T-ready" {
				t.Fatalf("AssignedTaskID = %q, want T-ready", ap.AssignedTaskID)
			}
			if ap.ResumeTaskID != "" {
				t.Fatalf("ResumeTaskID = %q, want cleared after refusing the resume", ap.ResumeTaskID)
			}
		})
	}
}

// Control: an interrupted task that is still in_progress resumes as before.
func TestClaimTask_ResumeReclaimsAnInProgressTask(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
		return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Status: "in_progress"}}, nil
	}
	var claimed []string
	mock.ClaimIssueFn = func(_ context.Context, id string, _ time.Duration) error {
		claimed = append(claimed, id)
		return nil
	}
	s := &Supervisor{IssueBackend: mock}
	ap := resumeAgent()

	if !s.claimTask(ap, "") {
		t.Fatalf("claimTask returned false (LastError %+v)", ap.LastError)
	}
	if !equalStrings(claimed, []string{"T-held"}) {
		t.Fatalf("claims = %v, want the interrupted task re-claimed", claimed)
	}
	if ap.AssignedTaskID != "T-held" {
		t.Fatalf("AssignedTaskID = %q, want T-held", ap.AssignedTaskID)
	}
}
