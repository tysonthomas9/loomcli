package agentmodules

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type bindingStoreStub struct {
	store.TriggerBindingStore
	update func(context.Context, string, string, store.TriggerBindingUpdate) (*automation.Binding, error)
}

func (stub *bindingStoreStub) Update(
	ctx context.Context,
	workspace, bindingID string,
	patch store.TriggerBindingUpdate,
) (*automation.Binding, error) {
	return stub.update(ctx, workspace, bindingID, patch)
}

type grantStoreStub struct {
	connectorsmodule.ManagementStore
	listByBinding func(context.Context, string, string) ([]*connectorsmodule.ConnectorGrant, error)
	revoke        func(context.Context, string, string) error
}

func (stub *grantStoreStub) ListGrantRecordsByBinding(
	ctx context.Context,
	workspace, bindingID string,
) ([]*connectorsmodule.ConnectorGrant, error) {
	return stub.listByBinding(ctx, workspace, bindingID)
}

func (stub *grantStoreStub) RevokeGrantRecord(ctx context.Context, workspace, grantID string) error {
	return stub.revoke(ctx, workspace, grantID)
}

func TestStoreConnectorCompatibilityConfigureBindingSecret(t *testing.T) {
	var gotWorkspace, gotBinding, gotSecret string
	bindings := &bindingStoreStub{update: func(
		_ context.Context,
		workspace, bindingID string,
		patch store.TriggerBindingUpdate,
	) (*automation.Binding, error) {
		gotWorkspace, gotBinding = workspace, bindingID
		if patch.WebhookSecret != nil {
			gotSecret = *patch.WebhookSecret
		}
		return &automation.Binding{}, nil
	}}
	compatibility := newStoreConnectorCompatibility(bindings, nil)

	if err := compatibility.ConfigureBindingSecret(t.Context(), " WORK ", " binding-1 ", "github", "secret"); err != nil {
		t.Fatalf("configure binding secret: %v", err)
	}
	if gotWorkspace != "WORK" || gotBinding != "binding-1" || gotSecret != "secret" {
		t.Fatalf("update = workspace %q binding %q secret %q", gotWorkspace, gotBinding, gotSecret)
	}
}

func TestStoreConnectorCompatibilityRevokeBindingGrants(t *testing.T) {
	var gotWorkspace, gotBinding string
	var revoked []string
	grants := &grantStoreStub{
		listByBinding: func(_ context.Context, workspace, bindingID string) ([]*connectorsmodule.ConnectorGrant, error) {
			gotWorkspace, gotBinding = workspace, bindingID
			return []*connectorsmodule.ConnectorGrant{
				nil,
				{GrantID: "grant-new"},
				{GrantID: "grant-already-revoked"},
				{GrantID: "grant-last"},
			}, nil
		},
		revoke: func(_ context.Context, workspace, grantID string) error {
			if workspace != "WORK" {
				t.Fatalf("revoke workspace = %q", workspace)
			}
			revoked = append(revoked, grantID)
			if grantID == "grant-already-revoked" {
				return connectorsmodule.ErrGrantRevoked
			}
			return nil
		},
	}
	compatibility := newStoreConnectorCompatibility(nil, grants)

	count, err := compatibility.RevokeBindingGrants(t.Context(), " WORK ", " binding-1 ")
	if err != nil {
		t.Fatalf("revoke binding grants: %v", err)
	}
	if count != 2 {
		t.Fatalf("revoked count = %d, want 2", count)
	}
	if gotWorkspace != "WORK" || gotBinding != "binding-1" {
		t.Fatalf("list = workspace %q binding %q", gotWorkspace, gotBinding)
	}
	want := []string{"grant-new", "grant-already-revoked", "grant-last"}
	if len(revoked) != len(want) {
		t.Fatalf("revoke calls = %v, want %v", revoked, want)
	}
	for i := range want {
		if revoked[i] != want[i] {
			t.Fatalf("revoke calls = %v, want %v", revoked, want)
		}
	}
}

func TestStoreConnectorCompatibilityFailsClosedWithoutOwnedStore(t *testing.T) {
	if got := newStoreConnectorCompatibility(nil, nil); got != nil {
		t.Fatalf("nil stores produced compatibility adapter %T", got)
	}
	secretOnly := newStoreConnectorCompatibility(&bindingStoreStub{}, nil)
	if _, err := secretOnly.RevokeBindingGrants(t.Context(), "WORK", "binding-1"); !errors.Is(err, automation.ErrUnavailable) {
		t.Fatalf("revoke without grant store error = %v, want unavailable", err)
	}
	grantOnly := newStoreConnectorCompatibility(nil, &grantStoreStub{})
	if err := grantOnly.ConfigureBindingSecret(t.Context(), "WORK", "binding-1", "github", "secret"); !errors.Is(err, automation.ErrUnavailable) {
		t.Fatalf("configure without binding store error = %v, want unavailable", err)
	}
}
