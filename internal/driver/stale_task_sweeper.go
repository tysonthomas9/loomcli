package driver

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// defaultStaleTaskRunMaxAge is how old a running TaskRun's heartbeat may be
// before the sweeper fails it, when no MaxAge is configured. Sized for the
// longest legitimate task runs — a daytona sandbox provision + git clone +
// agent run is routinely 10-15 minutes, and the old 5-minute default swept
// live runs (observed: a real daytona run killed at 11.3m). Deployments that
// only run fast local tasks can tighten this via LOOM_DRIVER_STALE_TASK_MAX_AGE.
const defaultStaleTaskRunMaxAge = 20 * time.Minute

const (
	staleTaskRunErrorClass   = "stale_task_run"
	staleTaskRunErrorMessage = "task run heartbeat is stale"
)

// StaleTaskSweeper is the server-side fault-recovery loop for TaskRuns. It
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
	// Now is a clock seam for tests; nil uses time.Now.
	Now func() time.Time
}

// StaleTaskSweepResult aggregates the per-driver-run recovery results of one
// sweep pass.
type StaleTaskSweepResult struct {
	Recovered           int
	SkippedFresh        int
	ReconciledSessions  int
	RecoveredTaskRunIDs []string
}

// RunOnce performs a single sweep: list running DriverRuns in each target
// workspace and fail their TaskRuns whose heartbeat predates now-MaxAge.
func (s *StaleTaskSweeper) RunOnce(ctx context.Context) (*StaleTaskSweepResult, error) {
	if s == nil || s.Store == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	staleBefore := s.now().Add(-s.maxAge())
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
		result, reconciled, err := RecoverStaleTaskRunsAndSessions(ctx, s.Store, ws, run.RunID, store.StaleTaskRunRecovery{
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
		out.ReconciledSessions += reconciled
	}
	return nil
}

// RecoverStaleTaskRunsAndSessions is the shared stale-recovery primitive used
// by the background sweeper and the manual HTTP/CLI operations. Session
// settlement deliberately happens in the same pass as TaskRun recovery.
func RecoverStaleTaskRunsAndSessions(ctx context.Context, st store.Store, ws, driverRunID string, recovery store.StaleTaskRunRecovery) (*store.StaleTaskRunRecoveryResult, int, error) {
	if st == nil {
		return nil, 0, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	result, err := st.DriverRuns().RecoverStaleTaskRuns(ctx, ws, driverRunID, recovery)
	if err != nil {
		return nil, 0, err
	}
	return result, reconcileRecoveredTaskRunSessions(ctx, st, ws, result.RecoveredTaskRunIDs), nil
}

func reconcileRecoveredTaskRunSessions(ctx context.Context, st store.Store, ws string, taskRunIDs []string) int {
	reconciler := TaskRunSessionReconciler{Store: st}
	settled := 0
	for _, taskRunID := range taskRunIDs {
		run, err := st.TaskRuns().Get(ctx, ws, taskRunID)
		if err != nil {
			slog.WarnContext(ctx, "load stale task run for session reconciliation failed", "task_run_id", taskRunID, "err", err)
			continue
		}
		result, err := reconciler.ReconcileTerminalTaskRun(ctx, run, SessionFinalizedByStale, SessionFinalizedByStale)
		if err != nil {
			slog.WarnContext(ctx, "reconcile stale task-run sessions failed", "task_run_id", taskRunID, "err", err)
			continue
		}
		settled += result.Settled
	}
	return settled
}

// workspaceKeys resolves the sweep targets: the configured workspace, or
// every known workspace when unscoped (mirrors Executor.RecoverStaleOnce).
func (s *StaleTaskSweeper) workspaceKeys(ctx context.Context) ([]string, error) {
	return resolveSweepWorkspaces(ctx, s.Store, s.WorkspaceKey, "stale task sweep")
}

// resolveSweepWorkspaces returns the workspace targets for a background sweep
// loop: the single configured workspace, or every workspace when unconfigured.
// label names the loop in the list-error (e.g. "stale task sweep"). Shared by
// the stale-task / await-timeout / outbox background loops, which otherwise
// re-derived this identical configured-or-list-all logic.
func resolveSweepWorkspaces(ctx context.Context, s store.Store, configured, label string) ([]string, error) {
	if configured != "" {
		return []string{configured}, nil
	}
	workspaces, err := s.Workspaces().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workspaces for %s: %w", label, err)
	}
	keys := make([]string, 0, len(workspaces))
	for _, ws := range workspaces {
		if ws == nil {
			continue
		}
		keys = append(keys, ws.Key)
	}
	return keys, nil
}

func (s *StaleTaskSweeper) maxAge() time.Duration {
	if s.MaxAge > 0 {
		return s.MaxAge
	}
	return defaultStaleTaskRunMaxAge
}

func (s *StaleTaskSweeper) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}
