package driver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var staleTaskClockOrigin = time.Now()

const (
	staleTaskRunErrorClass   = "stale_task_run"
	staleTaskRunErrorMessage = "task run heartbeat is stale"
)

// StaleTaskSweeper is the test-only legacy server-side fault-recovery loop for TaskRuns. It
// fails running TaskRuns whose heartbeat is older than MaxAge so workflows
// never have to call recoverStale themselves (fault policy is not workflow
// code). It reuses the same store method as the recover-stale-tasks driver
// op, which stays available for manual/compat use.
type StaleTaskSweeper struct {
	Store store.Store
	// WorkspaceKey scopes the sweep to one workspace. Empty sweeps every
	// workspace returned by Store.Workspaces().List.
	WorkspaceKey string
	// MaxAge is the heartbeat staleness threshold. Zero or negative falls
	// back to defaultStaleTaskRunMaxAge (1200s); override via the
	// LOOM_DRIVER_STALE_TASK_MAX_AGE env knob wired in loom serve.
	MaxAge time.Duration
	// Now is the wall-clock seam used to keep recovery cutoffs in the same clock
	// domain as persisted heartbeat timestamps. MonotonicNow limits forward
	// progress, while the live wall clock limits backward-clock false positives.
	// Nil uses time.Now.
	Now func() time.Time
	// MonotonicNow returns elapsed process time. Nil uses time.Since against a
	// process-local monotonic anchor. Tests inject it together with Now to prove
	// forward and backward wall-clock jump behavior.
	MonotonicNow func() time.Duration
	// ClockOrigin is the wall time corresponding to MonotonicNow()==0. Production
	// uses the package's process-start anchor. Tests that begin after a simulated
	// jump set this explicitly so the first sweep is protected too.
	ClockOrigin time.Time

	clockMu          sync.Mutex
	clockInitialized bool
	lastMonotonic    time.Duration
	logicalNow       time.Time
}

// StaleTaskSweepResult aggregates the per-driver-run recovery results of one
// sweep pass.
type StaleTaskSweepResult struct {
	Recovered           int
	SkippedFresh        int
	RecoveredTaskRunIDs []string
}

// RunOnce performs a single sweep: list running DriverRuns in each target
// workspace and fail their TaskRuns whose heartbeat predates now-MaxAge.
func (s *StaleTaskSweeper) RunOnce(ctx context.Context) (*StaleTaskSweepResult, error) {
	if s == nil || s.Store == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	staleBefore := s.recoveryNow().Add(-s.maxAge())
	workspaces, err := s.workspaceKeys(ctx)
	if err != nil {
		return nil, err
	}
	out := &StaleTaskSweepResult{}
	for _, ws := range workspaces {
		if err := s.sweepWorkspace(ctx, ws, staleBefore, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *StaleTaskSweeper) sweepWorkspace(ctx context.Context, ws string, staleBefore time.Time, out *StaleTaskSweepResult) error {
	runs, err := s.Store.DriverRuns().List(ctx, ws, store.DriverRunFilter{Status: domain.DriverRunRunning})
	if err != nil {
		return fmt.Errorf("list running driver runs in workspace %q: %w", ws, err)
	}
	for _, run := range runs {
		if run == nil {
			continue
		}
		result, err := s.Store.DriverRuns().RecoverStaleTaskRuns(ctx, ws, run.RunID, store.StaleTaskRunRecovery{
			StaleBefore:  staleBefore,
			ErrorClass:   staleTaskRunErrorClass,
			ErrorMessage: staleTaskRunErrorMessage,
		})
		if err != nil {
			return fmt.Errorf("recover stale task runs for driver run %q: %w", run.RunID, err)
		}
		out.Recovered += result.Recovered
		out.SkippedFresh += result.SkippedFresh
		out.RecoveredTaskRunIDs = append(out.RecoveredTaskRunIDs, result.RecoveredTaskRunIDs...)
	}
	return nil
}

// workspaceKeys resolves the sweep targets: the configured workspace, or
// every known workspace when unscoped (mirrors Executor.RecoverStaleOnce).
func (s *StaleTaskSweeper) workspaceKeys(ctx context.Context) ([]string, error) {
	return resolveSweepWorkspaces(ctx, s.Store, s.WorkspaceKey, "stale task sweep")
}

func (s *StaleTaskSweeper) maxAge() time.Duration {
	if s.MaxAge > 0 {
		return s.MaxAge
	}
	return DefaultStaleTaskRunMaxAge
}

func (s *StaleTaskSweeper) wallNow() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *StaleTaskSweeper) monotonicNow() time.Duration {
	if s.MonotonicNow != nil {
		return s.MonotonicNow()
	}
	return time.Since(staleTaskClockOrigin)
}

// recoveryNow returns the earlier of a process-local monotonic projection and
// the current wall clock. The projection prevents a forward wall-clock jump
// from aging every persisted heartbeat at once. The wall-clock floor keeps a
// fresh heartbeat written after a backward jump in the same timestamp domain
// as the recovery cutoff. A backward jump is therefore conservative: records
// from the old epoch may wait for wall time to catch up, but fresh records are
// never sacrificed to preserve immediate recovery.
func (s *StaleTaskSweeper) recoveryNow() time.Time {
	s.clockMu.Lock()
	defer s.clockMu.Unlock()

	monotonic := s.monotonicNow()
	wall := s.wallNow()
	if !s.clockInitialized {
		s.clockInitialized = true
		s.lastMonotonic = monotonic
		s.logicalNow = s.initialLogicalNow(wall, monotonic)
		if wall.Before(s.logicalNow) {
			return wall
		}
		return s.logicalNow
	}

	elapsed := monotonic - s.lastMonotonic
	if elapsed < 0 {
		elapsed = 0
	}
	s.lastMonotonic = monotonic
	s.logicalNow = s.logicalNow.Add(elapsed)
	if wall.Before(s.logicalNow) {
		return wall
	}
	return s.logicalNow
}

func (s *StaleTaskSweeper) initialLogicalNow(wall time.Time, monotonic time.Duration) time.Time {
	if !s.ClockOrigin.IsZero() {
		return s.ClockOrigin.UTC().Add(monotonic)
	}
	if s.Now == nil && s.MonotonicNow == nil {
		return staleTaskClockOrigin.Add(monotonic).UTC()
	}
	// A custom clock without an explicit origin retains the conventional test
	// behavior that its first wall/monotonic pair establishes the anchor.
	return wall
}
