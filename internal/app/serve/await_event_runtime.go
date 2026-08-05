package serve

import (
	"context"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	AwaitEventNotificationComponentID platformruntime.ComponentID = "serve-await-event-notifications"
	awaitEventReconcileCadence                                    = 5 * time.Second
	awaitEventReconcileTimeout                                    = 30 * time.Second
)

type awaitEventRuntimeComponent struct {
	reconciler *driver.AwaitEventReconciler
}

var _ platformruntime.Component = (*awaitEventRuntimeComponent)(nil)

func (*awaitEventRuntimeComponent) ID() platformruntime.ComponentID {
	return AwaitEventNotificationComponentID
}

func (component *awaitEventRuntimeComponent) RunOnce(ctx context.Context, now time.Time) error {
	if component == nil || component.reconciler == nil {
		return fmt.Errorf("await event reconciler is unavailable")
	}
	return component.reconciler.RunOnce(ctx, now)
}

func NewAwaitEventRuntimeRegistrationWithExecution(
	awaits store.AwaitStore,
	driverRuns store.DriverRunStore,
	workspacesStore store.WorkspaceStore,
	workspace string,
	api execution.DriverRunAPI,
	queue execution.AwaitEventNotificationAPI,
	authorities execution.SystemAuthorityResolver,
) (platformruntime.Registration, error) {
	if api == nil || queue == nil || authorities == nil {
		return platformruntime.Registration{}, fmt.Errorf("compose await event runtime: Execution await and queue APIs are unavailable")
	}
	resolver := &driver.ExecutionAwaitResolver{
		API: api, Authorities: authorities, ComponentID: string(AwaitEventNotificationComponentID),
	}
	if awaits == nil || driverRuns == nil || workspacesStore == nil {
		return platformruntime.Registration{}, fmt.Errorf("compose await event runtime: required dependency is unavailable")
	}
	reconciler, err := driver.NewAwaitEventReconcilerWithExecutionStores(
		queue, authorities, awaits, driverRuns, resolver,
		workspace, driver.RunOutcomeWorkspaceLister(newAutomationWorkspaceLister(workspacesStore)),
		string(AwaitEventNotificationComponentID),
	)
	if err != nil {
		return platformruntime.Registration{}, fmt.Errorf("compose await event runtime: %w", err)
	}
	return platformruntime.Registration{
		Component: &awaitEventRuntimeComponent{reconciler: reconciler},
		Policy: platformruntime.Policy{
			Cadence: awaitEventReconcileCadence, Immediate: true, Timeout: awaitEventReconcileTimeout,
			FailureBackoff: platformruntime.Backoff{Initial: time.Second, Max: time.Minute, Multiplier: 2},
		},
	}, nil
}
