package serveadapter

import (
	"fmt"

	appserve "github.com/tysonthomas9/loomcli/internal/app/serve"
	"github.com/tysonthomas9/loomcli/internal/driver"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui"
)

// RuntimeContributor is the only lifecycle surface serve accepts from a
// capability. Registration is complete before Host.Start; neither the CLI nor
// web server learns a capability's command API.
type RuntimeContributor interface {
	RuntimeRegistrations() []platformruntime.Registration
}

type AutomationRuntimeContributor = RuntimeContributor

// BuildServeRuntimeHost composes the always-on Execution recovery components
// with optional Automation registrations behind one CLI adapter boundary.
func BuildServeRuntimeHost(
	driverRuns store.DriverRunStore,
	awaits store.AwaitStore,
	events store.TriggerEventStore,
	workspaces store.WorkspaceStore,
	runOutcomes driver.RunOutcomePublisher,
	workspace string,
	executionCapability webui.ExecutionCapability,
	execution RuntimeContributor,
	automation AutomationRuntimeContributor,
) (*platformruntime.Host, error) {
	if driverRuns == nil || awaits == nil || events == nil || workspaces == nil {
		return nil, fmt.Errorf("compose serve runtime host: required stores are unavailable")
	}
	if executionCapability == nil || executionCapability.DriverRunAPI() == nil ||
		executionCapability.AwaitEventNotificationAPI() == nil || executionCapability.DriverRunOutcomeAPI() == nil ||
		executionCapability.SystemAuthorityResolver() == nil {
		return nil, fmt.Errorf("compose serve runtime host: Execution await capability is unavailable")
	}
	runOutcomeRegistration, err := appserve.NewRunOutcomeRuntimeRegistrationWithExecution(
		awaits, events, workspaces, runOutcomes, workspace,
		executionCapability.DriverRunAPI(), executionCapability.DriverRunOutcomeAPI(), executionCapability.SystemAuthorityResolver(),
	)
	if err != nil {
		return nil, err
	}
	awaitEventRegistration, err := appserve.NewAwaitEventRuntimeRegistrationWithExecution(
		awaits, driverRuns, workspaces, workspace,
		executionCapability.DriverRunAPI(), executionCapability.AwaitEventNotificationAPI(), executionCapability.SystemAuthorityResolver(),
	)
	if err != nil {
		return nil, err
	}
	return BuildPlatformRuntimeHost(
		[]platformruntime.Registration{runOutcomeRegistration, awaitEventRegistration},
		execution, automation,
	)
}

// BuildPlatformRuntimeHost registers the required base platform component
// before any optional Automation contributors. The base registration exists
// independently of feature flags so durable composition recovery cannot be
// disabled with Workflow Catalog or Automation.
func BuildPlatformRuntimeHost(
	required []platformruntime.Registration,
	contributors ...RuntimeContributor,
) (*platformruntime.Host, error) {
	host := platformruntime.NewHost(platformruntime.Options{})
	if len(required) == 0 {
		return nil, fmt.Errorf("compose platform runtime host: no required components registered")
	}
	for _, registration := range required {
		if err := host.Register(registration); err != nil {
			return nil, fmt.Errorf("compose platform runtime host: %w", err)
		}
	}
	for _, contributor := range contributors {
		if contributor == nil {
			continue
		}
		registrations := contributor.RuntimeRegistrations()
		if len(registrations) == 0 {
			return nil, fmt.Errorf("compose Automation runtime host: no components registered")
		}
		for _, registration := range registrations {
			if err := host.Register(registration); err != nil {
				return nil, fmt.Errorf("compose Automation runtime host: %w", err)
			}
		}
	}
	return host, nil
}

// BuildAutomationRuntimeHost constructs an inert process runtime host and
// registers every Automation component. A disabled capability returns no host;
// an enabled capability with no registrations fails closed.
func BuildAutomationRuntimeHost(capability AutomationRuntimeContributor) (*platformruntime.Host, error) {
	if capability == nil {
		return nil, nil
	}
	registrations := capability.RuntimeRegistrations()
	if len(registrations) == 0 {
		return nil, fmt.Errorf("compose Automation runtime host: no components registered")
	}
	host := platformruntime.NewHost(platformruntime.Options{})
	for _, registration := range registrations {
		if err := host.Register(registration); err != nil {
			return nil, fmt.Errorf("compose Automation runtime host: %w", err)
		}
	}
	return host, nil
}
