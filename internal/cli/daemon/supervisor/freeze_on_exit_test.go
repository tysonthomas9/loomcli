package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// D29 / P1.26: the daemon holds an agent's close of a task in review only when
// that agent's current run freezes the task's work as a revision on exit.
func TestFreezesTaskOnExitOnlyForTheAgentsFrozenTask(t *testing.T) {
	dir := t.TempDir()
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent", Role: "task"}, WorktreePath: dir,
		BeforeRef: "base", AgentSessionID: "session-1", AssignedTaskID: "task-1"}
	s := &Supervisor{WorkspaceID: "WS", Agents: []*AgentProcess{ap}}
	if !s.FreezesTaskOnExit("agent", "task-1") {
		t.Fatal("the agent's assigned task freezes on exit")
	}
	for _, tc := range []struct{ agent, task string }{{"agent", "task-2"}, {"other", "task-1"}, {"", "task-1"}, {"agent", ""}} {
		if s.FreezesTaskOnExit(tc.agent, tc.task) {
			t.Fatalf("FreezesTaskOnExit(%q, %q) = true, want false", tc.agent, tc.task)
		}
	}
	writeLockFile(t, dir, &cli.LockInfo{PID: os.Getpid(), AgentName: "agent", TaskID: "task-2"})
	if !s.FreezesTaskOnExit("agent", "task-2") || s.FreezesTaskOnExit("agent", "task-1") {
		t.Fatal("the claimed task (lock file) is the one that freezes")
	}
	ap.AgentSessionID = ""
	if s.FreezesTaskOnExit("agent", "task-2") {
		t.Fatal("a run with no agent session freezes nothing")
	}
	ap.AgentSessionID = "session-1"
	s.WorkspaceID = ""
	if s.FreezesTaskOnExit("agent", "task-2") {
		t.Fatal("without a workspace nothing is frozen")
	}
}

// P1.26 safety net: an agent that closed its task past the daemon (e.g.
// `loom data close` over HTTP) before its run froze the work gets the task
// put back in review with the code-review label once the attempt has code
// awaiting review. An empty attempt, or a task not closed, is left alone.
func TestReviewFrozenTaskPutsAClosedTaskWithCodeBackInReview(t *testing.T) {
	for _, tc := range []struct {
		name      string
		awaits    bool
		awaitsErr error
		status    string
		wantOps   []string
	}{
		{"closed with code", true, nil, "closed", []string{"reopen", "update:review+code-review"}},
		{"closed, no changes", false, nil, "closed", nil},
		{"already held in review", true, nil, "review", nil},
		{"still in progress", true, nil, "in_progress", nil},
		{"journal unreadable", true, errors.New("locked"), "closed", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prior := attemptAwaitsReview
			attemptAwaitsReview = func(_ context.Context, ws, attempt string) (bool, error) {
				if ws != "WS" || attempt != "session-1" {
					t.Errorf("asked about %s/%s", ws, attempt)
				}
				return tc.awaits, tc.awaitsErr
			}
			t.Cleanup(func() { attemptAwaitsReview = prior })
			var ops []string
			m := clitest.NewMockIssueBackend()
			m.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
				return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Status: tc.status}}, nil
			}
			m.ReopenFn = func(context.Context, string, backend.ReopenParams) error {
				ops = append(ops, "reopen")
				return nil
			}
			m.UpdateFn = func(_ context.Context, _ string, p backend.UpdateParams) error {
				op := "update:"
				if p.Status != nil {
					op += *p.Status
				}
				if backend.HasCodeReviewLabel(p.AddLabels) {
					op += "+code-review"
				}
				ops = append(ops, op)
				return nil
			}
			s := &Supervisor{WorkspaceID: "WS", IssueBackend: m}
			s.reviewFrozenTask("T-1", "session-1")
			if fmt.Sprint(ops) != fmt.Sprint(tc.wantOps) {
				t.Fatalf("ops = %v, want %v", ops, tc.wantOps)
			}
		})
	}
}
