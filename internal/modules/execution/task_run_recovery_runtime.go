package execution

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const (
	// DefaultStaleTaskRunMaxAge protects legitimately long task executions while
	// still converging workers that disappear without releasing their lease.
	// Deployments with only short local tasks may select a tighter host value.
	DefaultStaleTaskRunMaxAge = 20 * time.Minute

	staleTaskRunErrorClass   = "stale_task_run"
	staleTaskRunErrorMessage = "task run heartbeat is stale"
)

var staleTaskRunRecoveryClockOrigin = time.Now()

// OwnedStaleTaskRunRecovery is the healthy-parent recovery pass for one exact
// DriverRun generation. The raw parent lease token remains in the executor's
// in-memory owner envelope and is sent only through the owner-fenced port.
// Crashed parents are handled by DriverRun recovery and its child cascade; this
// pass deliberately cannot become a tokenless global TaskRun sweep.
type OwnedStaleTaskRunRecovery struct {
	API         TaskRunRecoveryAPI
	Authorities DriverRunAuthorityResolver

	WorkspaceKey string
	ParentOwner  Owner
	MaxAge       time.Duration

	// Now and MonotonicNow are deterministic clock seams. ClockOrigin is the
	// wall time corresponding to MonotonicNow()==0. Production leaves all three
	// unset and uses the process-local monotonic clock origin.
	Now          func() time.Time
	MonotonicNow func() time.Duration
	ClockOrigin  time.Time

	clockMu          sync.Mutex
	clockInitialized bool
	lastMonotonic    time.Duration
	logicalNow       time.Time
}

// RunOnce recovers stale TaskRuns owned by the exact live parent generation.
func (recovery *OwnedStaleTaskRunRecovery) RunOnce(ctx context.Context) (RecoverStaleTaskRunsResult, error) {
	if recovery == nil || recovery.API == nil || recovery.Authorities == nil {
		return RecoverStaleTaskRunsResult{}, fmt.Errorf("owned stale TaskRun recovery dependencies are required: %w", ErrUnavailable)
	}
	owner := recovery.ParentOwner
	if recovery.WorkspaceKey == "" || owner.ResourceKind != ResourceDriverRun || owner.ResourceID == "" ||
		owner.NodeID == "" || owner.LeaseID == "" || owner.LeaseToken == "" || owner.FencingToken <= 0 {
		return RecoverStaleTaskRunsResult{}, fmt.Errorf("owned stale TaskRun recovery parent is invalid: %w", ErrInvalid)
	}
	observedAt := recovery.recoveryNow()
	staleBefore := observedAt.Add(-recovery.maxAge())
	auth, err := recovery.Authorities.ResolveDriverRunAuthority(
		ctx, recovery.WorkspaceKey, ActionRecoverStaleChildTaskRuns, owner,
	)
	if err != nil {
		return RecoverStaleTaskRunsResult{}, fmt.Errorf("resolve stale child TaskRun recovery authority: %w", err)
	}
	result, err := recovery.API.RecoverStaleChildTaskRuns(ctx, auth, RecoverStaleChildTaskRunsCommand{
		WorkspaceKey: recovery.WorkspaceKey,
		RequestID:    RecoverStaleChildTaskRunsRequestID(owner.ResourceID, staleBefore),
		ParentOwner:  owner,
		DriverRunID:  owner.ResourceID,
		StaleBefore:  staleBefore,
		ErrorClass:   staleTaskRunErrorClass,
		ErrorMessage: staleTaskRunErrorMessage,
		ObservedAt:   observedAt,
	})
	if err != nil {
		return RecoverStaleTaskRunsResult{}, err
	}
	return result, nil
}

func (recovery *OwnedStaleTaskRunRecovery) maxAge() time.Duration {
	if recovery.MaxAge > 0 {
		return recovery.MaxAge
	}
	return DefaultStaleTaskRunMaxAge
}

// recoveryNow returns the earlier of a monotonic projection and the live wall
// clock. Forward wall-clock jumps therefore cannot mass-age TaskRuns, while a
// backward jump conservatively protects heartbeats written in the new epoch.
func (recovery *OwnedStaleTaskRunRecovery) recoveryNow() time.Time {
	recovery.clockMu.Lock()
	defer recovery.clockMu.Unlock()

	monotonic := recovery.monotonicNow()
	wall := recovery.wallNow()
	if !recovery.clockInitialized {
		recovery.clockInitialized = true
		recovery.lastMonotonic = monotonic
		recovery.logicalNow = recovery.initialLogicalNow(wall, monotonic)
		if wall.Before(recovery.logicalNow) {
			return wall
		}
		return recovery.logicalNow
	}

	elapsed := monotonic - recovery.lastMonotonic
	if elapsed < 0 {
		elapsed = 0
	}
	recovery.lastMonotonic = monotonic
	recovery.logicalNow = recovery.logicalNow.Add(elapsed)
	if wall.Before(recovery.logicalNow) {
		return wall
	}
	return recovery.logicalNow
}

func (recovery *OwnedStaleTaskRunRecovery) wallNow() time.Time {
	if recovery.Now != nil {
		return recovery.Now().UTC()
	}
	return time.Now().UTC()
}

func (recovery *OwnedStaleTaskRunRecovery) monotonicNow() time.Duration {
	if recovery.MonotonicNow != nil {
		return recovery.MonotonicNow()
	}
	return time.Since(staleTaskRunRecoveryClockOrigin)
}

func (recovery *OwnedStaleTaskRunRecovery) initialLogicalNow(wall time.Time, monotonic time.Duration) time.Time {
	if !recovery.ClockOrigin.IsZero() {
		return recovery.ClockOrigin.UTC().Add(monotonic)
	}
	if recovery.Now == nil && recovery.MonotonicNow == nil {
		return staleTaskRunRecoveryClockOrigin.Add(monotonic).UTC()
	}
	return wall
}
