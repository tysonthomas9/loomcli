package driver

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func (e HostBridgeTaskExecutor) recordTaskCopy(ctx context.Context, req TaskExecRequest, worktree TaskWorktree) error {
	if e.Store == nil || req.TaskRunID == "" {
		return nil
	}
	metadata := withTaskWorktreeMetadata(TaskExecResult{}, worktree).RuntimeMetadata
	metadata["retained_path"] = worktree.Path
	metadata["patch_back_status"] = "retained"
	_, err := e.Store.TaskRuns().Heartbeat(ctx, req.WorkspaceKey, req.TaskRunID, store.TaskRunHeartbeat{
		NodeID: req.NodeID, LeaseID: req.LeaseID, LeaseToken: req.LeaseToken,
		FencingToken: req.FencingToken, RuntimeMetadata: metadata,
	})
	if err != nil {
		return fmt.Errorf("record task copy before execution: %w", err)
	}
	return nil
}

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
	RecoveredTaskRunIDs []string
}

// RunOnce performs a single sweep of running TaskRuns in each target workspace.
// A driver run may already be terminal when its task copy needs capture.
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
	tasks, err := s.Store.TaskRuns().List(ctx, ws, store.TaskRunFilter{Status: domain.TaskRunRunning})
	if err != nil {
		return fmt.Errorf("list running task runs in workspace %q: %w", ws, err)
	}
	runIDs := make(map[string][]*domain.TaskRun)
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if task.DriverRunID == "" {
			if task.RuntimeMetadata["task_copy_path"] != "" && task.LastHeartbeat.Before(staleBefore) {
				return loomgit.NewError(loomgit.AttentionRequired, "stale task copy has no driver run", nil)
			}
			continue
		}
		runIDs[task.DriverRunID] = append(runIDs[task.DriverRunID], task)
	}
	ids := make([]string, 0, len(runIDs))
	for id := range runIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := captureStaleTaskCopies(ctx, runIDs[id], staleBefore); err != nil {
			return err
		}
		result, err := s.Store.DriverRuns().RecoverStaleTaskRuns(ctx, ws, id, store.StaleTaskRunRecovery{
			StaleBefore:  staleBefore,
			ErrorClass:   staleTaskRunErrorClass,
			ErrorMessage: staleTaskRunErrorMessage,
		})
		if err != nil {
			return fmt.Errorf("recover stale task runs for driver run %q: %w", id, err)
		}
		if result.Recovered > 0 {
			slog.Info("stale task ownership recovery", "driver_run_id", id,
				"task_run_ids", result.RecoveredTaskRunIDs, "released", result.Released,
				"released_task_ids", result.ReleasedTaskIDs)
			if result.Released == 0 {
				slog.Warn("stale task run recovered without task ownership release",
					"driver_run_id", id, "task_run_ids", result.RecoveredTaskRunIDs)
			}
		}
		out.Recovered += result.Recovered
		out.SkippedFresh += result.SkippedFresh
		out.RecoveredTaskRunIDs = append(out.RecoveredTaskRunIDs, result.RecoveredTaskRunIDs...)
	}
	return nil
}

func captureStaleTaskCopies(ctx context.Context, tasks []*domain.TaskRun, staleBefore time.Time) error {
	for _, task := range tasks {
		if task == nil || !task.LastHeartbeat.Before(staleBefore) || task.RuntimeMetadata["task_copy_path"] == "" {
			continue
		}
		if err := captureStaleTaskCopy(ctx, task); err != nil {
			return fmt.Errorf("task run %q: %w", task.TaskRunID, err)
		}
	}
	return nil
}

func captureStaleTaskCopy(ctx context.Context, task *domain.TaskRun) error {
	meta := task.RuntimeMetadata
	path, attempt, base := meta["task_copy_path"], meta["attempt_id"], meta["attempt_base_sha"]
	repo, source := meta["repo_name"], meta["source_repo_path"]
	if path == "" || attempt == "" || base == "" || repo == "" || source == "" || task.TaskID == "" {
		return loomgit.NewError(loomgit.AttentionRequired, "task copy has incomplete recovery metadata", nil)
	}
	captured, err := agentcapture.Capture(ctx, path, task.WorkspaceKey, attempt, task.TaskID, task.TaskID)
	if err != nil {
		return loomgit.NewError(loomgit.AttentionRequired, "capture stale task copy", err)
	}
	_, err = driverfreeze.FreezeCapture(ctx, driverfreeze.CaptureRequest{
		Workspace: task.WorkspaceKey, Task: task.TaskID, Repo: repo, Attempt: attempt,
		Worktree: path, Base: base, CaptureSHA: captured.SHA, SourceRepo: source,
		Outcome: "failed", Complete: captured.Complete,
	})
	if err != nil {
		return loomgit.NewError(loomgit.AttentionRequired, "freeze stale task copy", err)
	}
	return nil
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
