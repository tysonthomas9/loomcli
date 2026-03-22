package cli

import (
	"fmt"
	"testing"
)

// seedAssignment directly populates the EpicAssigner's assignment map for test setup.
func seedAssignment(ea *EpicAssigner, worktree, epicID string) {
	ea.mu.Lock()
	ea.assignments[worktree] = epicID
	ea.mu.Unlock()
}

func TestHandleEpicTransition_NotAssignedToEpic(t *testing.T) {
	// When not assigned to any epic, handleEpicTransition is a no-op.
	ea := NewEpicAssigner()
	d := &Daemon{epicAssigner: ea}
	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon"},
		worktreePath: "/repo/worktrees/falcon",
	}

	err := d.handleEpicTransition(ap)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHandleEpicTransition_ConfigDriven_StillHasTasks(t *testing.T) {
	// When the configured epic still has ready tasks, no transition occurs.
	stubEpicHasReadyTasks(t, func(id string) (bool, error) {
		return id == "epic-1", nil
	})

	ea := NewEpicAssigner()
	d := &Daemon{epicAssigner: ea}
	ap := &AgentProcess{
		entry:          AgentEntry{Worktree: "falcon", Parent: "epic-1"},
		worktreePath:   "/repo/worktrees/falcon",
		assignedEpicID: "epic-1",
		restartCount:   2,
	}

	err := d.handleEpicTransition(ap)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify restart count was NOT reset (no transition)
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
	// When the configured epic is exhausted, agent idles (sets lastNoWork).
	// No automatic reassignment — parent is config-driven.
	stubEpicHasReadyTasks(t, func(id string) (bool, error) {
		return false, nil
	})

	ea := NewEpicAssigner()
	d := &Daemon{epicAssigner: ea}
	ap := &AgentProcess{
		entry:          AgentEntry{Worktree: "falcon", Parent: "epic-1"},
		worktreePath:   "/repo/worktrees/falcon",
		assignedEpicID: "epic-1",
		restartCount:   3,
	}

	err := d.handleEpicTransition(ap)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify lastNoWork is set so daemon applies NoWork backoff
	ap.mu.Lock()
	noWork := ap.lastNoWork
	eid := ap.assignedEpicID
	ap.mu.Unlock()
	if !noWork {
		t.Error("expected lastNoWork=true when epic is exhausted")
	}
	// Epic assignment stays — it's config-driven, not auto-cleared
	if eid != "epic-1" {
		t.Errorf("expected assignedEpicID still epic-1, got %q", eid)
	}
}

func TestHandleEpicTransition_ConfigDriven_BdReadyFails(t *testing.T) {
	// When bd ready fails, stay on current epic (graceful handling).
	stubEpicHasReadyTasks(t, func(id string) (bool, error) {
		return false, fmt.Errorf("bd command failed")
	})

	ea := NewEpicAssigner()
	d := &Daemon{epicAssigner: ea}
	ap := &AgentProcess{
		entry:          AgentEntry{Worktree: "falcon", Parent: "epic-1"},
		worktreePath:   "/repo/worktrees/falcon",
		assignedEpicID: "epic-1",
		restartCount:   1,
	}

	err := d.handleEpicTransition(ap)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify restart count was NOT reset
	ap.mu.Lock()
	rc := ap.restartCount
	ap.mu.Unlock()
	if rc != 1 {
		t.Errorf("expected restartCount unchanged at 1, got %d", rc)
	}
}

func TestHandleEpicTransition_NoParentConfig_ClearsStaleAssignment(t *testing.T) {
	// When entry.Parent is empty but assignedEpicID is set (unexpected state),
	// handleEpicTransition clears the stale assignment and logs a warning.
	ea := NewEpicAssigner()
	d := &Daemon{epicAssigner: ea}
	ap := &AgentProcess{
		entry:          AgentEntry{Worktree: "falcon"},
		worktreePath:   "/repo/worktrees/falcon",
		assignedEpicID: "epic-1",
	}

	err := d.handleEpicTransition(ap)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	ap.mu.Lock()
	eid := ap.assignedEpicID
	ap.mu.Unlock()
	if eid != "" {
		t.Errorf("expected assignedEpicID cleared, got %q", eid)
	}
}
