package fleetdb

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
)

type transportFake struct {
	driver   *workflowcatalog.Driver
	version  *workflowcatalog.DriverVersion
	drivers  []*workflowcatalog.Driver
	versions []*workflowcatalog.DriverVersion
	result   *TransportLifecycleResult
	err      error
	action   string
	revision uint64
}

type authoringTransportFake struct {
	mutation AuthoringMutationAlias
	result   *TransportAuthoringResult
	err      error
}

// Alias keeps failure output compact while retaining the exact public shape.
type AuthoringMutationAlias = workflowcatalog.AuthoringMutation

func (fake *authoringTransportFake) AuthorVersion(_ context.Context, mutation workflowcatalog.AuthoringMutation) (*TransportAuthoringResult, error) {
	fake.mutation = mutation
	return fake.result, fake.err
}

func (f *transportFake) GetDriver(context.Context, string, string) (*workflowcatalog.Driver, error) {
	return f.driver, f.err
}
func (f *transportFake) FindDriverByName(context.Context, string, string) (*workflowcatalog.Driver, error) {
	return f.driver, f.err
}
func (f *transportFake) ListDrivers(context.Context, string) ([]*workflowcatalog.Driver, error) {
	return f.drivers, f.err
}
func (f *transportFake) GetVersion(context.Context, string, string) (*workflowcatalog.DriverVersion, error) {
	return f.version, f.err
}
func (f *transportFake) ListVersions(context.Context, string, string) ([]*workflowcatalog.DriverVersion, error) {
	return f.versions, f.err
}
func (f *transportFake) ApproveVersion(_ context.Context, _, _, _ string, revision uint64) (*TransportLifecycleResult, error) {
	f.action, f.revision = "approve", revision
	return f.result, f.err
}
func (f *transportFake) UnapproveVersion(_ context.Context, _, _, _ string, revision uint64) (*TransportLifecycleResult, error) {
	f.action, f.revision = "unapprove", revision
	return f.result, f.err
}
func (f *transportFake) ActivateVersion(_ context.Context, _, _, _ string, revision uint64) (*TransportLifecycleResult, error) {
	f.action, f.revision = "activate", revision
	return f.result, f.err
}

func TestAdapterDelegatesReadsAndLifecycle(t *testing.T) {
	driver := &workflowcatalog.Driver{WorkspaceKey: "TEST", DriverID: "driver-1", Revision: 8}
	version := &workflowcatalog.DriverVersion{WorkspaceKey: "TEST", DriverID: "driver-1", VersionID: "v1"}
	transport := &transportFake{
		driver: driver, version: version, drivers: []*workflowcatalog.Driver{driver}, versions: []*workflowcatalog.DriverVersion{version},
		result: &TransportLifecycleResult{
			Driver: driver, Version: version, CommittedRevision: 8,
			SemanticImpact: workflowcatalog.SemanticImpactVersionTrustChanged, Replayed: true,
		},
	}
	adapter, err := New(transport)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, err := adapter.GetDriver(context.Background(), "TEST", "driver-1"); err != nil || got != driver {
		t.Fatalf("GetDriver = %#v, %v", got, err)
	}
	if got, err := adapter.ListVersions(context.Background(), "TEST", "driver-1"); err != nil || len(got) != 1 || got[0] != version {
		t.Fatalf("ListVersions = %#v, %v", got, err)
	}
	result, err := adapter.ApproveVersion(context.Background(), workflowcatalog.LifecycleMutation{
		WorkspaceKey: "TEST", DriverID: "driver-1", VersionID: "v1", ExpectedRevision: 7,
	})
	if err != nil {
		t.Fatalf("ApproveVersion: %v", err)
	}
	if transport.action != "approve" || transport.revision != 7 || result.CommittedRevision != 8 || !result.Replayed || result.SemanticImpact != workflowcatalog.SemanticImpactVersionTrustChanged {
		t.Fatalf("delegation action=%q revision=%d result=%#v", transport.action, transport.revision, result)
	}
}

