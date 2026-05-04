package supervisor

import (
	"fmt"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

func newEpicTestSupervisor(hasReady func(epicID string) (bool, error)) *Supervisor {
	return &Supervisor{
		ConfigSnapshot:    func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{} },
		Shutdown:          make(chan struct{}),
		StoppedAgents:     make(map[string]struct{}),
		IssueBackendReady: hasReady,
		EmitEvent:         func(events.Event) {},
	}
}

func TestHandleEpicTransition_NotAssignedToEpic(t *testing.T) {
	s := newEpicTestSupervisor(nil)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon"},
		WorktreePath: "/repo/worktrees/falcon",
	}
	s.handleEpicTransition(ap)
}

func TestHandleEpicTransition_ConfigDriven_StillHasTasks(t *testing.T) {
	s := newEpicTestSupervisor(func(epicID string) (bool, error) {
		if epicID == "epic-1" {
			return true, nil
		}
		return false, nil
	})

	ap := &AgentProcess{
		Entry:          cfgpkg.AgentEntry{Worktree: "falcon", Parent: "epic-1"},
		WorktreePath:   "/repo/worktrees/falcon",
		AssignedEpicID: "epic-1",
		RestartCount:   2,
	}

	s.handleEpicTransition(ap)

	ap.Mu.Lock()
	rc := ap.RestartCount
	noWork := ap.LastNoWork
	ap.Mu.Unlock()
	if rc != 2 {
		t.Errorf("expected restartCount unchanged at 2, got %d", rc)
	}
	if noWork {
		t.Error("expected LastNoWork=false when epic still has tasks")
	}
}

func TestHandleEpicTransition_ConfigDriven_Exhausted(t *testing.T) {
	s := newEpicTestSupervisor(func(epicID string) (bool, error) {
		return false, nil
	})

	ap := &AgentProcess{
		Entry:          cfgpkg.AgentEntry{Worktree: "falcon", Parent: "epic-1"},
		WorktreePath:   "/repo/worktrees/falcon",
		AssignedEpicID: "epic-1",
		RestartCount:   3,
	}

	s.handleEpicTransition(ap)

	ap.Mu.Lock()
	noWork := ap.LastNoWork
	eid := ap.AssignedEpicID
	lastErr := ap.LastError
	ap.Mu.Unlock()

	if !noWork {
		t.Error("expected LastNoWork=true when epic is exhausted")
	}
	if eid != "epic-1" {
		t.Errorf("expected AssignedEpicID still epic-1, got %q", eid)
	}
	if lastErr == nil {
		t.Fatal("expected LastError to be set on epic exhaustion")
	}
	if lastErr.Class != agenterr.NoWork {
		t.Errorf("expected LastError.Class=NoWork, got %v", lastErr.Class)
	}
}

func TestHandleEpicTransition_ConfigDriven_Exhausted_DoesNotMaskCrashError(t *testing.T) {
	s := newEpicTestSupervisor(func(epicID string) (bool, error) {
		return false, nil
	})

	crashErr := &agenterr.AgentError{
		Class:   agenterr.Unknown,
		Message: "segfault",
		Backend: "claude",
	}

	ap := &AgentProcess{
		Entry:          cfgpkg.AgentEntry{Worktree: "falcon", Parent: "epic-1"},
		WorktreePath:   "/repo/worktrees/falcon",
		AssignedEpicID: "epic-1",
		LastError:      crashErr,
	}

	s.handleEpicTransition(ap)

	ap.Mu.Lock()
	lastErr := ap.LastError
	noWork := ap.LastNoWork
	ap.Mu.Unlock()

	if !noWork {
		t.Error("expected LastNoWork=true")
	}
	if lastErr != crashErr {
		t.Errorf("expected LastError to remain the crash error, got %v", lastErr)
	}
}

