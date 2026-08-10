package driver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

func (e *Executor) startHeartbeats(ctx context.Context, claimed *domain.DriverRun, nodeID, leaseToken string, cancelRun context.CancelFunc) (context.CancelFunc, <-chan struct{}) {
	hbCtx, stopHeartbeat := context.WithCancel(ctx)
	interval := e.heartbeatInterval()
	if interval <= 0 {
		return stopHeartbeat, nil
	}
	ownerHeartbeatDone := make(chan struct{})
	staleRecovery := e.ownedStaleTaskRunRecovery(claimed, leaseToken)
	go heartbeatDriverRun(hbCtx, e, claimed, leaseToken, interval, cancelRun, staleRecovery, ownerHeartbeatDone)
	go heartbeatExecutorNode(hbCtx, e, claimed.WorkspaceKey, nodeID, e.nodeTTL())
	return stopHeartbeat, ownerHeartbeatDone
}

// staleTaskRunRecoveryInterval keeps liveness heartbeats cheap while bounding
// stale-child convergence to one additional quarter of the selected stale
// threshold. It never polls faster than the owner heartbeat that fences it.
func (e *Executor) staleTaskRunRecoveryInterval() time.Duration {
	maxAge := e.StaleTaskRunMaxAge
	if maxAge <= 0 {
		maxAge = execution.DefaultStaleTaskRunMaxAge
	}
	interval := maxAge / 4
	heartbeat := e.heartbeatInterval()
	if interval < heartbeat {
		return heartbeat
	}
	return interval
}

// heartbeatDriverRun renews the run lease and observes cooperative cancel
// requests (composition cascade, AW10): a heartbeat that comes back with
// CancelRequestedAt set cancels the runner's context so the run terminalizes
// as cancelled through the normal finish. Losing the parent fence during
// stale-child recovery uses the same cancellation path.
func heartbeatDriverRun(
	ctx context.Context,
	executor *Executor,
	claimed *domain.DriverRun,
	leaseToken string,
	interval time.Duration,
	cancelRun context.CancelFunc,
	staleRecovery *execution.OwnedStaleTaskRunRecovery,
	done chan<- struct{},
) {
	if done != nil {
		defer close(done)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	recoveryInterval := executor.staleTaskRunRecoveryInterval()
	// The claim itself proves the parent owner before this goroutine starts, so
	// the immediate pass can recover children orphaned before the next cadence.
	if staleRecovery != nil && !runOwnedStaleTaskRunRecovery(ctx, staleRecovery, cancelRun) {
		return
	}
	nextRecoveryAt := time.Now().Add(recoveryInterval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run, err := executor.heartbeatDriverRun(ctx, claimed, leaseToken)
			if err != nil {
				if errors.Is(err, execution.ErrFenceConflict) {
					if cancelRun != nil {
						cancelRun()
					}
					slog.InfoContext(ctx, "driver runner cancelled after parent heartbeat fence conflict", "err", err)
					return
				}
				continue
			}
			if run != nil && run.CancelRequestedAt != nil && cancelRun != nil {
				cancelRun()
			}
			// Recovery has its own cadence: heartbeat frequency proves ownership but
			// must not create a durable no-op recovery receipt every few seconds.
			// Every recovery pass still follows a successful parent heartbeat.
			if staleRecovery != nil && !time.Now().Before(nextRecoveryAt) {
				if !runOwnedStaleTaskRunRecovery(ctx, staleRecovery, cancelRun) {
					return
				}
				nextRecoveryAt = time.Now().Add(recoveryInterval)
			}
		}
	}
}

func (e *Executor) ownedStaleTaskRunRecovery(claimed *domain.DriverRun, leaseToken string) *execution.OwnedStaleTaskRunRecovery {
	if e == nil || e.TaskRunRecovery == nil || e.ExecutionAuthorities == nil || claimed == nil {
		return nil
	}
	return &execution.OwnedStaleTaskRunRecovery{
		API: e.TaskRunRecovery, Authorities: e.ExecutionAuthorities,
		WorkspaceKey: claimed.WorkspaceKey,
		ParentOwner:  executionOwnerFromLegacyDriverRun(claimed, leaseToken),
		MaxAge:       e.StaleTaskRunMaxAge,
	}
}

func runOwnedStaleTaskRunRecovery(ctx context.Context, recovery *execution.OwnedStaleTaskRunRecovery, cancelRun context.CancelFunc) bool {
	result, err := recovery.RunOnce(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false
		}
		if errors.Is(err, execution.ErrFenceConflict) {
			// A fence conflict proves this executor no longer owns the parent
			// generation. Stop the backend as well as the heartbeat loop so a
			// superseded runner cannot keep mutating its checkout.
			if cancelRun != nil {
				cancelRun()
			}
			slog.InfoContext(ctx, "stale child TaskRun recovery stopped after parent fence conflict", "err", err)
			return false
		}
		slog.WarnContext(ctx, "stale child TaskRun recovery failed", "err", err)
		return true
	}
	if result.Recovered > 0 {
		slog.InfoContext(ctx, "recovered stale child TaskRuns", "workspace", result.WorkspaceKey,
			"count", result.Recovered, "released", result.Released, "task_run_ids", result.RecoveredTaskRunIDs)
	}
	return true
}

// resolveSweepWorkspaces is the shared read-only workspace projection used by
// background reconcilers. It performs no Execution aggregate mutation.
func resolveSweepWorkspaces(ctx context.Context, source workspaceReadStore, configured, label string) ([]string, error) {
	if configured != "" {
		return []string{configured}, nil
	}
	workspaces, err := source.Workspaces().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workspaces for %s: %w", label, err)
	}
	keys := make([]string, 0, len(workspaces))
	for _, workspace := range workspaces {
		if workspace != nil {
			keys = append(keys, workspace.Key)
		}
	}
	return keys, nil
}
