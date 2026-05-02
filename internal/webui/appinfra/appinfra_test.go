package appinfra

import (
	"errors"
	"log/slog"
	"testing"
)

func TestReconcileConfigWorkspacesRegistersConfiguredWorkspaces(t *testing.T) {
	registry := NewWorkspaceRegistry(slog.Default())
	ReconcileConfigWorkspaces(
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

func TestReconcileConfigWorkspacesSkipsInitialWorkspace(t *testing.T) {
	registry := NewWorkspaceRegistry(slog.Default())
	ReconcileConfigWorkspaces(
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

func TestReconcileConfigWorkspacesIgnoresNilAndFailedList(t *testing.T) {
	registry := NewWorkspaceRegistry(slog.Default())
	ReconcileConfigWorkspaces(nil, "", false, registry, nil)
	if ids := registry.WorkspaceIDs(); len(ids) != 0 {
		t.Fatalf("WorkspaceIDs() after nil list = %#v, want none", ids)
	}

	ReconcileConfigWorkspaces(
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
