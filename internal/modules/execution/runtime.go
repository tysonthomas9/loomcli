package execution

import (
	"context"
	"fmt"
	"time"

	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

const (
	DriverExecutorComponentID     platformruntime.ComponentID = "execution-driver-run"
	DriverRunOutcomeComponentID   platformruntime.ComponentID = "serve-driver-run-outcomes"
	AwaitTimeoutComponentID       platformruntime.ComponentID = "execution-await-timeout-recovery"
	TaskRunConvergenceComponentID platformruntime.ComponentID = "execution-task-run-convergence"
	OutboxDeliveryComponentID     platformruntime.ComponentID = "execution-outbox-delivery"
)

// RuntimePass is Execution's bounded unit of scheduled work. Platform Runtime
// owns repetition, cancellation, backoff, and health.
type RuntimePass interface {
	RunOnce(context.Context) error
}

type RuntimePassFunc func(context.Context) error

func (function RuntimePassFunc) RunOnce(ctx context.Context) error { return function(ctx) }

type RuntimeConfig struct {
	DriverExecutor     RuntimePass
	TaskWorkers        []RuntimePass
	AwaitTimeouts      RuntimePass
	TaskRunConvergence RuntimePass

	DriverCadence             time.Duration
	TaskWorkerCadence         time.Duration
	AwaitTimeoutCadence       time.Duration
	TaskRunConvergenceCadence time.Duration
}

type runtimeComponent struct {
	id   platformruntime.ComponentID
	pass RuntimePass
}

func (component *runtimeComponent) ID() platformruntime.ComponentID { return component.id }

func (component *runtimeComponent) RunOnce(ctx context.Context, _ time.Time) error {
	if component == nil || component.pass == nil {
		return ErrUnavailable
	}
	return component.pass.RunOnce(ctx)
}

// RuntimeRegistrations translates Execution-owned reconciliation passes into
// inert registrations. It never launches goroutines.
func RuntimeRegistrations(config RuntimeConfig) ([]platformruntime.Registration, error) {
	config = defaultRuntimeCadences(config)
	registrations := make([]platformruntime.Registration, 0, 3+len(config.TaskWorkers))
	add := func(id platformruntime.ComponentID, pass RuntimePass, cadence time.Duration) {
		if pass == nil {
			return
		}
		registrations = append(registrations, platformruntime.Registration{
			Component: &runtimeComponent{id: id, pass: pass},
			Policy: platformruntime.Policy{
				// A claim pass may own a real provider process for minutes. Host
				// cancellation bounds it; cadence must never become a run timeout.
				Cadence: cadence, Immediate: true,
				FailureBackoff: platformruntime.Backoff{Initial: time.Second, Max: time.Minute, Multiplier: 2},
			},
		})
	}
	add(DriverExecutorComponentID, config.DriverExecutor, config.DriverCadence)
	for index, worker := range config.TaskWorkers {
		add(platformruntime.ComponentID(fmt.Sprintf("execution-task-run-worker-%d", index+1)), worker, config.TaskWorkerCadence)
	}
	add(AwaitTimeoutComponentID, config.AwaitTimeouts, config.AwaitTimeoutCadence)
	add(TaskRunConvergenceComponentID, config.TaskRunConvergence, config.TaskRunConvergenceCadence)
	if len(registrations) == 0 {
		return nil, fmt.Errorf("%w: no execution runtime passes", ErrUnavailable)
	}
	return registrations, nil
}

func defaultRuntimeCadences(config RuntimeConfig) RuntimeConfig {
	if config.DriverCadence <= 0 {
		config.DriverCadence = 2 * time.Second
	}
	if config.TaskWorkerCadence <= 0 {
		config.TaskWorkerCadence = time.Second
	}
	if config.AwaitTimeoutCadence <= 0 {
		config.AwaitTimeoutCadence = 30 * time.Second
	}
	if config.TaskRunConvergenceCadence <= 0 {
		config.TaskRunConvergenceCadence = 2 * time.Second
	}
	return config
}
