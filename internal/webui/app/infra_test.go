package app

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/subscription"
)

func TestReconcileStoreWorkspacesRegistersConfiguredWorkspaces(t *testing.T) {
	registry := NewWorkspaceRegistry(slog.Default())
	ReconcileStoreWorkspaces(
		func() (map[string]string, error) {
			return map[string]string{"ws-1": t.TempDir()}, nil
		},
		"",
		false,
		registry,
		slog.Default(),
	)

	ids := registry.WorkspaceIDs()
	if len(ids) != 1 || ids[0] != "ws-1" {
		t.Fatalf("WorkspaceIDs() = %#v, want [ws-1]", ids)
	}
}

func TestReconcileStoreWorkspacesSkipsInitialWorkspace(t *testing.T) {
	registry := NewWorkspaceRegistry(slog.Default())
	ReconcileStoreWorkspaces(
		func() (map[string]string, error) {
			return map[string]string{"initial": t.TempDir()}, nil
		},
		"initial",
		true,
		registry,
		nil,
	)

	if ids := registry.WorkspaceIDs(); len(ids) != 0 {
		t.Fatalf("WorkspaceIDs() = %#v, want none", ids)
	}
}

func TestReconcileStoreWorkspacesSkipsMissingLocalPath(t *testing.T) {
	registry := NewWorkspaceRegistry(slog.Default())
	ReconcileStoreWorkspaces(
		func() (map[string]string, error) {
			return map[string]string{
				"pathless": "",
				"spaced":   "   ",
				"valid":    t.TempDir(),
			}, nil
		},
		"",
		false,
		registry,
		nil,
	)

	ids := registry.WorkspaceIDs()
	if len(ids) != 1 || ids[0] != "valid" {
		t.Fatalf("WorkspaceIDs() = %#v, want [valid]", ids)
	}
}

func TestReconcileStoreWorkspacesIgnoresNilAndFailedList(t *testing.T) {
	registry := NewWorkspaceRegistry(slog.Default())
	ReconcileStoreWorkspaces(nil, "", false, registry, nil)
	if ids := registry.WorkspaceIDs(); len(ids) != 0 {
		t.Fatalf("WorkspaceIDs() after nil list = %#v, want none", ids)
	}

	ReconcileStoreWorkspaces(
		func() (map[string]string, error) { return nil, errors.New("boom") },
		"",
		false,
		registry,
		nil,
	)
	if ids := registry.WorkspaceIDs(); len(ids) != 0 {
		t.Fatalf("WorkspaceIDs() after failed list = %#v, want none", ids)
	}
}

func TestRegisterHooksRegistersFleetSubscriberForFleetDBClient(t *testing.T) {
	registry := NewWorkspaceRegistry(slog.Default())
	hub := realtime.NewHub()
	multiSub := subscription.NewMultiWorkspaceSubscriber(hub, slog.Default())
	t.Cleanup(multiSub.Stop)

	registered := RegisterHooks(registry, HookConfig{
		MultiSub: multiSub,
		FleetURL: "http://fleet-db:8080",
		Logger:   slog.Default(),
	})

	if registered.FleetSubscriber == nil {
		t.Fatal("FleetSubscriber was nil; fleet-db clients need SSE mutation streaming even when FleetMode is false")
	}
}
