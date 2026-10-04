package supervisor

import (
	"os"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
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
