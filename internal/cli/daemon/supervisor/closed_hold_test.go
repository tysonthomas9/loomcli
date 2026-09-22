package supervisor

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// A closed task is terminal: someone or something closed it, possibly while
// this run was in flight. The tests below pin that a completion hook never
// writes a status over it — which is how a closed ticket gets reopened and
// re-dispatched — and that respecting that never demotes the run.

func closedGet(status string) func(context.Context, string) (*backend.IssueDetailData, error) {
	return func(_ context.Context, id string) (*backend.IssueDetailData, error) {
		return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Status: status}}, nil
	}
}

func TestRunCompletionHooks_SetStatusLeavesAClosedTaskClosed(t *testing.T) {
	for _, taskStatus := range []string{"closed", "tombstone"} {
		for _, action := range []domain.AgentHookAction{
			{Type: domain.AgentHookActionSetStatus, Value: "open"},
			{Type: domain.AgentHookActionSetStatus, Value: "review"},
			{Type: domain.AgentHookActionSetStatus, Value: "deferred"},
			{Type: domain.AgentHookActionSetStatus, Value: "blocked", Reason: "needs a human"},
		} {
			t.Run(taskStatus+"/"+action.Value, func(t *testing.T) {
				hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
					{Type: domain.AgentHookActionAddLabel, Value: "in-review"},
					action,
				}}
				ap := newHookAgentProcess(t, "T-8", hooks)
				ap.AgentSessionID = "sess-closed"
				r := newFieldOpRecorder(nil)
				r.GetFn = closedGet(taskStatus)
				s := &Supervisor{IssueBackend: r}

				if got := s.runCompletionHooks(ap, 0); got != 0 {
					t.Fatalf("exit code = %d, want the clean exit preserved", got)
				}
				if ap.LastError != nil {
					t.Fatalf("LastError = %+v, want nil: leaving a closed task closed is not a hook failure", ap.LastError)
				}
				if got := r.seq(); !equalStrings(got, []string{"add:in-review"}) {
					t.Fatalf("write sequence = %v, want no status write on a %s task", got, taskStatus)
				}
			})
		}
	}
}

// The control: an ordinary open task still gets its status write, so the guard
// above cannot silently disable the hand-off it exists to protect.
func TestRunCompletionHooks_SetStatusStillWritesAnOpenTask(t *testing.T) {
	hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionAddLabel, Value: "in-review"},
		{Type: domain.AgentHookActionSetStatus, Value: "open"},
	}}
	ap := newHookAgentProcess(t, "T-9", hooks)
	ap.AgentSessionID = "sess-open"
	r := newFieldOpRecorder(nil)
	r.GetFn = closedGet("in_progress")
	s := &Supervisor{IssueBackend: r}

	if got := s.runCompletionHooks(ap, 0); got != 0 {
		t.Fatalf("exit code = %d, want 0", got)
	}
	if got := r.seq(); !equalStrings(got, []string{"add:in-review", "status:open"}) {
		t.Fatalf("write sequence = %v, want the label and the status write", got)
	}
}
