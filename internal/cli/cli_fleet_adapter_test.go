package cli

import (
	"strings"
	"testing"
)

func TestCreateFleetWorkItems_MissingURL(t *testing.T) {
	// Ensure no env vars provide a fleet URL.
	t.Setenv("LOOM_FLEET_URL", "")
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv("LOOM_FLEET_API_KEY", "")
	t.Setenv("LOOM_ISSUE_BACKEND", "fleet")

	_, err := createFleetWorkItemStore()
	if err == nil {
		t.Fatal("expected error when fleet URL is missing, got nil")
	}
	if !strings.Contains(err.Error(), "fleet URL is required") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "fleet URL is required")
	}
}

func TestCreateFleetWorkItems_Success(t *testing.T) {
	t.Setenv("LOOM_FLEET_URL", "http://localhost:0")
	t.Setenv("LOOM_WORKSPACE", "test-ws")
	t.Setenv("LOOM_FLEET_API_KEY", "")
	t.Setenv("LOOM_ISSUE_BACKEND", "fleet")

	be, err := createFleetWorkItemStore()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if be == nil {
		t.Fatal("expected non-nil WorkItems")
	}
	if got := be.BackendName(); got != "fleet" {
		t.Errorf("BackendName() = %q, want %q", got, "fleet")
	}
}

func TestCreateFleetWorkItemsFromConfig_Valid(t *testing.T) {
	cfg := FleetClientConfig{
		URL:       "http://localhost:0",
		Workspace: "my-workspace",
		APIKey:    "test-key",
	}

	be, err := createFleetWorkItemStoreFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if be == nil {
		t.Fatal("expected non-nil WorkItems")
	}
	if got := be.BackendName(); got != "fleet" {
		t.Errorf("BackendName() = %q, want %q", got, "fleet")
	}
}

func TestCreateFleetWorkItemsFromConfig_EmptyURL(t *testing.T) {
	cfg := FleetClientConfig{
		URL:       "",
		Workspace: "ws",
		APIKey:    "key",
	}

	_, err := createFleetWorkItemStoreFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error when URL is empty, got nil")
	}
	if !strings.Contains(err.Error(), "fleet URL is required") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "fleet URL is required")
	}
}
