package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
)

// deadPID is a PID that is not running, so CheckLock reports (info, false,
// nil) — the post-exit shape RecoverWorktree sees.
const deadPID = 999999999

// writeAgentLock writes a worktree lock owned by pid. The resume paths run
// in-process and mutate their OWN lock (ClearLockClaudeSessionID enforces
// ownership), so those tests pass os.Getpid(); the recovery paths run after the
// agent is gone and pass deadPID.
func writeAgentLock(t *testing.T, dir string, pid int, agentName, taskID, sessionID string) {
	t.Helper()
	info := cli.LockInfo{
		PID:             pid,
		Command:         "task",
		AgentName:       agentName,
		TaskID:          taskID,
		ClaudeSessionID: sessionID,
	}
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		t.Fatalf("marshal lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, cli.LockFileName), b, 0600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
}

// claimStateBackend returns a mock reporting the given claim state, plus an
// empty orphan list so resetOrphanedAgentTasks stays quiet.
func claimStateBackend(status, assignee string) *MockIssueBackend {
	m := NewMockIssueBackend()
	m.GetResult = &backend.IssueDetailData{
		IssueData: backend.IssueData{ID: "task-123", Status: status, Assignee: assignee},
	}
	m.ListResult = []backend.IssueData{}
	return m
}

func updateStatusFor(t *testing.T, m *MockIssueBackend, id string) *string {
	t.Helper()
	for _, c := range m.Calls {
		if c.Method != "Update" || len(c.Args) < 2 {
			continue
		}
		if got, _ := c.Args[0].(string); got != id {
			continue
		}
		params, ok := c.Args[1].(backend.UpdateParams)
		if !ok {
			t.Fatalf("Update args = %+v, want UpdateParams at [1]", c.Args)
		}
		return params.Status
	}
	return nil
}

// ---------------------------------------------------------------------------
// RecoverWorktree: the incomplete-run path
// ---------------------------------------------------------------------------

func TestRecoverWorktree_IncompleteRun_RequeuesTaskAndKeepsUntrackedFiles(t *testing.T) {
	tmpDir := t.TempDir()
	writeAgentLock(t, tmpDir, deadPID, "test-agent", "task-123", "claude-abc")

	// An empty stub list makes CommandMock fail the test on ANY command, so a
	// `git clean` reaching the worktree is caught rather than tolerated. Its
	// Verify() cleanup also asserts nothing was expected-but-missed.
	mock := NewCommandMock(t, nil)
	mock.Install()

	tracker := claimStateBackend("in_progress", "test-agent")
	setDefaultIssueBackend(tracker)
	t.Cleanup(resetDefaultIssueBackend)

	if err := RecoverWorktree(tmpDir, "test-agent", "", 0, true); err != nil {
		t.Fatalf("RecoverWorktree: %v", err)
	}

	status := updateStatusFor(t, tracker, "task-123")
	if status == nil {
		t.Fatal("no Update recorded; an unfinished task must go back on the queue for another agent")
	}
	if *status != "open" {
		t.Errorf("reset status = %q, want open", *status)
	}
}

// The mirror: a genuinely complete run still trusts the agent's status and
// still cleans, so the divergence is confined to the incomplete case.
func TestRecoverWorktree_CompleteRun_TrustsStatusAndStillCleans(t *testing.T) {
	tmpDir := t.TempDir()
	writeAgentLock(t, tmpDir, deadPID, "test-agent", "task-123", "claude-abc")

	mock := NewCommandMock(t, []CommandStub{{
		Dir:  tmpDir,
		Name: "git",
		Args: []string{"clean", "-fdn", "--exclude=.loom", "--exclude=sessions", "--exclude=AGENTS.md"},
	}})
	mock.Install()

	tracker := claimStateBackend("review", "test-agent")
	setDefaultIssueBackend(tracker)
	t.Cleanup(resetDefaultIssueBackend)

	if err := RecoverWorktree(tmpDir, "test-agent", "", 0, false); err != nil {
		t.Fatalf("RecoverWorktree: %v", err)
	}

	if status := updateStatusFor(t, tracker, "task-123"); status != nil {
		t.Errorf("task was reset to %q; a clean exit must not stomp the status the agent set", *status)
	}
}

// ---------------------------------------------------------------------------
// the resume session id survives
// ---------------------------------------------------------------------------

func lockSessionID(t *testing.T, dir string) string {
	t.Helper()
	info, err := cli.ReadLockFile(dir)
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	if info == nil {
		t.Fatal("lock file missing")
	}
	return info.ClaudeSessionID
}

func TestClearDaemonResumeOnSuccess_ClaimStillHeld_KeepsSessionID(t *testing.T) {
	dir := t.TempDir()
	writeAgentLock(t, dir, os.Getpid(), "falcon", "task-123", "claude-abc")

	cli.SetDefaultIssueBackend(claimStateBackend("in_progress", "falcon"))
	t.Cleanup(cli.ResetDefaultIssueBackend)

	clearDaemonResumeOnSuccess(dir)

	if got := lockSessionID(t, dir); got != "claude-abc" {
		t.Errorf("ClaudeSessionID = %q, want claude-abc: an unfinished run must stay resumable", got)
	}
}

func TestClearDaemonResumeOnSuccess_ClaimReleased_ClearsSessionID(t *testing.T) {
	dir := t.TempDir()
	writeAgentLock(t, dir, os.Getpid(), "falcon", "task-123", "claude-abc")

	cli.SetDefaultIssueBackend(claimStateBackend("closed", "falcon"))
	t.Cleanup(cli.ResetDefaultIssueBackend)

	clearDaemonResumeOnSuccess(dir)

	if got := lockSessionID(t, dir); got != "" {
		t.Errorf("ClaudeSessionID = %q, want empty: a completed run starts the next task fresh", got)
	}
}

// ---------------------------------------------------------------------------
// ClaimStillHeld: the discriminator itself
// ---------------------------------------------------------------------------

func TestClaimStillHeld_PositiveEvidenceOnly(t *testing.T) {
	cases := []struct {
		name     string
		status   string
		assignee string
		want     bool
	}{
		{"held by us", "in_progress", "falcon", true},
		{"released to open", "open", "", false},
		{"moved to review", "review", "falcon", false},
		{"held by a sibling", "in_progress", "ember", false},
		{"unowned", "in_progress", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClaimStillHeld(t.Context(), claimStateBackend(tc.status, tc.assignee), "task-123", "falcon")
			if got != tc.want {
				t.Errorf("ClaimStillHeld = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClaimStillHeld_MissingInputsAreNotEvidence(t *testing.T) {
	held := claimStateBackend("in_progress", "falcon")
	if ClaimStillHeld(t.Context(), nil, "task-123", "falcon") {
		t.Error("nil backend reported a held claim")
	}
	if ClaimStillHeld(t.Context(), held, "", "falcon") {
		t.Error("empty task id reported a held claim")
	}
	if ClaimStillHeld(t.Context(), held, "task-123", "") {
		t.Error("empty actor reported a held claim")
	}
}
