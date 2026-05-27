package cli

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
)

func TestCreateFleetIssueBackend_MissingURL(t *testing.T) {
	// Ensure no env vars provide a fleet URL.
	t.Setenv(bootstrap.EnvFleetDBURL, "")
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv(bootstrap.EnvFleetDBAPIKey, "")
	t.Setenv("LOOM_ISSUE_BACKEND", "fleet")

	_, err := createFleetIssueBackend()
	if err == nil {
		t.Fatal("expected error when fleet URL is missing, got nil")
	}
	if !strings.Contains(err.Error(), "fleet URL is required") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "fleet URL is required")
	}
}

func TestCreateFleetIssueBackend_Success(t *testing.T) {
	t.Setenv(bootstrap.EnvFleetDBURL, "http://localhost:0")
	t.Setenv("LOOM_WORKSPACE", "test-ws")
	t.Setenv(bootstrap.EnvFleetDBAPIKey, "")
	t.Setenv("LOOM_ISSUE_BACKEND", "fleet")

	be, err := createFleetIssueBackend()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if be == nil {
		t.Fatal("expected non-nil IssueBackend")
	}
	if got := be.BackendName(); got != "fleet" {
		t.Errorf("BackendName() = %q, want %q", got, "fleet")
	}
}

func TestCreateFleetIssueBackendFromConfig_Valid(t *testing.T) {
	cfg := FleetClientConfig{
		URL:       "http://localhost:0",
		Workspace: "my-workspace",
		APIKey:    "test-key",
	}

	be, err := createFleetIssueBackendFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if be == nil {
		t.Fatal("expected non-nil IssueBackend")
	}
	if got := be.BackendName(); got != "fleet" {
		t.Errorf("BackendName() = %q, want %q", got, "fleet")
	}
}

func TestCreateFleetIssueBackendFromConfig_EmptyURL(t *testing.T) {
	cfg := FleetClientConfig{
		URL:       "",
		Workspace: "ws",
		APIKey:    "key",
	}

	_, err := createFleetIssueBackendFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error when URL is empty, got nil")
	}
	if !strings.Contains(err.Error(), "fleet URL is required") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "fleet URL is required")
	}
}
