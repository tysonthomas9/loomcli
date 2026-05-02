package cli

import (
	"testing"
)

func TestResolveIssueBackendType(t *testing.T) {
	t.Run("empty env var falls through to config", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "")
		got := resolveIssueBackendType()
		// Empty string is treated as unset — falls through to project/global config.
		// With no config files matching, defaults to "fleetdb".
		if got != "fleetdb" {
			t.Errorf("resolveIssueBackendType() = %q, want %q", got, "fleetdb")
		}
	})

	t.Run("env var true returns fleetdb", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "true")
		got := resolveIssueBackendType()
		if got != "fleetdb" {
			t.Errorf("resolveIssueBackendType() = %q, want %q", got, "fleetdb")
		}
	})

	t.Run("env var 1 returns fleetdb", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "1")
		got := resolveIssueBackendType()
		if got != "fleetdb" {
			t.Errorf("resolveIssueBackendType() = %q, want %q", got, "fleetdb")
		}
	})

	t.Run("env var false returns fleetdb default", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "false")
		got := resolveIssueBackendType()
		if got != "fleetdb" {
			t.Errorf("resolveIssueBackendType() = %q, want %q", got, "fleetdb")
		}
	})

	t.Run("env var 0 returns fleetdb default", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "0")
		got := resolveIssueBackendType()
		if got != "fleetdb" {
			t.Errorf("resolveIssueBackendType() = %q, want %q", got, "fleetdb")
		}
	})

	t.Run("env var invalid returns fleetdb default", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "garbage")
		got := resolveIssueBackendType()
		if got != "fleetdb" {
			t.Errorf("resolveIssueBackendType() = %q, want %q", got, "fleetdb")
		}
	})
}

func TestIsFleetDBActive(t *testing.T) {
	t.Run("returns true when fleetdb", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "true")
		if !isFleetDBActive() {
			t.Error("expected isFleetDBActive() to return true")
		}
	})

	t.Run("returns true when fleetdb default", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "false")
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

func TestResolveIssueBackendType_FleetEnvVar_BeatsFleetDB(t *testing.T) {
	t.Setenv("LOOM_ISSUE_BACKEND", "fleet")
	t.Setenv("LOOM_FLEETDB_ENABLED", "true")
	got := resolveIssueBackendType()
	if got != "fleet" {
		t.Errorf("resolveIssueBackendType() = %q, want %q (LOOM_ISSUE_BACKEND should beat LOOM_FLEETDB_ENABLED)", got, "fleet")
	}
}

func TestResolveIssueBackendType_BeadsEnvVar_IsInvalid(t *testing.T) {
	t.Setenv("LOOM_ISSUE_BACKEND", "beads")
	t.Setenv("LOOM_FLEETDB_ENABLED", "true")
	got := resolveIssueBackendType()
	if got != "fleetdb" {
		t.Errorf("resolveIssueBackendType() = %q, want fleetdb (LOOM_ISSUE_BACKEND=beads is no longer valid)", got)
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

func TestResolveIssueBackendType_FleetDBEnvVar_Still_Works(t *testing.T) {
	// LOOM_FLEETDB_ENABLED=true without LOOM_ISSUE_BACKEND should still return "fleetdb"
	t.Setenv("LOOM_FLEETDB_ENABLED", "true")
	got := resolveIssueBackendType()
	if got != "fleetdb" {
		t.Errorf("resolveIssueBackendType() = %q, want %q", got, "fleetdb")
	}
}

func TestIsFleetActive(t *testing.T) {
	t.Run("returns true when fleet", func(t *testing.T) {
		t.Setenv("LOOM_ISSUE_BACKEND", "fleet")
		if !isFleetActive() {
			t.Error("expected isFleetActive() to return true when LOOM_ISSUE_BACKEND=fleet")
		}
	})

	t.Run("returns false when beads", func(t *testing.T) {
		t.Setenv("LOOM_ISSUE_BACKEND", "beads")
		if isFleetActive() {
			t.Error("expected isFleetActive() to return false when LOOM_ISSUE_BACKEND=beads")
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
