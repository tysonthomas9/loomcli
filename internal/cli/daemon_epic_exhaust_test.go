package cli

import (
	"errors"
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

func TestHandleEpicTransition_EpicStillHasTasks(t *testing.T) {
	// When the current epic still has ready tasks, no transition occurs.
	stubEpicHasReadyTasks(t, func(id string) (bool, error) {
		return id == "epic-1", nil
	})

	ea := NewEpicAssigner()
	seedAssignment(ea, "falcon", "epic-1")

	d := &Daemon{epicAssigner: ea}
	ap := &AgentProcess{
		entry:          AgentEntry{Worktree: "falcon"},
		worktreePath:   "/repo/worktrees/falcon",
		assignedEpicID: "epic-1",
		restartCount:   2,
	}

	err := d.handleEpicTransition(ap)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify still assigned to same epic
	if got := ea.GetAssignment("falcon"); got != "epic-1" {
		t.Errorf("expected still assigned to epic-1, got %q", got)
	}

	// Verify restart count was NOT reset (no transition occurred)
	ap.mu.Lock()
	rc := ap.restartCount
	ap.mu.Unlock()
	if rc != 2 {
		t.Errorf("expected restartCount unchanged at 2, got %d", rc)
	}
}

func TestHandleEpicTransition_ExhaustedWithNewEpic(t *testing.T) {
	// When epic-1 is exhausted but epic-2 is available, transitions to epic-2.
	stubEpicHasReadyTasks(t, func(id string) (bool, error) {
		// epic-1 has no tasks, epic-2 has tasks
		return id == "epic-2", nil
	})
	stubQueryOpenEpics(t, func() ([]EpicInfo, error) {
		return []EpicInfo{
			{ID: "epic-1", Priority: 1},
			{ID: "epic-2", Priority: 2},
		}, nil
	})

	ea := NewEpicAssigner()
	seedAssignment(ea, "falcon", "epic-1")

	// Mock branch switching for the new epic
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "epic/epic-1\n"},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"rev-parse", "--verify", "refs/heads/epic/epic-2"}, Err: errors.New("not found")},
		{Name: "git", Args: []string{"rev-parse", "--verify", "refs/remotes/origin/epic/epic-2"}, Err: errors.New("not found")},
	})
	cmdMock.Install()

	outMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"checkout", "-b", "epic/epic-2", "origin/main"}, Err: nil},
	})
	outMock.Install()

	d := &Daemon{epicAssigner: ea}
	ap := &AgentProcess{
		entry:          AgentEntry{Worktree: "falcon"},
		worktreePath:   "/repo/worktrees/falcon",
		assignedEpicID: "epic-1",
		restartCount:   3,
	}

	err := d.handleEpicTransition(ap)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify assigned to new epic
	if got := ea.GetAssignment("falcon"); got != "epic-2" {
		t.Errorf("expected assigned to epic-2, got %q", got)
	}

	// Verify assignedEpicID was updated
	ap.mu.Lock()
	eid := ap.assignedEpicID
	rc := ap.restartCount
	ap.mu.Unlock()
	if eid != "epic-2" {
		t.Errorf("expected assignedEpicID epic-2, got %q", eid)
	}

	// Verify restart counter was reset
	if rc != 0 {
		t.Errorf("expected restartCount reset to 0, got %d", rc)
	}
}

func TestHandleEpicTransition_ExhaustedNoMoreEpics(t *testing.T) {
	// When epic-1 is exhausted and no other epics are available, falls back to non-epic mode.
	stubEpicHasReadyTasks(t, func(id string) (bool, error) {
		return false, nil // no ready tasks in any epic
	})
	stubQueryOpenEpics(t, func() ([]EpicInfo, error) {
		return []EpicInfo{{ID: "epic-1", Priority: 1}}, nil
	})

	ea := NewEpicAssigner()
	seedAssignment(ea, "falcon", "epic-1")

	// Mock branch switching back to agent-name branch
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "epic/epic-1\n"},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"rev-parse", "--verify", "refs/heads/falcon"}, Stdout: "abc123\n"},
	})
	cmdMock.Install()

	outMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"checkout", "falcon"}, Err: nil},
	})
	outMock.Install()

	d := &Daemon{epicAssigner: ea}
	ap := &AgentProcess{
		entry:          AgentEntry{Worktree: "falcon"},
		worktreePath:   "/repo/worktrees/falcon",
		assignedEpicID: "epic-1",
		restartCount:   2,
	}

	err := d.handleEpicTransition(ap)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify released from epic
	if got := ea.GetAssignment("falcon"); got != "" {
		t.Errorf("expected no assignment, got %q", got)
	}

	// Verify assignedEpicID was cleared
	ap.mu.Lock()
	eid := ap.assignedEpicID
	rc := ap.restartCount
	ap.mu.Unlock()
	if eid != "" {
		t.Errorf("expected assignedEpicID empty, got %q", eid)
	}

	// Verify restart counter was reset
	if rc != 0 {
		t.Errorf("expected restartCount reset to 0, got %d", rc)
	}
}

func TestHandleEpicTransition_BdReadyFails(t *testing.T) {
	// When bd ready fails, stay on current epic (graceful handling).
	stubEpicHasReadyTasks(t, func(id string) (bool, error) {
		return false, fmt.Errorf("bd command failed")
	})

	ea := NewEpicAssigner()
	seedAssignment(ea, "falcon", "epic-1")

	d := &Daemon{epicAssigner: ea}
	ap := &AgentProcess{
		entry:          AgentEntry{Worktree: "falcon"},
		worktreePath:   "/repo/worktrees/falcon",
		assignedEpicID: "epic-1",
		restartCount:   1,
	}

	err := d.handleEpicTransition(ap)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify still assigned (stayed on current epic)
	if got := ea.GetAssignment("falcon"); got != "epic-1" {
		t.Errorf("expected still assigned to epic-1, got %q", got)
	}

	// Verify restart count was NOT reset
	ap.mu.Lock()
	rc := ap.restartCount
	ap.mu.Unlock()
	if rc != 1 {
		t.Errorf("expected restartCount unchanged at 1, got %d", rc)
	}
}

func TestSwitchToNonEpicMode(t *testing.T) {
	// switchToNonEpicMode clears epic assignment and switches to agent-name branch.
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "epic/epic-1\n"},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"rev-parse", "--verify", "refs/heads/falcon"}, Stdout: "abc123\n"},
	})
	cmdMock.Install()

	outMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"checkout", "falcon"}, Err: nil},
	})
	outMock.Install()

	ea := NewEpicAssigner()
	d := &Daemon{epicAssigner: ea}
	ap := &AgentProcess{
		entry:          AgentEntry{Worktree: "falcon"},
		worktreePath:   "/repo/worktrees/falcon",
		assignedEpicID: "epic-1",
		restartCount:   5,
	}

	err := d.switchToNonEpicMode(ap)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	ap.mu.Lock()
	eid := ap.assignedEpicID
	rc := ap.restartCount
	ap.mu.Unlock()

	if eid != "" {
		t.Errorf("expected assignedEpicID empty, got %q", eid)
	}
	if rc != 0 {
		t.Errorf("expected restartCount reset to 0, got %d", rc)
	}
}
