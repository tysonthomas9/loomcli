package cli

import (
	"testing"
)

func TestResolveIssueBackendType(t *testing.T) {
	t.Run("defaults to fleetdb", func(t *testing.T) {
		got := resolveIssueBackendType()
		if got != "fleetdb" {
			t.Errorf("resolveIssueBackendType() = %q, want %q", got, "fleetdb")
		}
	})
}

func TestIsFleetDBActive(t *testing.T) {
	t.Run("returns true when fleetdb default", func(t *testing.T) {
		if !isFleetDBActive() {
			t.Error("expected isFleetDBActive() to return true")
		}
	})
}

func TestResolveIssueBackendType_FleetEnvVar(t *testing.T) {
	t.Setenv("LOOM_ISSUE_BACKEND", "fleet")
	got := resolveIssueBackendType()
	if got != "fleet" {
		t.Errorf("resolveIssueBackendType() = %q, want %q", got, "fleet")
	}
}

func TestResolveIssueBackendType_InvalidEnvVar(t *testing.T) {
	t.Setenv("LOOM_ISSUE_BACKEND", "postgres")
	got := resolveIssueBackendType()
	// Invalid value is ignored (with a warning log); falls through to default "fleetdb"
	if got != "fleetdb" {
		t.Errorf("resolveIssueBackendType() = %q, want %q (invalid LOOM_ISSUE_BACKEND should fall through to default)", got, "fleetdb")
	}
}

func TestIsFleetActive(t *testing.T) {
	t.Run("returns true when fleet", func(t *testing.T) {
		t.Setenv("LOOM_ISSUE_BACKEND", "fleet")
		if !isFleetActive() {
			t.Error("expected isFleetActive() to return true when LOOM_ISSUE_BACKEND=fleet")
		}
	})

	t.Run("returns false when fleetdb", func(t *testing.T) {
		t.Setenv("LOOM_ISSUE_BACKEND", "fleetdb")
		if isFleetActive() {
			t.Error("expected isFleetActive() to return false when LOOM_ISSUE_BACKEND=fleetdb")
		}
	})

	t.Run("returns false by default", func(t *testing.T) {
		// No env vars set; defaults to fleetdb, not fleet mode.
		if isFleetActive() {
			t.Error("expected isFleetActive() to return false by default")
		}
	})
}
