package serveadapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	driverexecutor "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
	"github.com/tysonthomas9/loomcli/internal/webui"
)

// ExecutionRuntimeContributor is inert composition data. Platform Runtime is
// the only owner of goroutines, cadence, cancellation, and backoff.
type ExecutionRuntimeContributor struct {
	registrations []platformruntime.Registration
}

// RuntimeRegistrations returns an isolated copy of the composed registrations.
func (contributor *ExecutionRuntimeContributor) RuntimeRegistrations() []platformruntime.Registration {
	if contributor == nil {
		return nil
	}
	return append([]platformruntime.Registration(nil), contributor.registrations...)
}

// ExecutionRuntimePasses contains already-composed compatibility passes. The
// broad legacy Store remains confined to the CLI composition root; this
// Execution runtime surface receives only owner API passes.
type ExecutionRuntimePasses struct {
	DriverExecutor execution.RuntimePass
	TaskWorkers    []execution.RuntimePass
	AwaitTimeouts  execution.RuntimePass
}

// NewAwaitTimeoutExecutionResolver supplies the exact Execution-owned atomic
// mutation port used by the legacy sweeper. Composite Store construction stays
// in the already-reviewed root CLI seam.
func NewAwaitTimeoutExecutionResolver(capability webui.ExecutionCapability) *driverexecutor.ExecutionAwaitResolver {
	if capability == nil {
		return nil
	}
	return &driverexecutor.ExecutionAwaitResolver{
		API: capability.DriverRunAPI(), Authorities: capability.SystemAuthorityResolver(),
		ComponentID: string(execution.AwaitTimeoutComponentID),
	}
}

// BuildAwaitTimeoutRuntimePass adapts an already-composed legacy await sweep
// to an inert Execution runtime pass.
func BuildAwaitTimeoutRuntimePass(
	runOnce func(context.Context) (*driverexecutor.AwaitTimeoutSweepResult, error),
) execution.RuntimePass {
	return execution.RuntimePassFunc(func(ctx context.Context) error {
		if runOnce == nil {
			return fmt.Errorf("await timeout sweep is unavailable")
		}
		result, err := runOnce(ctx)
		if err != nil {
			return err
		}
		if result != nil && result.TimedOut+result.ResumeDeferred > 0 {
			slog.Info("Execution resolved due awaits", "timed_out", result.TimedOut,
				"resume_deferred", result.ResumeDeferred, "instance_keys", result.TimedOutInstanceKeys)
		}
		return nil
	})
}

// BuildDriverExecutorRuntimePass adapts the legacy DriverRun executor to an
// inert Execution runtime pass.
func BuildDriverExecutorRuntimePass(executor *driverexecutor.Executor) execution.RuntimePass {
	return execution.RuntimePassFunc(func(ctx context.Context) error {
		if recovered, err := executor.RecoverStaleOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("recover stale DriverRuns: %w", err)
		} else if recovered != nil && recovered.Recovered > 0 {
			slog.Info("Execution recovered stale driver runs", "count", recovered.Recovered)
		}
		_, err := executor.RunOnce(ctx)
		if errors.Is(err, driverexecutor.ErrNoQueuedRun) || errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	})
}

// BuildTaskWorkerRuntimePasses adapts the requested number of legacy TaskRun
// workers to inert Execution runtime passes.
func BuildTaskWorkerRuntimePasses(template driverexecutor.TaskWorker, concurrency int) []execution.RuntimePass {
	passes := make([]execution.RuntimePass, 0, concurrency)
	for index := 0; index < concurrency; index++ {
		worker := template.CloneForRuntime()
		worker.ExecutionComponentID = fmt.Sprintf("execution-task-run-worker-%d", index+1)
		if worker.RunnerID == "" {
			worker.RunnerID = fmt.Sprintf("loom-serve-task-worker-%d", index+1)
		}
		passes = append(passes, execution.RuntimePassFunc(func(ctx context.Context) error {
			_, err := worker.RunOnce(ctx)
			if errors.Is(err, driverexecutor.ErrNoQueuedTaskRun) || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}))
	}
	return passes
}

// BuildExecutionRuntimeContributor validates owner APIs and composes every
// Execution registration without exposing platform runtime types to the root
// CLI package.
func BuildExecutionRuntimeContributor(
	passes ExecutionRuntimePasses,
	executionCapability webui.ExecutionCapability,
	workspace string,
	awaitTimeoutCadence time.Duration,
) (*ExecutionRuntimeContributor, error) {
	if executionCapability == nil || executionCapability.DriverRunAPI() == nil ||
		executionCapability.DriverRunOutcomeAPI() == nil ||
		executionCapability.DriverRunAuthorityResolver() == nil || executionCapability.SystemAuthorityResolver() == nil ||
		executionCapability.TaskRunRecoveryScopes() == nil ||
		executionCapability.TaskRunConvergenceAPI() == nil || executionCapability.TaskRunConvergenceSource() == nil {
		return nil, fmt.Errorf("compose Execution runtime: DriverRun and TaskRun convergence APIs and authority resolvers are required")
	}
	if passes.AwaitTimeouts == nil {
		return nil, fmt.Errorf("compose Execution runtime: await timeout pass is required")
	}
	convergencePass := &execution.TaskRunConvergencePass{
		WorkspaceKey: workspace, Scopes: executionCapability.TaskRunRecoveryScopes(),
		Source: executionCapability.TaskRunConvergenceSource(), API: executionCapability.TaskRunConvergenceAPI(),
		Authorities: executionCapability.SystemAuthorityResolver(),
	}
	config := execution.RuntimeConfig{
		DriverExecutor: passes.DriverExecutor, TaskWorkers: append([]execution.RuntimePass(nil), passes.TaskWorkers...),
		AwaitTimeouts: passes.AwaitTimeouts, TaskRunConvergence: convergencePass,
		AwaitTimeoutCadence: awaitTimeoutCadence,
	}
	registrations, err := execution.RuntimeRegistrations(config)
	if err != nil {
		return nil, fmt.Errorf("compose Execution runtime: %w", err)
	}
	return &ExecutionRuntimeContributor{registrations: registrations}, nil
}
