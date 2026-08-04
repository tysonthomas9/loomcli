package supervisor

import (
	"errors"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
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
// must count exactly once and the supervisor must give up after maxRetries.
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

	// One more failure exceeds maxRetries → instead of giving up, the agent
	// blocks-and-retries: shouldRestart stays true, the budget resets, and the
	// stop reason flips to blocked.
	s.markSpawnFailure(ap, errors.New("spawn failed"))
	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart = false after budget exhausted, want true (blocks)")
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.RestartCount != 0 {
		t.Errorf("RestartCount = %d, want 0 (reset on block)", ap.RestartCount)
	}
	if ap.StopReason != StopReasonMaxRetriesBlocked {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonMaxRetriesBlocked)
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
