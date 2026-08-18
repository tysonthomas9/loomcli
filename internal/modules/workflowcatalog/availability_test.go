package workflowcatalog

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestVersionAvailableRequiresExplicitAvailableState(t *testing.T) {
	for _, test := range []struct {
		name   string
		status DriverVersionAvailabilityStatus
		want   bool
	}{
		{name: "pending", status: DriverVersionAvailabilityPending},
		{name: "available", status: DriverVersionAvailabilityAvailable, want: true},
		{name: "failed", status: DriverVersionAvailabilityFailed},
		{name: "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := VersionAvailable(&DriverVersion{AvailabilityStatus: test.status}); got != test.want {
				t.Fatalf("VersionAvailable(%q) = %v, want %v", test.status, got, test.want)
			}
		})
	}
	if VersionAvailable(nil) {
		t.Fatal("nil version reported available")
	}
}

func TestPendingVersionCannotResolveApproveOrActivate(t *testing.T) {
	fixture := newCatalogFixture(t)
	fixture.reader.versions["v1"].AvailabilityStatus = DriverVersionAvailabilityPending

	if _, err := fixture.service.ResolveEffectiveVersion(
		context.Background(),
		fixture.system(t, "TEST", ActionResolveEffectiveVersion),
		"TEST",
		"driver-1",
	); !errors.Is(err, ErrVersionNotAvailable) {
		t.Fatalf("ResolveEffectiveVersion error = %v, want ErrVersionNotAvailable", err)
	}
	if _, err := fixture.service.ResolveRequestedVersion(
		context.Background(),
		fixture.operator(t, "TEST", ActionResolveRequestedVersion),
		"TEST",
		"driver-1",
		"v1",
	); !errors.Is(err, ErrVersionNotAvailable) {
		t.Fatalf("ResolveRequestedVersion error = %v, want ErrVersionNotAvailable", err)
	}

	for _, action := range []struct {
		name string
		call func() error
	}{
		{name: "approve", call: func() error {
			_, err := fixture.service.ApproveVersion(context.Background(), fixture.operator(t, "TEST", ActionApproveVersion), VersionCommand{
				WorkspaceKey: "TEST", DriverID: "driver-1", VersionID: "v1", ExpectedRevision: 7,
			})
			return err
		}},
		{name: "activate", call: func() error {
			_, err := fixture.service.ActivateVersion(context.Background(), fixture.operator(t, "TEST", ActionActivateVersion), VersionCommand{
				WorkspaceKey: "TEST", DriverID: "driver-1", VersionID: "v1", ExpectedRevision: 7,
			})
			return err
		}},
	} {
		t.Run(action.name, func(t *testing.T) {
			if err := action.call(); !errors.Is(err, ErrVersionNotAvailable) {
				t.Fatalf("error = %v, want ErrVersionNotAvailable", err)
			}
		})
	}
	if len(fixture.lifecycle.calls) != 0 {
		t.Fatalf("pending version reached lifecycle store: %v", fixture.lifecycle.calls)
	}
}

func TestManagedLifecycleUsesExactSystemActions(t *testing.T) {
	fixture := newCatalogFixture(t)
	admission, err := fixture.issuer.NewAdmission(
		authority.Allow(ActionApproveManagedVersion, authority.ClassSystem),
		authority.Allow(ActionActivateManagedVersion, authority.ClassSystem),
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.service = New(fixture.reader, fixture.lifecycle, admission)
	approved, err := fixture.service.ApproveManagedVersion(
		context.Background(),
		fixture.system(t, "TEST", ActionApproveManagedVersion),
		VersionCommand{WorkspaceKey: "TEST", DriverID: "driver-1", VersionID: "v2", ExpectedRevision: 7},
	)
	if err != nil {
		t.Fatalf("ApproveManagedVersion: %v", err)
	}
	if approved.Action != ActionApproveManagedVersion || len(fixture.lifecycle.calls) != 1 || fixture.lifecycle.calls[0] != ActionApproveVersion {
		t.Fatalf("approved=%+v calls=%v", approved, fixture.lifecycle.calls)
	}

	fixture.reader.drivers["driver-1"] = cloneDriver(approved.Driver)
	activated, err := fixture.service.ActivateManagedVersion(
		context.Background(),
		fixture.system(t, "TEST", ActionActivateManagedVersion),
		VersionCommand{WorkspaceKey: "TEST", DriverID: "driver-1", VersionID: "v2", ExpectedRevision: approved.Driver.Revision},
	)
	if err != nil {
		t.Fatalf("ActivateManagedVersion: %v", err)
	}
	if activated.Action != ActionActivateManagedVersion || !activated.Active || len(fixture.lifecycle.calls) != 2 || fixture.lifecycle.calls[1] != ActionActivateVersion {
		t.Fatalf("activated=%+v calls=%v", activated, fixture.lifecycle.calls)
	}
}