func TestAdapterMapsFleetDBErrorsToCatalogOwnership(t *testing.T) {
	for _, test := range []struct {
		name string
		in   error
		want error
	}{
		{name: "not found", in: ErrTransportNotFound, want: workflowcatalog.ErrNotFound},
		{name: "revision", in: ErrTransportRevisionConflict, want: workflowcatalog.ErrStaleRevision},
		{name: "ownership", in: ErrTransportVersionOwnership, want: workflowcatalog.ErrVersionOwnership},
		{name: "validation", in: ErrTransportVersionNotValidated, want: workflowcatalog.ErrVersionNotValidated},
		{name: "approval", in: ErrTransportVersionNotApproved, want: workflowcatalog.ErrVersionNotApproved},
		{name: "unavailable", in: errors.New("dial refused"), want: workflowcatalog.ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter, err := New(&transportFake{err: test.in})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = adapter.GetDriver(context.Background(), "TEST", "driver-1")
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want errors.Is %v", err, test.want)
			}
		})
	}
}

func TestAdapterRejectsNilDependenciesAndEmptyLifecycleResponse(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, workflowcatalog.ErrUnavailable) {
		t.Fatalf("New(nil) error = %v", err)
	}
	adapter, err := New(&transportFake{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = adapter.ActivateVersion(context.Background(), workflowcatalog.LifecycleMutation{})
	if !errors.Is(err, workflowcatalog.ErrInvalidPersistedState) {
		t.Fatalf("empty result error = %v", err)
	}
}

func TestAdapterDelegatesAtomicAuthoringOnlyWhenComposed(t *testing.T) {
	base := &transportFake{}
	adapter, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.AuthorVersion(context.Background(), workflowcatalog.AuthoringMutation{}); !errors.Is(err, workflowcatalog.ErrUnavailable) {
		t.Fatalf("lifecycle-only AuthorVersion err = %v, want unavailable", err)
	}
	if _, err := NewWithAuthoring(base, nil); !errors.Is(err, workflowcatalog.ErrUnavailable) {
		t.Fatalf("NewWithAuthoring(nil) err = %v", err)
	}

	driver := &workflowcatalog.Driver{WorkspaceKey: "TEST", DriverID: "demo", Revision: 1}
	version := &workflowcatalog.DriverVersion{WorkspaceKey: "TEST", DriverID: "demo", VersionID: "demo-v1"}
	authoring := &authoringTransportFake{result: &TransportAuthoringResult{
		Driver: driver, Version: version, CreatedDriver: true, CreatedVersion: true,
		CommittedRevision: 1, SemanticImpact: workflowcatalog.SemanticImpactVersionAuthored,
	}}
	adapter, err = NewWithAuthoring(base, authoring)
	if err != nil {
		t.Fatal(err)
	}
	mutation := workflowcatalog.AuthoringMutation{
		AuthorVersionCommand: workflowcatalog.AuthorVersionCommand{
			WorkspaceKey: "TEST", RequestID: "request-1", DriverID: "demo", VersionID: "demo-v1",
		},
		Managed: true, Activate: true,
	}
	result, err := adapter.AuthorVersion(context.Background(), mutation)
	if err != nil {
		t.Fatalf("AuthorVersion: %v", err)
	}
	if authoring.mutation.RequestID != "request-1" || !authoring.mutation.Managed || !authoring.mutation.Activate ||
		result.Driver != driver || result.Version != version || !result.CreatedDriver || !result.CreatedVersion {
		t.Fatalf("authoring delegation mutation=%+v result=%+v", authoring.mutation, result)
	}
}

func TestAdapterMapsAuthoringConflict(t *testing.T) {
	adapter, err := NewWithAuthoring(&transportFake{}, &authoringTransportFake{err: ErrTransportAuthoringConflict})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.AuthorVersion(context.Background(), workflowcatalog.AuthoringMutation{})
	if !errors.Is(err, workflowcatalog.ErrAuthoringConflict) {
		t.Fatalf("AuthorVersion err = %v, want authoring conflict", err)
	}
}
