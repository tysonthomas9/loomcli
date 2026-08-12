package serve

import (
	"context"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/driver"
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

// NewAwaitEventRuntimeRegistration composes Execution's durable consumer for
// every admitted trigger event. It is a required base-platform registration:
// Automation feature flags affect event producers, never the await recovery
// guarantee.
func NewAwaitEventRuntimeRegistration(
	events store.TriggerEventStore,
	awaits store.AwaitStore,
	driverRuns store.DriverRunStore,
	workspacesStore store.WorkspaceStore,
	workspace string,
) (platformruntime.Registration, error) {
	if events == nil || awaits == nil || driverRuns == nil || workspacesStore == nil {
		return platformruntime.Registration{}, fmt.Errorf("compose await event runtime: required dependency is unavailable")
	}
	outbox, ok := events.(store.AwaitEventNotificationStore)
	if !ok {
		return platformruntime.Registration{}, fmt.Errorf("compose await event runtime: TriggerEvent store lacks durable notification capability")
	}
	reconciler, err := driver.NewAwaitEventReconcilerFromStores(
		outbox, awaits, driverRuns,
		workspace,
		driver.RunOutcomeWorkspaceLister(newAutomationWorkspaceLister(workspacesStore)),
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