func TestHandleEpicTransition_ConfigDriven_ReadyQueryFails(t *testing.T) {
	s := newEpicTestSupervisor(func(epicID string) (bool, error) {
		return false, fmt.Errorf("ready query failed")
	})

	ap := &AgentProcess{
		Entry:          cfgpkg.AgentEntry{Worktree: "falcon", Parent: "epic-1"},
		WorktreePath:   "/repo/worktrees/falcon",
		AssignedEpicID: "epic-1",
		RestartCount:   1,
	}

	s.handleEpicTransition(ap)

	ap.Mu.Lock()
	rc := ap.RestartCount
	ap.Mu.Unlock()
	if rc != 1 {
		t.Errorf("expected restartCount unchanged at 1, got %d", rc)
	}
}

func TestHandleEpicTransition_NoParentConfig_ClearsStaleAssignment(t *testing.T) {
	s := newEpicTestSupervisor(nil)
	ap := &AgentProcess{
		Entry:          cfgpkg.AgentEntry{Worktree: "falcon"},
		WorktreePath:   "/repo/worktrees/falcon",
		AssignedEpicID: "epic-1",
	}

	s.handleEpicTransition(ap)

	ap.Mu.Lock()
	eid := ap.AssignedEpicID
	ap.Mu.Unlock()
	if eid != "" {
		t.Errorf("expected AssignedEpicID cleared, got %q", eid)
	}
}

func TestHandleEpicTransition_WithIssueBackend_HasTasks(t *testing.T) {
	s := newEpicTestSupervisor(func(epicID string) (bool, error) {
		return true, nil
	})

	ap := &AgentProcess{
		Entry:          cfgpkg.AgentEntry{Worktree: "falcon", Parent: "epic-1"},
		WorktreePath:   "/repo/worktrees/falcon",
		AssignedEpicID: "epic-1",
	}

	s.handleEpicTransition(ap)

	ap.Mu.Lock()
	noWork := ap.LastNoWork
	lastErr := ap.LastError
	ap.Mu.Unlock()

	if noWork {
		t.Error("expected LastNoWork=false when backend returns tasks")
	}
	if lastErr != nil {
		t.Errorf("expected LastError=nil, got %v", lastErr)
	}
}

func TestHandleEpicTransition_WithIssueBackend_Exhausted(t *testing.T) {
	s := newEpicTestSupervisor(func(epicID string) (bool, error) {
		return false, nil
	})

	ap := &AgentProcess{
		Entry:          cfgpkg.AgentEntry{Worktree: "falcon", Parent: "epic-1"},
		WorktreePath:   "/repo/worktrees/falcon",
		AssignedEpicID: "epic-1",
	}

	s.handleEpicTransition(ap)

	ap.Mu.Lock()
	noWork := ap.LastNoWork
	lastErr := ap.LastError
	ap.Mu.Unlock()

	if !noWork {
		t.Error("expected LastNoWork=true when backend returns empty")
	}
	if lastErr == nil {
		t.Fatal("expected LastError to be set on epic exhaustion")
	}
	if lastErr.Class != agenterr.NoWork {
		t.Errorf("expected LastError.Class=NoWork, got %v", lastErr.Class)
	}
}

func TestHandleEpicTransition_WithIssueBackend_Error(t *testing.T) {
	s := newEpicTestSupervisor(func(epicID string) (bool, error) {
		return false, fmt.Errorf("rpc timeout")
	})

	ap := &AgentProcess{
		Entry:          cfgpkg.AgentEntry{Worktree: "falcon", Parent: "epic-1"},
		WorktreePath:   "/repo/worktrees/falcon",
		AssignedEpicID: "epic-1",
		RestartCount:   1,
	}

	s.handleEpicTransition(ap)

	ap.Mu.Lock()
	noWork := ap.LastNoWork
	rc := ap.RestartCount
	ap.Mu.Unlock()

	if noWork {
		t.Error("expected LastNoWork=false on error")
	}
	if rc != 1 {
		t.Errorf("expected restartCount unchanged at 1, got %d", rc)
	}
}
