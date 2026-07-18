package serve

import (
	"context"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/systemeventing"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
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

func NewRunOutcomeRuntimeRegistrationWithExecution(
	awaits store.AwaitStore,
	triggerEvents store.TriggerEventStore,
	workspacesStore store.WorkspaceStore,
	publisher driver.RunOutcomePublisher,
	workspace string,
	api execution.DriverRunAPI,
	queue execution.DriverRunOutcomeAPI,
	authorities execution.SystemAuthorityResolver,
) (platformruntime.Registration, error) {
	if api == nil || queue == nil || authorities == nil {
		return platformruntime.Registration{}, fmt.Errorf("compose driver run outcome runtime: Execution await and queue APIs are unavailable")
	}
	resolver := &driver.ExecutionAwaitResolver{
		API: api, Authorities: authorities, ComponentID: driver.RunOutcomeAwaitComponentID,
	}
	if awaits == nil || triggerEvents == nil || workspacesStore == nil {
		return platformruntime.Registration{}, fmt.Errorf("compose driver run outcome runtime: required dependency is unavailable")
	}
	journal, ok := triggerEvents.(store.TriggerEventAppender)
	if !ok {
		return platformruntime.Registration{}, fmt.Errorf("compose driver run outcome runtime: TriggerEvent store lacks base event journal capability")
	}
	notifier, err := driver.NewRunOutcomeAwaitNotifierWithResolver(awaits, resolver)
	if err != nil {
		return platformruntime.Registration{}, fmt.Errorf("compose driver run outcome runtime: %w", err)
	}
	workspaces := driver.RunOutcomeWorkspaceLister(newAutomationWorkspaceLister(workspacesStore))
	reconciler, err := driver.NewRunOutcomeReconcilerWithExecution(
		queue, notifier, journal, publisher, workspace, workspaces, api, authorities,
		string(execution.DriverRunOutcomeComponentID),
	)
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
