package doctor

import (
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/daemon"
)

// freshState returns a daemon state at "now" with no backoff in flight.
func freshState(worktree string) daemon.DaemonState {
	return daemon.DaemonState{
		PID:       12345,
		StartedAt: time.Now().Add(-1 * time.Hour),
		Agents: []daemon.DaemonAgentStatus{{
			Worktree: worktree,
			Role:     "task",
			Status:   "running",
		}},
	}
}

// TestEvaluateDaemonStuck_PassWhenAgentsAreFresh covers the happy path: no
// backoff_until set, state file written recently, no signal of a stuck loop.
func TestEvaluateDaemonStuck_PassWhenAgentsAreFresh(t *testing.T) {
	now := time.Now()
	state := freshState("worker-1")

	result := evaluateDaemonStuck(state, now.Add(-5*time.Second), now, 30*time.Second)

	if result.Status != StatusPass {
		t.Fatalf("expected StatusPass for fresh state, got %s (%s / %s)",
			result.Status, result.Summary, result.Detail)
	}
	if result.Name != "daemon_stuck" {
		t.Errorf("unexpected check name: %q", result.Name)
	}
}

// TestEvaluateDaemonStuck_FailAcceptanceCase reproduces the observed bug:
// daemon-agents.json shows backoff_until pinned 5 hours in the past while the
// daemon is alive. With no_work_backoff=30s, lateness=5h > 2×30s=60s, so the
// check must return StatusFail. This is **Acceptance #2**.
func TestEvaluateDaemonStuck_FailAcceptanceCase(t *testing.T) {
	now := time.Now()
	state := daemon.DaemonState{
		PID:       12345,
		StartedAt: now.Add(-1 * 24 * time.Hour),
		Agents: []daemon.DaemonAgentStatus{{
			Worktree:     "tree-clustering-worker",
			Role:         "task",
			Status:       "running",
			BackoffUntil: now.Add(-5 * time.Hour),
		}},
	}

	result := evaluateDaemonStuck(state, now.Add(-5*time.Second), now, 30*time.Second)

	if result.Status != StatusFail {
		t.Fatalf("expected StatusFail for stuck backoff, got %s\nsummary: %s\ndetail: %s",
			result.Status, result.Summary, result.Detail)
	}
	if !strings.Contains(result.Detail, "tree-clustering-worker") {
		t.Errorf("expected detail to name the stuck worktree, got: %s", result.Detail)
	}
	if !strings.Contains(result.Detail, "backoff_until") {
		t.Errorf("expected detail to explain backoff_until staleness, got: %s", result.Detail)
	}
}

// TestEvaluateDaemonStuck_WarnAtOneBackoffWindow covers the "slipping but not
// yet broken" case (>1× but <=2× no_work_backoff).
func TestEvaluateDaemonStuck_WarnAtOneBackoffWindow(t *testing.T) {
	now := time.Now()
	state := daemon.DaemonState{
		PID: 12345,
		Agents: []daemon.DaemonAgentStatus{{
			Worktree:     "wobbly-worker",
			Status:       "running",
			BackoffUntil: now.Add(-45 * time.Second),
		}},
	}

	result := evaluateDaemonStuck(state, now.Add(-5*time.Second), now, 30*time.Second)

	if result.Status != StatusWarn {
		t.Fatalf("expected StatusWarn at >1×backoff lateness, got %s (%s)",
			result.Status, result.Summary)
	}
}

// TestEvaluateDaemonStuck_FutureBackoffIsFine: backoff_until set to a future
// time means the agent is legitimately in the middle of a backoff sleep — not
// a freeze signal.
func TestEvaluateDaemonStuck_FutureBackoffIsFine(t *testing.T) {
	now := time.Now()
	state := daemon.DaemonState{
		PID: 12345,
		Agents: []daemon.DaemonAgentStatus{{
			Worktree:     "current-backoff",
			Status:       "running",
			BackoffUntil: now.Add(20 * time.Second),
		}},
	}

	result := evaluateDaemonStuck(state, now.Add(-5*time.Second), now, 30*time.Second)

	if result.Status != StatusPass {
		t.Fatalf("expected StatusPass when backoff is still active, got %s",
			result.Status)
	}
}

// TestEvaluateDaemonStuck_StateFileMtimeWedged: the 5s state updater goroutine
// should keep daemon-agents.json mtime fresh. A 60s-old mtime while the
// daemon PID is reported as alive proves the updater is dead.
func TestEvaluateDaemonStuck_StateFileMtimeWedged(t *testing.T) {
	now := time.Now()
	state := freshState("worker-1")

	result := evaluateDaemonStuck(state, now.Add(-60*time.Second), now, 30*time.Second)

	if result.Status != StatusFail {
		t.Fatalf("expected StatusFail for stale state file, got %s (%s)",
			result.Status, result.Summary)
	}
	if !strings.Contains(result.Detail, "state updater wedged") {
		t.Errorf("expected detail to call out state updater, got: %s", result.Detail)
	}
}

// TestEvaluateDaemonStuck_HonorsCustomNoWorkBackoff: a workspace with a
// longer no_work_backoff (e.g. 5min) raises the failure threshold accordingly.
func TestEvaluateDaemonStuck_HonorsCustomNoWorkBackoff(t *testing.T) {
	now := time.Now()
	state := daemon.DaemonState{
		PID: 12345,
		Agents: []daemon.DaemonAgentStatus{{
			Worktree:     "slow-cycle-worker",
			Status:       "running",
			BackoffUntil: now.Add(-4 * time.Minute), // would fail at default 30s
		}},
	}

	// With 5min no_work_backoff, 2× = 10min. 4min lateness is below 1× even,
	// so this must Pass.
	result := evaluateDaemonStuck(state, now.Add(-5*time.Second), now, 5*time.Minute)

	if result.Status != StatusPass {
		t.Fatalf("expected StatusPass under custom 5min backoff, got %s (%s)",
			result.Status, result.Summary)
	}
}

// TestEvaluateDaemonStuck_ZeroBackoffFallsBackToDefault guards the helper
// against zero/negative no_work_backoff inputs (e.g., config not loaded).
func TestEvaluateDaemonStuck_ZeroBackoffFallsBackToDefault(t *testing.T) {
	now := time.Now()
	state := daemon.DaemonState{
		PID: 12345,
		Agents: []daemon.DaemonAgentStatus{{
			Worktree:     "bad-config-worker",
			Status:       "running",
			BackoffUntil: now.Add(-5 * time.Minute),
		}},
	}

	result := evaluateDaemonStuck(state, now.Add(-5*time.Second), now, 0)

	if result.Status != StatusFail {
		t.Fatalf("expected fallback default to flag stuck backoff, got %s",
			result.Status)
	}
}
