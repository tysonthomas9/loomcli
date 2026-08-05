package serve

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

func TestNewAwaitEventRuntimeRegistrationIsAlwaysOnExecutionComponent(t *testing.T) {
	st := memstore.New()
	capability, err := NewExecutionCapability(executionTestDependencies(t, st))
	if err != nil {
		t.Fatal(err)
	}
	registration, err := NewAwaitEventRuntimeRegistrationWithExecution(
		st.Awaits(), st.DriverRuns(), st.Workspaces(), "WS",
		capability.DriverRunAPI(), capability.AwaitEventNotificationAPI(), capability.SystemAuthorityResolver(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if registration.Component.ID() != AwaitEventNotificationComponentID ||
		!registration.Policy.Immediate || registration.Policy.Cadence != awaitEventReconcileCadence ||
		registration.Policy.Timeout != awaitEventReconcileTimeout {
		t.Fatalf("registration = %+v", registration)
	}
}
