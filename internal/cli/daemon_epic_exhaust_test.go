package cli

import (
	"fmt"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
)

func TestHandleEpicTransition_NotAssignedToEpic(t *testing.T) {
	d := &Daemon{}
	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon"},
		worktreePath: "/repo/worktrees/falcon",
	}

	d.handleEpicTransition(ap)
}

func TestHandleEpicTransition_ConfigDriven_StillHasTasks(t *testing.T) {
	stubEpicHasReadyTasks(t, func(id string) (bool, error) {
		return id == "epic-1", nil
	})

	d := &Daemon{}
	ap := &AgentProcess{
		entry:          AgentEntry{Worktree: "falcon", Parent: "epic-1"},
		worktreePath:   "/repo/worktrees/falcon",
		assignedEpicID: "epic-1",
		restartCount:   2,
	}

	d.handleEpicTransition(ap)

	ap.mu.Lock()
	rc := ap.restartCount
	noWork := ap.lastNoWork
	ap.mu.Unlock()
	if rc != 2 {
		t.Errorf("expected restartCount unchanged at 2, got %d", rc)
	}
	if noWork {
		t.Error("expected lastNoWork=false when epic still has tasks")
	}
}

func TestHandleEpicTransition_ConfigDriven_Exhausted(t *testing.T) {
	stubEpicHasReadyTasks(t, func(id string) (bool, error) {
		return false, nil
	})

	d := &Daemon{config: &DaemonConfig{}}
	ap := &AgentProcess{
		entry:          AgentEntry{Worktree: "falcon", Parent: "epic-1"},
		worktreePath:   "/repo/worktrees/falcon",
		assignedEpicID: "epic-1",
		restartCount:   3,
	}

	d.handleEpicTransition(ap)

	ap.mu.Lock()
	noWork := ap.lastNoWork
	eid := ap.assignedEpicID
	lastErr := ap.lastError
	ap.mu.Unlock()

	if !noWork {
		t.Error("expected lastNoWork=true when epic is exhausted")
	}
	if eid != "epic-1" {
		t.Errorf("expected assignedEpicID still epic-1, got %q", eid)
	}
	if lastErr == nil {
		t.Fatal("expected lastError to be set on epic exhaustion")
	}
	if lastErr.Class != agenterr.NoWork {
		t.Errorf("expected lastError.Class=NoWork, got %v", lastErr.Class)
	}
}

func TestHandleEpicTransition_ConfigDriven_Exhausted_DoesNotMaskCrashError(t *testing.T) {
	// When the agent crashed (lastError is a real error) and the epic is also
	// exhausted, the crash error should NOT be overwritten with NoWork.
	stubEpicHasReadyTasks(t, func(id string) (bool, error) {
		return false, nil
	})

	crashErr := &agenterr.AgentError{
		Class:   agenterr.Unknown,
		Message: "segfault",
		Backend: "claude",
	}

	d := &Daemon{config: &DaemonConfig{}}
	ap := &AgentProcess{
		entry:          AgentEntry{Worktree: "falcon", Parent: "epic-1"},
		worktreePath:   "/repo/worktrees/falcon",
		assignedEpicID: "epic-1",
		lastError:      crashErr,
	}

	d.handleEpicTransition(ap)

	ap.mu.Lock()
	lastErr := ap.lastError
	noWork := ap.lastNoWork
	ap.mu.Unlock()

	// lastNoWork should be set (epic IS exhausted)
	if !noWork {
		t.Error("expected lastNoWork=true")
	}
	// But lastError should still be the crash error, not overwritten
	if lastErr != crashErr {
		t.Errorf("expected lastError to remain the crash error, got %v", lastErr)
	}
}

func TestHandleEpicTransition_ConfigDriven_BdReadyFails(t *testing.T) {
	stubEpicHasReadyTasks(t, func(id string) (bool, error) {
		return false, fmt.Errorf("bd command failed")
	})

	d := &Daemon{}
	ap := &AgentProcess{
		entry:          AgentEntry{Worktree: "falcon", Parent: "epic-1"},
		worktreePath:   "/repo/worktrees/falcon",
		assignedEpicID: "epic-1",
		restartCount:   1,
	}

	d.handleEpicTransition(ap)

	ap.mu.Lock()
	rc := ap.restartCount
	ap.mu.Unlock()
	if rc != 1 {
		t.Errorf("expected restartCount unchanged at 1, got %d", rc)
	}
}

func TestHandleEpicTransition_NoParentConfig_ClearsStaleAssignment(t *testing.T) {
	d := &Daemon{}
	ap := &AgentProcess{
		entry:          AgentEntry{Worktree: "falcon"},
		worktreePath:   "/repo/worktrees/falcon",
		assignedEpicID: "epic-1",
	}

	d.handleEpicTransition(ap)

	ap.mu.Lock()
	eid := ap.assignedEpicID
	ap.mu.Unlock()
	if eid != "" {
		t.Errorf("expected assignedEpicID cleared, got %q", eid)
	}
}
