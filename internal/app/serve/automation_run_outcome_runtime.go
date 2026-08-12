package serve

import (
	"context"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/systemeventing"
	"github.com/tysonthomas9/loomcli/internal/driver"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	runOutcomeReconcileCadence = 5 * time.Second
	runOutcomeReconcileTimeout = 30 * time.Second
)

type runOutcomeRuntimeComponent struct {
	reconciler *driver.RunOutcomeReconciler
}

var _ platformruntime.Component = (*runOutcomeRuntimeComponent)(nil)

func (*runOutcomeRuntimeComponent) ID() platformruntime.ComponentID {
	return platformruntime.ComponentID(systemeventing.DriverRunOutcomeComponentID)
}

func (component *runOutcomeRuntimeComponent) RunOnce(ctx context.Context, now time.Time) error {
	if component == nil || component.reconciler == nil {
		return fmt.Errorf("driver run outcome reconciler is unavailable")
	}
	return component.reconciler.RunOnce(ctx, now)
}

// NewRunOutcomeRuntimeRegistration composes Execution's durable terminal
// outcome consumer as a base platform component. Automation publication is an
// optional second leg; composition-await recovery remains registered when
// Automation and Workflow Catalog are disabled.
func NewRunOutcomeRuntimeRegistration(
	driverRuns store.DriverRunStore,
	awaits store.AwaitStore,
	triggerEvents store.TriggerEventStore,
	workspacesStore store.WorkspaceStore,
	publisher driver.RunOutcomePublisher,
	workspace string,
) (platformruntime.Registration, error) {
	if driverRuns == nil || awaits == nil || triggerEvents == nil {
		return platformruntime.Registration{}, fmt.Errorf("compose driver run outcome runtime: required dependency is unavailable")
	}
	outbox, ok := driverRuns.(store.DriverRunOutcomeStore)
	if !ok {
		return platformruntime.Registration{}, fmt.Errorf("compose driver run outcome runtime: DriverRun store lacks durable outcome capability")
	}
	journal, ok := triggerEvents.(store.TriggerEventAppender)
	if !ok {
		return platformruntime.Registration{}, fmt.Errorf("compose driver run outcome runtime: TriggerEvent store lacks base event journal capability")
	}
	notifier, err := driver.NewRunOutcomeAwaitNotifier(awaits)
	if err != nil {
		return platformruntime.Registration{}, fmt.Errorf("compose driver run outcome runtime: %w", err)
	}
	workspaces := driver.RunOutcomeWorkspaceLister(newAutomationWorkspaceLister(workspacesStore))
	reconciler, err := driver.NewRunOutcomeReconciler(outbox, notifier, journal, publisher, workspace, workspaces)
	if err != nil {
		return platformruntime.Registration{}, fmt.Errorf("compose driver run outcome runtime: %w", err)
	}
	return platformruntime.Registration{
		Component: &runOutcomeRuntimeComponent{reconciler: reconciler},
		Policy: platformruntime.Policy{
			Cadence: runOutcomeReconcileCadence, Immediate: true, Timeout: runOutcomeReconcileTimeout,
			FailureBackoff: platformruntime.Backoff{Initial: time.Second, Max: time.Minute, Multiplier: 2},
		},
	}, nil
}
