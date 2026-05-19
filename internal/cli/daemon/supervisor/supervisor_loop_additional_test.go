package supervisor

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
)

func TestSuperviseAgentStopsWhenConcurrencyClosed(t *testing.T) {
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
	s.Concurrency = NewConcurrencyTracker(nil)
	s.Concurrency.Close()
	s.EmitEvent = func(events.Event) {}

	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker", Role: "task"},
		RoleConfig:   cfgpkg.RoleConfig{},
		WorktreePath: t.TempDir(),
		StopCh:       make(chan struct{}),
	}

	s.superviseAgent(ap)

	if ap.StopReason != StopReasonShutdown {
		t.Fatalf("StopReason = %q, want shutdown", ap.StopReason)
	}
}

func TestPreFlightSetupRecordsEphemeralNoWork(t *testing.T) {
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
	s.IssueBackend = &ephemeralNoopBackend{}
	s.EmitEvent = func(events.Event) {}
	ap := &AgentProcess{
		Entry: cfgpkg.AgentEntry{
			Worktree: "ephemeral-worker",
			Role:     "task",
			Mode:     domain.AgentModeEphemeral,
		},
		RoleConfig:   cfgpkg.RoleConfig{TaskFilter: "any"},
		WorktreePath: t.TempDir(),
	}

	if s.preFlightSetup(ap) {
		t.Fatal("preFlightSetup returned true for ephemeral worker without requested task")
	}
	if ap.LastError == nil || !strings.Contains(ap.LastError.Message, "requires a requested task") {
		t.Fatalf("LastError = %+v, want requested-task preflight error", ap.LastError)
	}
}

func TestSpawnAndWaitBuildFailureStopsOnShutdown(t *testing.T) {
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
	s.Concurrency = NewConcurrencyTracker(nil)
	s.EmitEvent = func(events.Event) {}
	close(s.Shutdown)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "custom-worker", Role: "custom"},
		RoleConfig:   cfgpkg.RoleConfig{},
		WorktreePath: t.TempDir(),
		StopCh:       make(chan struct{}),
	}

	if s.spawnAndWait(ap) {
		t.Fatal("spawnAndWait returned true after build failure and shutdown")
	}
	if ap.RestartCount != 1 {
		t.Fatalf("RestartCount = %d, want 1", ap.RestartCount)
	}
	if ap.StopReason != StopReasonShutdown {
		t.Fatalf("StopReason = %q, want shutdown", ap.StopReason)
	}
}

func TestAddAgentForTaskAppendsRuntimeAgentBeforeClosedShutdown(t *testing.T) {
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
	s.Concurrency = NewConcurrencyTracker(nil)
	s.FindRepoConfig = func(string) *cfgpkg.RepoConfig { return nil }
	s.EmitEvent = func(events.Event) {}
	close(s.Shutdown)

	worktree := t.TempDir()
	if err := s.AddAgentForTask(cfgpkg.AgentEntry{Worktree: worktree, Role: "task"}, "TASK-1", "lead-session"); err != nil {
		t.Fatalf("AddAgentForTask: %v", err)
	}
	s.Wg.Wait()

	if len(s.Agents) != 1 {
		t.Fatalf("Agents len = %d, want 1", len(s.Agents))
	}
	ap := s.Agents[0]
	if ap.RequestedTaskID != "TASK-1" || ap.ParentSessionID != "lead-session" || ap.WorktreePath != worktree {
		t.Fatalf("runtime agent = %+v", ap)
	}
	if ap.StopReason != StopReasonShutdown {
		t.Fatalf("StopReason = %q, want shutdown", ap.StopReason)
	}

	if err := s.AddAgentForTask(cfgpkg.AgentEntry{Worktree: "ephemeral", Role: "task", Mode: domain.AgentModeEphemeral}, ""); err == nil {
		t.Fatal("AddAgentForTask ephemeral without task returned nil error")
	}
}

type ephemeralNoopBackend struct {
	backend.IssueBackend
}
