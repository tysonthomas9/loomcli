package serveadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

type runtimeContributorStub struct {
	registrations []platformruntime.Registration
}

func (stub runtimeContributorStub) RuntimeRegistrations() []platformruntime.Registration {
	return stub.registrations
}

type runtimeComponentStub struct {
	id platformruntime.ComponentID
}

func (component runtimeComponentStub) ID() platformruntime.ComponentID { return component.id }
func (runtimeComponentStub) RunOnce(context.Context, time.Time) error  { return nil }

func TestBuildAutomationRuntimeHostRegistersBeforeStart(t *testing.T) {
	if host, err := BuildAutomationRuntimeHost(nil); err != nil || host != nil {
		t.Fatalf("disabled host = %v, %v", host, err)
	}
	if _, err := BuildAutomationRuntimeHost(runtimeContributorStub{}); err == nil {
		t.Fatal("empty enabled contributor did not fail closed")
	}

	policy := platformruntime.Policy{Cadence: time.Minute}
	host, err := BuildAutomationRuntimeHost(runtimeContributorStub{registrations: []platformruntime.Registration{
		{Component: runtimeComponentStub{id: "automation-cron"}, Policy: policy},
		{Component: runtimeComponentStub{id: "automation-retry"}, Policy: policy},
	}})
	if err != nil {
		t.Fatalf("BuildAutomationRuntimeHost: %v", err)
	}
	snapshot := host.Snapshot()
	if snapshot.Status != platformruntime.HostCreated || len(snapshot.Components) != 2 {
		t.Fatalf("inert snapshot = %+v", snapshot)
	}
	if snapshot.Components[0].ID != "automation-cron" || snapshot.Components[1].ID != "automation-retry" {
		t.Fatalf("registered components = %+v", snapshot.Components)
	}
	if err := host.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := host.Register(platformruntime.Registration{Component: runtimeComponentStub{id: "late"}, Policy: policy}); !errors.Is(err, platformruntime.ErrHostStarted) {
		t.Fatalf("late Register error = %v, want %v", err, platformruntime.ErrHostStarted)
	}
	if err := host.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestBuildAutomationRuntimeHostRejectsDuplicateComponent(t *testing.T) {
	registration := platformruntime.Registration{
		Component: runtimeComponentStub{id: "duplicate"},
		Policy:    platformruntime.Policy{Cadence: time.Minute},
	}
	_, err := BuildAutomationRuntimeHost(runtimeContributorStub{registrations: []platformruntime.Registration{registration, registration}})
	if !errors.Is(err, platformruntime.ErrDuplicateComponent) {
		t.Fatalf("duplicate error = %v, want %v", err, platformruntime.ErrDuplicateComponent)
	}
}

func TestBuildPlatformRuntimeHostKeepsRunOutcomeRecoveryWhenCatalogDisabled(t *testing.T) {
	t.Setenv(WorkflowCatalogEnabledEnv, "false")
	t.Setenv(AutomationEnabledEnv, "false")
	catalogEnabled, err := WorkflowCatalogEnabled(false, false)
	if err != nil || catalogEnabled {
		t.Fatalf("WorkflowCatalogEnabled = %v, %v", catalogEnabled, err)
	}
	automationEnabled, err := AutomationEnabled(false, false)
	if err != nil || automationEnabled {
		t.Fatalf("AutomationEnabled = %v, %v", automationEnabled, err)
	}
	host, err := BuildPlatformRuntimeHost([]platformruntime.Registration{
		{Component: runtimeComponentStub{id: "serve-driver-run-outcomes"}, Policy: platformruntime.Policy{Cadence: time.Minute}},
		{Component: runtimeComponentStub{id: "serve-await-event-notifications"}, Policy: platformruntime.Policy{Cadence: time.Minute}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := host.Snapshot()
	if len(snapshot.Components) != 2 || snapshot.Components[0].ID != "serve-await-event-notifications" ||
		snapshot.Components[1].ID != "serve-driver-run-outcomes" {
		t.Fatalf("catalog-disabled runtime components = %+v", snapshot.Components)
	}
}
