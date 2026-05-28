package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/daemon"
)

// writeDaemonStuckFixture lays out a temp project dir with a .loom/ containing
// a pid file (pointing at pid) and, when state != nil, a daemon-agents.json
// whose mtime is set to stateMtime. It chdirs into the project dir for the
// duration of the test so checkDaemonStuck's os.Getwd() resolves there, and
// keeps config lookups local + offline.
func writeDaemonStuckFixture(t *testing.T, pid int, state *daemon.DaemonState, stateMtime time.Time) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	t.Setenv("LOOM_FLEET_DB_NO_DISCOVERY", "1")

	loomDir := filepath.Join(dir, ".loom")
	if err := os.MkdirAll(loomDir, 0o755); err != nil {
		t.Fatalf("mkdir .loom: %v", err)
	}
	if pid > 0 {
		if err := os.WriteFile(filepath.Join(loomDir, "daemon.pid"),
			[]byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
			t.Fatalf("write pid file: %v", err)
		}
	}
	if state != nil {
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatalf("marshal state: %v", err)
		}
		statePath := filepath.Join(loomDir, "daemon-agents.json")
		if err := os.WriteFile(statePath, data, 0o600); err != nil {
			t.Fatalf("write state file: %v", err)
		}
		if !stateMtime.IsZero() {
			if err := os.Chtimes(statePath, stateMtime, stateMtime); err != nil {
				t.Fatalf("chtimes state file: %v", err)
			}
		}
	}
	t.Chdir(dir)
}

func TestCheckDaemonStuck_DeadDaemonReturnsEmpty(t *testing.T) {
	// pid 1 is alive but not a loom daemon; use a pid that is almost
	// certainly dead so IsLoomDaemonRunning reports not-running.
	writeDaemonStuckFixture(t, 2147483600, nil, time.Time{})

	result := checkDaemonStuck()
	if result.Name != "" {
		t.Fatalf("expected empty CheckResult when daemon not running, got %+v", result)
	}
}

func TestCheckDaemonStuck_RunningButStateFileMissing(t *testing.T) {
	writeDaemonStuckFixture(t, os.Getpid(), nil, time.Time{})

	result := checkDaemonStuck()
	if result.Status != StatusWarn {
		t.Fatalf("expected StatusWarn for missing state file, got %s (%s)",
			result.Status, result.Summary)
	}
	if !strings.Contains(result.Summary, "state file missing") {
		t.Errorf("expected summary to mention missing state file, got: %s", result.Summary)
	}
}

func TestCheckDaemonStuck_RunningWithStuckBackoffFails(t *testing.T) {
	now := time.Now()
	state := &daemon.DaemonState{
		PID:       os.Getpid(),
		StartedAt: now.Add(-time.Hour),
		Agents: []daemon.DaemonAgentStatus{{
			Worktree:     "wedged-worker",
			Role:         "task",
			Status:       "running",
			BackoffUntil: now.Add(-5 * time.Minute),
		}},
	}
	writeDaemonStuckFixture(t, os.Getpid(), state, now)

	result := checkDaemonStuck()
	if result.Status != StatusFail {
		t.Fatalf("expected StatusFail for stuck backoff, got %s (%s / %s)",
			result.Status, result.Summary, result.Detail)
	}
	if !strings.Contains(result.Detail, "wedged-worker") {
		t.Errorf("expected detail to name the worktree, got: %s", result.Detail)
	}
}

func TestCheckDaemonStuck_RunningWithStaleMtimeFails(t *testing.T) {
	now := time.Now()
	state := &daemon.DaemonState{
		PID:       os.Getpid(),
		StartedAt: now.Add(-time.Hour),
		Agents: []daemon.DaemonAgentStatus{{
			Worktree:     "fresh-worker",
			Role:         "task",
			Status:       "running",
			BackoffUntil: now.Add(2 * time.Minute), // future → not a backoff failure
		}},
	}
	// Backdate the state file mtime well past the 30s staleness threshold.
	writeDaemonStuckFixture(t, os.Getpid(), state, now.Add(-90*time.Second))

	result := checkDaemonStuck()
	if result.Status != StatusFail {
		t.Fatalf("expected StatusFail for stale mtime, got %s (%s / %s)",
			result.Status, result.Summary, result.Detail)
	}
	if !strings.Contains(result.Detail, "state updater wedged") {
		t.Errorf("expected detail to call out state updater, got: %s", result.Detail)
	}
}

func TestCheckDaemonStuck_RunningAndHealthyPasses(t *testing.T) {
	now := time.Now()
	state := &daemon.DaemonState{
		PID:       os.Getpid(),
		StartedAt: now.Add(-time.Hour),
		Agents: []daemon.DaemonAgentStatus{{
			Worktree: "happy-worker",
			Role:     "task",
			Status:   "running",
			// no backoff_until → zero value, skipped
		}},
	}
	writeDaemonStuckFixture(t, os.Getpid(), state, now)

	result := checkDaemonStuck()
	if result.Status != StatusPass {
		t.Fatalf("expected StatusPass for healthy daemon, got %s (%s / %s)",
			result.Status, result.Summary, result.Detail)
	}
}

func TestCheckDaemonStuck_RunningWithUnparseableStateWarns(t *testing.T) {
	writeDaemonStuckFixture(t, os.Getpid(), nil, time.Time{})
	// Write a deliberately malformed state file.
	statePath := filepath.Join(".loom", "daemon-agents.json")
	if err := os.WriteFile(statePath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write malformed state: %v", err)
	}

	result := checkDaemonStuck()
	if result.Status != StatusWarn {
		t.Fatalf("expected StatusWarn for unparseable state, got %s (%s)",
			result.Status, result.Summary)
	}
	if !strings.Contains(result.Summary, "could not parse") {
		t.Errorf("expected summary to mention parse failure, got: %s", result.Summary)
	}
}

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
