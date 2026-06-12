package driver

import (
	"context"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// defaultStaleTaskRunMaxAge is how old a running TaskRun's heartbeat may be
// before the sweeper fails it, when no MaxAge is configured.
const defaultStaleTaskRunMaxAge = 5 * time.Minute

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
	// back to defaultStaleTaskRunMaxAge (300s); override via the
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
	if s.WorkspaceKey != "" {
		return []string{s.WorkspaceKey}, nil
	}
	workspaces, err := s.Store.Workspaces().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workspaces for stale task sweep: %w", err)
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
