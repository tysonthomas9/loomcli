package connectors

import (
	"context"
	"errors"
	"testing"
	"time"
)

type bindingLifecycleStoreFake struct {
	ManagementStore
	configured ConfigureBindingSecretCommand
	grants     []*ConnectorGrant
	revoked    map[string]int
	revokeErr  map[string]error
}

func (store *bindingLifecycleStoreFake) ConfigureBindingSecretRecord(
	_ context.Context,
	workspace, bindingID, secret string,
) error {
	store.configured = ConfigureBindingSecretCommand{
		WorkspaceKey: workspace,
		BindingID:    bindingID,
		Secret:       secret,
	}
	return nil
}

func (store *bindingLifecycleStoreFake) ListGrantRecordsByBinding(
	_ context.Context,
	workspace, bindingID string,
) ([]*ConnectorGrant, error) {
	return append([]*ConnectorGrant(nil), store.grants...), nil
}

func (store *bindingLifecycleStoreFake) RevokeGrantRecord(
	_ context.Context,
	workspace, grantID string,
) error {
	if err := store.revokeErr[grantID]; err != nil {
		return err
	}
	if store.revoked == nil {
		store.revoked = make(map[string]int)
	}
	store.revoked[workspace+"/"+grantID]++
	return nil
}

func TestBindingLifecycleOwnsSecretPersistenceAndGrantCleanup(t *testing.T) {
	createdAt := time.Now().UTC()
	store := &bindingLifecycleStoreFake{
		grants: []*ConnectorGrant{
			{WorkspaceKey: "WORK", GrantID: "grant-1", ConnectorID: "github", BindingID: "binding-1", Action: "github.read", ResourcePattern: "repo:*", CreatedAt: createdAt},
			{WorkspaceKey: "WORK", GrantID: "grant-2", ConnectorID: "github", BindingID: "binding-1", Action: "github.write", ResourcePattern: "repo:one", CreatedAt: createdAt},
		},
		revokeErr: map[string]error{"grant-2": ErrGrantRevoked},
	}
	management, err := NewManagement(store)
	if err != nil {
		t.Fatal(err)
	}
	secret := ConfigureBindingSecretCommand{
		WorkspaceKey: "WORK",
		BindingID:    "binding-1",
		Secret:       "super-secret",
	}
	if err := management.ConfigureBindingSecret(t.Context(), secret); err != nil {
		t.Fatalf("ConfigureBindingSecret: %v", err)
	}
	if store.configured != secret {
		t.Fatalf("configured = %+v, want %+v", store.configured, secret)
	}
	revoked, err := management.RevokeBindingGrants(t.Context(), BindingGrantCleanupCommand{
		WorkspaceKey: "WORK",
		BindingID:    "binding-1",
	})
	if err != nil {
		t.Fatalf("RevokeBindingGrants: %v", err)
	}
	if revoked != 1 || store.revoked["WORK/grant-1"] != 1 || store.revoked["WORK/grant-2"] != 0 {
		t.Fatalf("revoked=%d calls=%v, want one changed grant and idempotent replay", revoked, store.revoked)
	}
}

func TestBindingLifecycleRejectsInvalidInputAndPersistedGrant(t *testing.T) {
	store := &bindingLifecycleStoreFake{grants: []*ConnectorGrant{nil}}
	management, err := NewManagement(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := management.ConfigureBindingSecret(t.Context(), ConfigureBindingSecretCommand{
		WorkspaceKey: "WORK",
		BindingID:    "binding-1",
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty secret error = %v, want %v", err, ErrInvalid)
	}
	if _, err := management.RevokeBindingGrants(t.Context(), BindingGrantCleanupCommand{
		WorkspaceKey: "WORK",
		BindingID:    "binding-1",
	}); !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("nil grant error = %v, want %v", err, ErrInvalidPersistedState)
	}
}
