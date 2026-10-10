package daemon

import (
	"encoding/json"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

func withLabels(status string, labels ...string) *backend.IssueDetailData {
	return &backend.IssueDetailData{IssueData: backend.IssueData{ID: "T-1", Status: status, Labels: labels}}
}

func ipcArgs(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// withFreezingAgent registers falcon working on T-1 with a run that freezes
// its work on exit (P1.21): a workspace, a base ref and an agent session.
func withFreezingAgent(t *testing.T, d *Daemon, freezes bool) {
	t.Helper()
	ap := &supervisor.AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon"}, WorktreePath: t.TempDir(),
		AssignedTaskID: "T-1", AgentSessionID: "session-1"}
	if freezes {
		ap.BeforeRef = "0123456789012345678901234567890123456789"
	}
	d.sup.WorkspaceID = "W"
	d.sup.Agents = append(d.sup.Agents, ap)
}

// D29 / P1.26: a daemon-managed agent that closes its task with code to
// freeze leaves the task open, in review, with the code-review label. Loom's
// review settle closes it later if the frozen attempt is empty.
func TestIPCCompleteKeepsTaskWithFrozenWorkInReview(t *testing.T) {
	mb := &mockIPCBackend{issue: withLabels("in_progress")}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)
	withFreezingAgent(t, d, true)

	resp := d.handleIPCComplete(AgentIPCRequest{Operation: ipcOpComplete, AgentName: "falcon", IssueID: "T-1",
		Args: ipcArgs(t, backend.CloseParams{Reason: "done"})})
	if !resp.Success {
		t.Fatalf("complete: %s", resp.Error)
	}
	if len(mb.closeCalls) != 0 {
		t.Fatalf("close calls = %+v, want the task kept open", mb.closeCalls)
	}
	if len(mb.updateCalls) != 1 || *mb.updateCalls[0].Params.Status != "review" ||
		!backend.HasCodeReviewLabel(mb.updateCalls[0].Params.AddLabels) {
		t.Fatalf("updates = %+v, want status review with the code-review label", mb.updateCalls)
	}
	var result backend.CloseResult
	if err := json.Unmarshal(resp.Data, &result); err != nil || result.Closed == nil || result.Closed.ID != "T-1" {
		t.Fatalf("close result = %s %v, want the task", resp.Data, err)
	}
}

// A run that freezes nothing (no Loom Git copy), or an agent closing a task
// that is not its run's task, closes as before.
func TestIPCCompleteClosesWhenNothingIsFrozen(t *testing.T) {
	for _, tc := range []struct {
		name    string
		freezes bool
		issue   string
	}{{"no freeze", false, "T-1"}, {"another task", true, "T-2"}} {
		t.Run(tc.name, func(t *testing.T) {
			mb := &mockIPCBackend{issue: withLabels("in_progress")}
			d := newTestIPCDaemon(mb)
			defer close(d.sup.Shutdown)
			withFreezingAgent(t, d, tc.freezes)
			resp := d.handleIPCComplete(AgentIPCRequest{Operation: ipcOpComplete, AgentName: "falcon", IssueID: tc.issue})
			if !resp.Success || len(mb.closeCalls) != 1 || len(mb.updateCalls) != 0 {
				t.Fatalf("resp %+v closes %+v updates %+v, want a plain close", resp, mb.closeCalls, mb.updateCalls)
			}
		})
	}
}

// An agent may not skip code review: it can't close, claim or change the
// status of a task in code review, and can't add, remove or drop the
// code-review label on any task.
func TestIPCRefusesAgentsSkippingCodeReview(t *testing.T) {
	inReview := withLabels("review", "keep", backend.CodeReviewLabel)
	closed, open := "closed", "open"
	for _, tc := range []struct {
		name  string
		issue *backend.IssueDetailData
		req   AgentIPCRequest
	}{
		{"close", inReview, AgentIPCRequest{Operation: ipcOpComplete}},
		{"claim", inReview, AgentIPCRequest{Operation: ipcOpClaim}},
		{"status", inReview, AgentIPCRequest{Operation: ipcOpUpdate, Args: ipcArgs(t, backend.UpdateParams{Status: &closed})}},
		{"reopen", inReview, AgentIPCRequest{Operation: ipcOpUpdate, Args: ipcArgs(t, backend.UpdateParams{Status: &open})}},
		{"claim update", inReview, AgentIPCRequest{Operation: ipcOpUpdate, Args: ipcArgs(t, backend.UpdateParams{Claim: true})}},
		{"remove label", inReview, AgentIPCRequest{Operation: ipcOpUpdate, Args: ipcArgs(t, backend.UpdateParams{RemoveLabels: []string{backend.CodeReviewLabel}})}},
		{"replace labels", inReview, AgentIPCRequest{Operation: ipcOpUpdate, Args: ipcArgs(t, backend.UpdateParams{SetLabels: []string{"keep"}})}},
		{"add label", withLabels("in_progress"), AgentIPCRequest{Operation: ipcOpUpdate, Args: ipcArgs(t, backend.UpdateParams{AddLabels: []string{backend.CodeReviewLabel}})}},
		{"set label", withLabels("in_progress"), AgentIPCRequest{Operation: ipcOpUpdate, Args: ipcArgs(t, backend.UpdateParams{SetLabels: []string{backend.CodeReviewLabel}})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mb := &mockIPCBackend{issue: tc.issue}
			d := newTestIPCDaemon(mb)
			defer close(d.sup.Shutdown)
			tc.req.AgentName, tc.req.IssueID = "falcon", "T-1"
			resp := d.dispatchIPCOperation(tc.req)
			if resp.Success || resp.Kind != string(backend.KindConflict) {
				t.Fatalf("resp = %+v, want a conflict refusal", resp)
			}
			if len(mb.closeCalls)+len(mb.updateCalls)+len(mb.claimCalls) != 0 {
				t.Fatalf("backend reached: closes %+v updates %+v claims %+v", mb.closeCalls, mb.updateCalls, mb.claimCalls)
			}
		})
	}
}

// Other agent updates stay allowed: notes on a task in code review, and a
// planner's own status change on a task without the label (plan review is
// unchanged).
func TestIPCAllowsUpdatesThatKeepCodeReview(t *testing.T) {
	notes, review := "progress", "review"
	for _, tc := range []struct {
		name   string
		issue  *backend.IssueDetailData
		params backend.UpdateParams
	}{
		{"notes in code review", withLabels("review", backend.CodeReviewLabel), backend.UpdateParams{Notes: &notes}},
		{"labels kept", withLabels("review", backend.CodeReviewLabel), backend.UpdateParams{SetLabels: []string{"x", backend.CodeReviewLabel}}},
		{"planner to review", withLabels("in_progress"), backend.UpdateParams{Status: &review}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mb := &mockIPCBackend{issue: tc.issue}
			d := newTestIPCDaemon(mb)
			defer close(d.sup.Shutdown)
			resp := d.handleIPCUpdate(AgentIPCRequest{Operation: ipcOpUpdate, AgentName: "falcon", IssueID: "T-1", Args: ipcArgs(t, tc.params)})
			if !resp.Success || len(mb.updateCalls) != 1 {
				t.Fatalf("resp %+v updates %+v, want the update applied", resp, mb.updateCalls)
			}
		})
	}
}
