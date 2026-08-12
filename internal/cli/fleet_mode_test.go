package cli

import (
	"testing"
)

func TestIsFleetMode_NilConfig(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "")
	if isFleetMode(nil) {
		t.Error("isFleetMode(nil) = true, want false")
	}
}

func TestIsFleetMode_EmptyBackend(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "")
	cfg := &RuntimeConfig{Backend: ""}
	if isFleetMode(cfg) {
		t.Error("isFleetMode with empty backend = true, want false")
	}
}

func TestIsFleetMode_UnsupportedBackend(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "")
	cfg := &RuntimeConfig{Backend: "unknown"}
	if isFleetMode(cfg) {
		t.Error("isFleetMode with backend=unknown = true, want false")
	}
}

func TestIsFleetMode_FleetWorkItemsAdapter(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "")
	cfg := &RuntimeConfig{Backend: BackendFleet}
	if !isFleetMode(cfg) {
		t.Error("isFleetMode with backend=fleet = false, want true")
	}
}

func TestIsFleetMode_AIBackendNotFleet(t *testing.T) {
	// AI backends (claude, codex) should NOT trigger fleet mode
	t.Setenv(fleetModeEnvVar, "")
	for _, b := range []string{"claude", "codex", "opencode"} {
		cfg := &RuntimeConfig{Backend: b}
		if isFleetMode(cfg) {
			t.Errorf("isFleetMode with backend=%s = true, want false", b)
		}
	}
}

func TestIsFleetMode_EnvVarOverride(t *testing.T) {
	t.Setenv(fleetModeEnvVar, BackendFleet)
	// Env var should override config, even with empty/nil config
	if !isFleetMode(nil) {
		t.Error("isFleetMode with LOOM_ISSUE_BACKEND=fleet and nil config = false, want true")
	}
	cfg := &RuntimeConfig{Backend: "unknown"}
	if !isFleetMode(cfg) {
		t.Error("isFleetMode with LOOM_ISSUE_BACKEND=fleet and backend=unknown = false, want true")
	}
}

func TestIsFleetMode_EnvVarOtherValue(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "unknown")
	cfg := &RuntimeConfig{Backend: ""}
	if isFleetMode(cfg) {
		t.Error("isFleetMode with LOOM_ISSUE_BACKEND=unknown = true, want false")
	}
}

func TestIsFleetMode_FleetDBIsNotFleetMode(t *testing.T) {
	// "fleetdb" (embedded SQLite fleet store) is NOT the same as "fleet" (external fleet server)
	t.Setenv(fleetModeEnvVar, "")
	cfg := &RuntimeConfig{Backend: "fleetdb"}
	if isFleetMode(cfg) {
		t.Error("isFleetMode with backend=fleetdb = true, want false (fleetdb != fleet)")
	}
}

func TestIsFleetModeFromEnv_NoFleetMode(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "")
	// Without env var and without a project config, should return false
	origDir := t.TempDir()
	t.Chdir(origDir)
	if isFleetModeFromEnv() {
		t.Error("isFleetModeFromEnv() = true, want false (no env var, no config)")
	}
}

func TestIsFleetModeFromEnv_EnvVar(t *testing.T) {
	t.Setenv(fleetModeEnvVar, BackendFleet)
	if !isFleetModeFromEnv() {
		t.Error("isFleetModeFromEnv() = false, want true (LOOM_ISSUE_BACKEND=fleet)")
	}
}
