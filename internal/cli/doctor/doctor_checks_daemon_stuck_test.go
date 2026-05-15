package doctor

import (
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/daemon"
)

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

func TestEvaluateDaemonStuck_PassWhenAgentsAreFresh(t *testing.T) {
	now := time.Now()
	state := freshState("worker-1")

	result := evaluateDaemonStuck(state, now.Add(-5*time.Second), now, 30*time.Second)

	if result.Status != StatusPass {
		t.Fatalf("expected StatusPass, got %s (%s / %s)",
			result.Status, result.Summary, result.Detail)
	}
	if result.Name != "daemon_stuck" {
		t.Errorf("unexpected check name: %q", result.Name)
	}
}

// TestEvaluateDaemonStuck_FailAcceptanceCase reproduces the observed bug:
// backoff_until pinned 5 hours in the past while no_work_backoff=30s.
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
		t.Errorf("expected detail to mention backoff_until, got: %s", result.Detail)
	}
}

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

func TestEvaluateDaemonStuck_HonorsCustomNoWorkBackoff(t *testing.T) {
	now := time.Now()
	state := daemon.DaemonState{
		PID: 12345,
		Agents: []daemon.DaemonAgentStatus{{
			Worktree:     "slow-cycle-worker",
			Status:       "running",
			BackoffUntil: now.Add(-4 * time.Minute),
		}},
	}

	result := evaluateDaemonStuck(state, now.Add(-5*time.Second), now, 5*time.Minute)

	if result.Status != StatusPass {
		t.Fatalf("expected StatusPass under custom 5min backoff, got %s (%s)",
			result.Status, result.Summary)
	}
}

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
