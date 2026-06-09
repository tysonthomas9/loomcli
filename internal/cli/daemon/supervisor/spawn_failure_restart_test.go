package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

func spawnFailureTestConfig(maxRetries int) *config.DaemonConfig {
	return &config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries: config.IntPtr(maxRetries),
			},
		},
	}
}

// TestMarkSpawnFailure_SetsSyntheticExitState verifies markSpawnFailure records
// a spawn failure as a synthetic exit: exit code -1, a non-nil SpawnFailure
// error, and no NoWork flag. It must not touch RestartCount — counting belongs
// to shouldRestart.
func TestMarkSpawnFailure_SetsSyntheticExitState(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{Backend: "claude"})
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "wt", Backend: "claude"},
		RestartCount: 2,
	}

	s.markSpawnFailure(ap, errors.New(`exec: "claude": executable file not found in $PATH`))

	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.LastExitCode != -1 {
		t.Errorf("LastExitCode = %d, want -1", ap.LastExitCode)
	}
	if ap.LastError == nil {
		t.Fatal("LastError = nil, want non-nil SpawnFailure error")
	}
	if ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.SpawnFailureOutcome) {
		t.Errorf("LastError.Class = %v, want SpawnFailure", ap.LastError.Class)
	}
	if ap.LastNoWork {
		t.Error("LastNoWork = true, want false")
	}
	if ap.RestartCount != 2 {
		t.Errorf("RestartCount = %d, want 2 (markSpawnFailure must not count)", ap.RestartCount)
	}
}

// TestSpawnFailure_CountsOnceAndRespectsMaxRetries simulates the supervise loop
// for repeated spawn failures, starting from a clean prior run. Each failure
// must count exactly once and, after maxRetries, the agent enters error
// (SpawnFailure routes through the single exhaustion branch, so it stops like
// any other non-fatal class rather than retrying forever).
//
// Regression: previously the spawn-failure path left LastExitCode==0 &&
// LastError==nil, so shouldRestart's clean-success branch reset RestartCount
// every cycle and spawn failures retried forever.
func TestSpawnFailure_CountsOnceAndRespectsMaxRetries(t *testing.T) {
	const maxRetries = 3
	s := newTestSupervisorWithConfig(spawnFailureTestConfig(maxRetries))
	ap := &AgentProcess{
		Entry: config.AgentEntry{Worktree: "wt"},
		// Prior run exited cleanly — the dangerous stale state for the reset branch.
		LastExitCode: 0,
		LastError:    nil,
		RestartCount: 0,
	}

	// Each loop iteration: spawnAndWait records the failure (markSpawnFailure),
	// then the single restart decision (shouldRestart) counts it once.
	for attempt := 1; attempt <= maxRetries; attempt++ {
		s.markSpawnFailure(ap, errors.New("spawn failed"))
		if !s.shouldRestart(ap) {
			t.Fatalf("attempt %d: shouldRestart = false, want true (<= maxRetries)", attempt)
		}
		ap.Mu.Lock()
		count := ap.RestartCount
		ap.Mu.Unlock()
		if count != attempt {
			t.Fatalf("attempt %d: RestartCount = %d, want %d (exactly one increment per failure)",
				attempt, count, attempt)
		}
	}

	// One more failure exceeds maxRetries: shouldRestart stops automatic retry
	// and preserves the exhausted count for observability.
	s.markSpawnFailure(ap, errors.New("spawn failed"))
	if s.shouldRestart(ap) {
		t.Fatal("shouldRestart = true after budget exhausted, want false")
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.RestartCount != maxRetries+1 {
		t.Errorf("RestartCount = %d, want %d (preserved for observability)", ap.RestartCount, maxRetries+1)
	}
	if ap.StopReason != StopReasonMaxRetries {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonMaxRetries)
	}
}

func TestRealSubprocessExitExhaustsMaxRetriesIntoError(t *testing.T) {
	const maxRetries = 0
	binDir := t.TempDir()
	loomPath := filepath.Join(binDir, "loom")
	if err := os.WriteFile(loomPath, []byte("#!/bin/sh\necho 'transient crash from real subprocess'\nexit 1\n"), 0755); err != nil {
		t.Fatalf("write loom shim: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(t.TempDir(), "loom-config"))
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv("LOOM_WORKSPACE_ID", "")

	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			LogDir:    t.TempDir(),
			EventsDir: t.TempDir(),
			RestartPolicy: config.RestartPolicy{
				MaxRetries:     config.IntPtr(maxRetries),
				BackoffInitial: config.IntPtr(0),
				BackoffMax:     config.IntPtr(0),
			},
		},
	})
	s.ProjectDir = t.TempDir()
	s.Concurrency = NewConcurrencyTracker(nil)
	s.EmitEvent = func(events.Event) {}

	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "real-fail", Role: "plan"},
		WorktreePath: t.TempDir(),
		StopCh:       make(chan struct{}),
	}

	if !s.Concurrency.Acquire(ap.Entry.Role) {
		t.Fatal("failed to acquire concurrency slot")
	}
	if err := s.spawnAgent(ap); err != nil {
		s.Concurrency.Release(ap.Entry.Role)
		t.Fatalf("spawnAgent: %v", err)
	}
	exitCode := s.waitForAgent(ap)
	s.classifyAgentExit(ap, exitCode)
	s.Concurrency.Release(ap.Entry.Role)

	ap.Mu.Lock()
	recordedExitCode := ap.LastExitCode
	lastErr := ap.LastError
	ap.Mu.Unlock()
	if recordedExitCode != 1 {
		t.Fatalf("LastExitCode = %d, want 1 from real subprocess", recordedExitCode)
	}
	if lastErr == nil {
		t.Fatal("LastError = nil, want classified subprocess failure")
	}

	if s.shouldRestart(ap) {
		t.Fatal("shouldRestart = true after real subprocess exhausted maxRetries=0, want false")
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.RestartCount != 1 {
		t.Errorf("RestartCount = %d, want 1 (first real failure exceeds maxRetries=0)", ap.RestartCount)
	}
	if ap.StopReason != StopReasonMaxRetries {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonMaxRetries)
	}
	if _, ok := s.StoppedAgents["real-fail"]; !ok {
		t.Fatal("agent was not marked stopped for explicit resume")
	}
}

// TestSpawnFailure_AfterNonZeroExitNotDoubleCounted verifies that a spawn
// failure following a non-zero exit is counted exactly once and is classified
// as SpawnFailure rather than inheriting the previous run's stale error.
//
// Regression: previously the spawn-failure path incremented RestartCount once
// (in handleRestartAfterError) and then fell through to shouldRestart, which
// incremented again and re-decided on the stale prior error.
func TestSpawnFailure_AfterNonZeroExitNotDoubleCounted(t *testing.T) {
	s := newTestSupervisorWithConfig(spawnFailureTestConfig(5))
	ap := &AgentProcess{
		Entry: config.AgentEntry{Worktree: "wt"},
		// Stale state from a prior non-zero exit that was already counted once.
		LastExitCode: 1,
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTransient), Message: "stale"},
		RestartCount: 1,
	}

	s.markSpawnFailure(ap, errors.New("spawn failed"))

	ap.Mu.Lock()
	if ap.LastError == nil || ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.SpawnFailureOutcome) {
		ap.Mu.Unlock()
		t.Fatalf("LastError = %v, want SpawnFailure (stale class must be overwritten)", ap.LastError)
	}
	ap.Mu.Unlock()

	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart = false, want true (under maxRetries)")
	}

	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.RestartCount != 2 {
		t.Errorf("RestartCount = %d, want 2 (single increment, no double-count)", ap.RestartCount)
	}
}
