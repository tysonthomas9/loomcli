package fleetdb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

type transportFake struct {
	grant     *ConnectorGrantWire
	grants    []*ConnectorGrantWire
	err       error
	create    CreateConnectorGrantWire
	workspace string
	filter    ConnectorGrantFilterWire
	calls     []string
}

func (fake *transportFake) CreateConnectorGrant(
	_ context.Context,
	request CreateConnectorGrantWire,
) (*ConnectorGrantWire, error) {
	fake.calls = append(fake.calls, "create")
	fake.create = request
	return fake.grant, fake.err
}

func (fake *transportFake) ListConnectorGrants(
	_ context.Context,
	workspace string,
	filter ConnectorGrantFilterWire,
) ([]*ConnectorGrantWire, error) {
	fake.calls = append(fake.calls, "list")
	fake.workspace, fake.filter = workspace, filter
	return fake.grants, fake.err
}

func TestAdapterMapsCreateAndBindingFilteredList(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	revokedAt := now.Add(time.Hour)
	wire := &ConnectorGrantWire{
		WorkspaceKey: "WS", GrantID: "grant-read", ConnectorID: "github-main",
		BindingID: "binding-docs", Action: "pull_request.read",
		ResourcePattern: "repo:acme/docs", CreatedAt: now, RevokedAt: &revokedAt,
	}
	fake := &transportFake{grant: wire, grants: []*ConnectorGrantWire{wire}}
	adapter, err := New(fake)
	if err != nil {
		t.Fatal(err)
	}

	created, err := adapter.CreateGrant(t.Context(), connectors.CreateGrantMutation{
		WorkspaceKey: "WS", GrantID: "grant-read", ConnectorID: "github-main",
		BindingID: "binding-docs", Action: "pull_request.read",
		ResourcePattern: "repo:acme/docs",
	})
	if err != nil {
		t.Fatalf("CreateGrant: %v", err)
	}
	if fake.create != (CreateConnectorGrantWire{
		WorkspaceKey: "WS", GrantID: "grant-read", ConnectorID: "github-main",
		BindingID: "binding-docs", Action: "pull_request.read",
		ResourcePattern: "repo:acme/docs",
	}) {
		t.Fatalf("create wire = %#v", fake.create)
	}
	if created.GrantID != wire.GrantID || created.ResourcePattern != wire.ResourcePattern ||
		created.RevokedAt == nil || !created.RevokedAt.Equal(revokedAt) {
		t.Fatalf("created mapping = %#v", created)
	}
	*created.RevokedAt = time.Time{}
	if wire.RevokedAt.IsZero() {
		t.Fatal("created mapping leaked transport RevokedAt pointer")
	}

	listed, err := adapter.ListGrantsByBinding(t.Context(), "WS", "binding-docs")
	if err != nil {
		t.Fatalf("ListGrantsByBinding: %v", err)
	}
	if len(listed) != 1 || listed[0].GrantID != "grant-read" ||
		fake.workspace != "WS" || fake.filter.BindingID != "binding-docs" {
		t.Fatalf(
			"listed=%#v workspace=%q filter=%#v",
			listed,
			fake.workspace,
			fake.filter,
		)
	}
	if len(fake.calls) != 2 || fake.calls[0] != "create" || fake.calls[1] != "list" {
		t.Fatalf("calls = %#v", fake.calls)
	}
}

func TestAdapterPreservesNilRecordsForCapabilityValidation(t *testing.T) {
	adapter, err := New(&transportFake{grants: []*ConnectorGrantWire{nil}})
	if err != nil {
		t.Fatal(err)
	}
	values, err := adapter.ListGrantsByBinding(t.Context(), "WS", "binding")
	if err != nil || len(values) != 1 || values[0] != nil {
		t.Fatalf("nil record mapping = %#v, %v", values, err)
	}

	adapter, err = New(&transportFake{})
	if err != nil {
		t.Fatal(err)
	}
	values, err = adapter.ListGrantsByBinding(t.Context(), "WS", "binding")
	if err != nil || values == nil || len(values) != 0 {
		t.Fatalf("nil slice mapping = %#v, %v; want non-nil empty", values, err)
	}
}

func TestAdapterMapsTransportErrorsToConnectorOwnership(t *testing.T) {
	tests := []struct {
		name string
		in   error
		want error
	}{
		{name: "not found", in: ErrTransportNotFound, want: connectors.ErrNotFound},
		{name: "invalid", in: ErrTransportInvalid, want: connectors.ErrInvalid},
		{name: "already exists", in: ErrTransportAlreadyExists, want: connectors.ErrGrantConflict},
		{name: "conflict", in: ErrTransportConflict, want: connectors.ErrGrantConflict},
		{name: "typed unavailable", in: ErrTransportUnavailable, want: connectors.ErrUnavailable},
		{name: "unknown", in: errors.New("dial refused"), want: connectors.ErrUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, err := New(&transportFake{err: test.in})
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.CreateGrant(t.Context(), connectors.CreateGrantMutation{})
			if !errors.Is(err, test.want) || !errors.Is(err, test.in) {
				t.Fatalf("CreateGrant error = %v, want %v and original", err, test.want)
			}
		})
	}
}

func TestNewRejectsNilTransport(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, connectors.ErrUnavailable) {
		t.Fatalf("New(nil) error = %v, want unavailable", err)
	}
}
