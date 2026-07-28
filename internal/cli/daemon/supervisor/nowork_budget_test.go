package supervisor

import (
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// budgetSupervisor supplies the config the backoff path reads. applyNoWorkRestart
// consults it once the post-spawn streak exceeds one, so a bare &Supervisor{}
// nil-derefs there.
func budgetSupervisor() *Supervisor {
	return &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{} }}
}

// The restart budget must survive a no-work exit.
//
// Resetting RestartCount on every no-op made max_retries unreachable for any
// agent whose failures interleave with no-work exits: each idle poll wiped the
// evidence of the previous failure, so the agent respawned forever. That is
// DOGFOOD-36 — 742 no-op critic spawns, 43/hr, a 191 MB log.
//
// The symptom took hours to accumulate, but the invariant is a single state
// transition, so it does not need sustained observation to pin.
func TestApplyNoWorkRestart_PreservesRestartBudget(t *testing.T) {
	s := budgetSupervisor()

	for _, start := range []int{0, 1, 5} {
		ap := &AgentProcess{RestartCount: start}

		s.applyNoWorkRestart(ap)

		if ap.RestartCount != start {
			t.Errorf("RestartCount = %d after a no-work exit, want it untouched at %d",
				ap.RestartCount, start)
		}
	}
}

// Repeated no-work exits must not erode the budget either — the original bug
// only became visible after many of them.
func TestApplyNoWorkRestart_BudgetSurvivesRepeatedIdlePolls(t *testing.T) {
	s := budgetSupervisor()
	ap := &AgentProcess{RestartCount: 2}

	for i := 0; i < 50; i++ {
		s.applyNoWorkRestart(ap)
	}

	if ap.RestartCount != 2 {
		t.Errorf("RestartCount = %d after 50 idle polls, want 2", ap.RestartCount)
	}
	if ap.NoWorkCount != 50 {
		t.Errorf("NoWorkCount = %d, want 50 — idle polls should still be counted", ap.NoWorkCount)
	}
}

// A genuinely clean run is the correct "progress" signal, and it must still
// clear the budget — otherwise the fix would strand agents that are working.
func TestApplyCleanSuccessRestart_ResetsBudget(t *testing.T) {
	s := budgetSupervisor()
	ap := &AgentProcess{RestartCount: 4, NoWorkCount: 9, NoWorkSpawnCount: 3, BlockCount: 2}

	s.applyCleanSuccessRestart(ap)

	if ap.RestartCount != 0 {
		t.Errorf("RestartCount = %d after a clean run, want 0", ap.RestartCount)
	}
	if ap.NoWorkCount != 0 || ap.NoWorkSpawnCount != 0 {
		t.Errorf("no-work counters = (%d, %d) after a clean run, want (0, 0)",
			ap.NoWorkCount, ap.NoWorkSpawnCount)
	}
}

// The post-spawn streak drives the escalating poll backoff, so it has to count
// consecutive spawns and reset when a poll finds work.
func TestApplyNoWorkRestart_PostSpawnStreak(t *testing.T) {
	s := budgetSupervisor()
	ap := &AgentProcess{LastNoWorkPostSpawn: true}

	s.applyNoWorkRestart(ap)
	s.applyNoWorkRestart(ap)
	if ap.NoWorkSpawnCount != 2 {
		t.Errorf("NoWorkSpawnCount = %d after two post-spawn no-ops, want 2", ap.NoWorkSpawnCount)
	}

	ap.LastNoWorkPostSpawn = false
	s.applyNoWorkRestart(ap)
	if ap.NoWorkSpawnCount != 0 {
		t.Errorf("NoWorkSpawnCount = %d, want 0 once the streak breaks", ap.NoWorkSpawnCount)
	}
}
