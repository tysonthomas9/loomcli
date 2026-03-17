package cli

import (
	"testing"
)

func TestResolveIssueBackendType(t *testing.T) {
	t.Run("empty env var falls through to config", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "")
		got := resolveIssueBackendType()
		// Empty string is treated as unset — falls through to project/global config.
		// With no config files matching, defaults to "beads".
		if got != "beads" {
			t.Errorf("resolveIssueBackendType() = %q, want %q", got, "beads")
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

	t.Run("env var false returns beads", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "false")
		got := resolveIssueBackendType()
		if got != "beads" {
			t.Errorf("resolveIssueBackendType() = %q, want %q", got, "beads")
		}
	})

	t.Run("env var 0 returns beads", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "0")
		got := resolveIssueBackendType()
		if got != "beads" {
			t.Errorf("resolveIssueBackendType() = %q, want %q", got, "beads")
		}
	})

	t.Run("env var invalid returns beads", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "garbage")
		got := resolveIssueBackendType()
		if got != "beads" {
			t.Errorf("resolveIssueBackendType() = %q, want %q", got, "beads")
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

	t.Run("returns false when beads", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "false")
		if isFleetDBActive() {
			t.Error("expected isFleetDBActive() to return false")
		}
	})
}
