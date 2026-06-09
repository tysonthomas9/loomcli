package supervisor

import (
	"strings"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// shouldRestart's ephemeral branch:
//   ephemeral mode + clean exit + a task was actually claimed → return false,
//   StopReason = ephemeral_done. Worker stays in fleet-db (state=stopped).
//
// Service mode (or empty mode) preserves all existing semantics.

func newEphemeralSupervisor(t *testing.T) *Supervisor {
	t.Helper()
	cfg := makeSupervisorConfig(
		[]cfgpkg.AgentEntry{{Worktree: "test", Role: "task", Mode: domain.AgentModeEphemeral}},
		nil,
	)
	cfg.Daemon.RestartPolicy.MaxRetries = cfgpkg.IntPtr(3)
	return &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg },
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
	}
}

func TestShouldRestart_Ephemeral_CleanExitWithClaim_DoesNotRestart(t *testing.T) {
	s := newEphemeralSupervisor(t)
	ap := &AgentProcess{
		Entry:          cfgpkg.AgentEntry{Worktree: "test", Role: "task", Mode: domain.AgentModeEphemeral},
		LastExitCode:   0,
		LastError:      nil,
		AssignedTaskID: "auth-3",
	}

	if got := s.shouldRestart(ap); got {
		t.Fatalf("shouldRestart = true, want false (ephemeral after clean exit + claimed task)")
	}
	if ap.StopReason != StopReasonEphemeralDone {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonEphemeralDone)
	}
}

func TestAddAgentForTask_EphemeralRequiresTaskID(t *testing.T) {
	s := newEphemeralSupervisor(t)

	err := s.AddAgentForTask(cfgpkg.AgentEntry{
		Worktree: "test",
		Role:     "task",
		Mode:     domain.AgentModeEphemeral,
	}, "")
	if err == nil {
		t.Fatal("AddAgentForTask returned nil, want task_id validation error")
	}
	if !strings.Contains(err.Error(), "requires a task_id") {
		t.Fatalf("error = %q, want requires a task_id", err)
	}
}

func TestShouldRestart_Ephemeral_NoTaskClaimed_FallsThroughToCleanSuccess(t *testing.T) {
	// An ephemeral agent that exited cleanly without claiming a task (e.g. a
	// transient run that found no work) should NOT trigger ephemeral_done — it
	// falls through to the existing "clean success" restart branch so the agent
	// can keep polling Ready() until a task arrives.
	s := newEphemeralSupervisor(t)
	ap := &AgentProcess{
		Entry:          cfgpkg.AgentEntry{Worktree: "test", Role: "task", Mode: domain.AgentModeEphemeral},
		LastExitCode:   0,
		LastError:      nil,
		AssignedTaskID: "",
	}

	if got := s.shouldRestart(ap); !got {
		t.Fatalf("shouldRestart = false, want true (ephemeral + clean exit + no claim falls through)")
	}
	if ap.StopReason == StopReasonEphemeralDone {
		t.Errorf("StopReason = %q, ephemeral_done should not fire when no task was claimed", ap.StopReason)
	}
}

func TestShouldRestart_Ephemeral_ErrorExit_RetriesPerExistingPolicy(t *testing.T) {
	// Errors on an ephemeral agent must still retry up to max_retries — ephemeral
	// means "exit after one *successful* task," not "exit on first attempt."
	s := newEphemeralSupervisor(t)
	ap := &AgentProcess{
		Entry:          cfgpkg.AgentEntry{Worktree: "test", Role: "task", Mode: domain.AgentModeEphemeral},
		RestartCount:   1,
		LastExitCode:   1,
		LastError:      &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrUnknown)},
		AssignedTaskID: "auth-3",
	}

	if got := s.shouldRestart(ap); !got {
		t.Fatalf("shouldRestart = false, want true (ephemeral with error under retry limit)")
	}
	if ap.RestartCount != 2 {
		t.Errorf("RestartCount = %d, want 2 (incremented on error)", ap.RestartCount)
	}
	if ap.StopReason == StopReasonEphemeralDone {
		t.Errorf("StopReason = %q, ephemeral_done should not fire on error", ap.StopReason)
	}
}

func TestShouldRestart_Service_CleanExitWithClaim_StillRestarts(t *testing.T) {
	// Service-mode agent (the default) with a clean exit + claimed task should
	// keep its existing restart-loop behavior. The ephemeral branch must not
	// short-circuit here.
	cfg := makeSupervisorConfig(
		[]cfgpkg.AgentEntry{{Worktree: "test", Role: "task", Mode: domain.AgentModeService}},
		nil,
	)
	cfg.Daemon.RestartPolicy.MaxRetries = cfgpkg.IntPtr(3)
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg },
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
	}
	ap := &AgentProcess{
		Entry:          cfgpkg.AgentEntry{Worktree: "test", Role: "task", Mode: domain.AgentModeService},
		LastExitCode:   0,
		LastError:      nil,
		AssignedTaskID: "auth-3",
	}

	if got := s.shouldRestart(ap); !got {
		t.Fatalf("shouldRestart = false, want true (service mode keeps looping)")
	}
	if ap.StopReason == StopReasonEphemeralDone {
		t.Errorf("StopReason = %q, ephemeral_done must never fire in service mode", ap.StopReason)
	}
}

func TestShouldRestart_EmptyMode_DefaultsToServiceBehavior(t *testing.T) {
	// Agents created without an explicit Mode (empty string) must keep the
	// pre-existing always-restart behavior. No silent regression.
	cfg := makeSupervisorConfig(
		[]cfgpkg.AgentEntry{{Worktree: "test", Role: "task"}},
		nil,
	)
	cfg.Daemon.RestartPolicy.MaxRetries = cfgpkg.IntPtr(3)
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg },
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
	}
	ap := &AgentProcess{
		Entry:          cfgpkg.AgentEntry{Worktree: "test", Role: "task"},
		LastExitCode:   0,
		LastError:      nil,
		AssignedTaskID: "auth-3",
	}

	if got := s.shouldRestart(ap); !got {
		t.Fatalf("shouldRestart = false, want true (empty mode = service default)")
	}
	if ap.StopReason == StopReasonEphemeralDone {
		t.Errorf("StopReason = %q, ephemeral_done must not fire on empty Mode", ap.StopReason)
	}
}
