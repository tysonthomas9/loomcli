package serveadapter

import (
	"fmt"

	appserve "github.com/tysonthomas9/loomcli/internal/app/serve"
	"github.com/tysonthomas9/loomcli/internal/driver"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// AutomationRuntimeContributor is the only lifecycle surface serve accepts
// from Automation composition. Registration is complete before Host.Start;
// neither the CLI nor web server learns the Automation command API.
type AutomationRuntimeContributor interface {
	RuntimeRegistrations() []platformruntime.Registration
}

// BuildServeRuntimeHost composes the always-on Execution recovery components
// with optional Automation registrations behind one CLI adapter boundary.
func BuildServeRuntimeHost(
	driverRuns store.DriverRunStore,
	awaits store.AwaitStore,
	events store.TriggerEventStore,
	workspaces store.WorkspaceStore,
	runOutcomes driver.RunOutcomePublisher,
	workspace string,
	automation AutomationRuntimeContributor,
) (*platformruntime.Host, error) {
	if driverRuns == nil || awaits == nil || events == nil || workspaces == nil {
		return nil, fmt.Errorf("compose serve runtime host: required stores are unavailable")
	}
	runOutcomeRegistration, err := appserve.NewRunOutcomeRuntimeRegistration(
		driverRuns, awaits, events, workspaces, runOutcomes, workspace,
	)
	if err != nil {
		return nil, err
	}
	awaitEventRegistration, err := appserve.NewAwaitEventRuntimeRegistration(
		events, awaits, driverRuns, workspaces, workspace,
	)
	if err != nil {
		return nil, err
	}
	return BuildPlatformRuntimeHost(
		[]platformruntime.Registration{runOutcomeRegistration, awaitEventRegistration},
		automation,
	)
}

// BuildPlatformRuntimeHost registers the required base platform component
// before any optional Automation contributors. The base registration exists
// independently of feature flags so durable composition recovery cannot be
// disabled with Workflow Catalog or Automation.
func BuildPlatformRuntimeHost(
	required []platformruntime.Registration,
	contributors ...AutomationRuntimeContributor,
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
