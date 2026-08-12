package supervisor

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

func newClassifySupervisor(backend string) *Supervisor {
	cfg := &cfgpkg.DaemonConfig{Backend: backend}
	return &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg },
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		Agents:         make([]*AgentProcess, 0),
		EmitEvent:      func(events.Event) {},
	}
}

// The other half of the interactive-invocation guard: the CLI refuses and exits
// non-zero, and the supervisor has to read that as a real failure. This pins the
// join. Both classifications it must NOT produce are failure modes we have seen:
// a clean verdict advances the pipeline on a run that did nothing, and a NoWork
// verdict quietly parks an agent that in fact has work and a bug.
func TestClassifyAgentExit_TaskBearingFailureIsNeverCleanOrNoWork(t *testing.T) {
	for _, exitCode := range []int{1, 2, 127} {
		s := newClassifySupervisor("claude")
		ap := &AgentProcess{
			Entry:          cfgpkg.AgentEntry{Worktree: "worker", Role: "task", Backend: "claude"},
			WorktreePath:   t.TempDir(),
			AssignedTaskID: "TASK-1",
		}

		s.classifyAgentExit(ap, exitCode)

		ap.Mu.Lock()
		lastErr, noWork := ap.LastError, ap.LastNoWork
		ap.Mu.Unlock()

		if lastErr == nil {
			t.Fatalf("exit %d with a claimed task must not be classified clean", exitCode)
		}
		if noWork {
			t.Fatalf("exit %d with a claimed task must not be classified NoWork", exitCode)
		}
		if lastErr.Class == agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome) {
			t.Fatalf("exit %d classified %v, want a real failure class", exitCode, lastErr.Class)
		}
	}
}

// A daemon child can also fail before it ever claims anything — the
// interactive-invocation refusal fires before the first turn, so there is no
// task on the lock. That must still be a failure and not the idle verdict,
// which is reserved for a clean exit with nothing to do.
func TestClassifyAgentExit_TasklessFailureIsNotIdle(t *testing.T) {
	s := newClassifySupervisor("claude")
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker", Role: "task", Backend: "claude"},
		WorktreePath: t.TempDir(),
	}

	s.classifyAgentExit(ap, 1)

	ap.Mu.Lock()
	lastErr, noWork := ap.LastError, ap.LastNoWork
	ap.Mu.Unlock()

	if noWork {
		t.Fatal("a non-zero exit is not the idle verdict, even with no claimed task")
	}
	if lastErr == nil || lastErr.Class == agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome) {
		t.Fatalf("LastError = %#v, want a real failure class", lastErr)
	}
}
