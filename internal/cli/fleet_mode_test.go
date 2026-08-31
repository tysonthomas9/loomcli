package cli

import (
	"io"
	"os"
	"strings"
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
	cfg := &DaemonConfig{Backend: ""}
	if isFleetMode(cfg) {
		t.Error("isFleetMode with empty backend = true, want false")
	}
}

func TestIsFleetMode_UnsupportedBackend(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "")
	cfg := &DaemonConfig{Backend: "unknown"}
	if isFleetMode(cfg) {
		t.Error("isFleetMode with backend=unknown = true, want false")
	}
}

func TestIsFleetMode_FleetBackend(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "")
	cfg := &DaemonConfig{Backend: BackendFleet}
	if !isFleetMode(cfg) {
		t.Error("isFleetMode with backend=fleet = false, want true")
	}
}

func TestIsFleetMode_AIBackendNotFleet(t *testing.T) {
	// AI backends (claude, codex) should NOT trigger fleet mode
	t.Setenv(fleetModeEnvVar, "")
	for _, b := range []string{"claude", "codex", "opencode"} {
		cfg := &DaemonConfig{Backend: b}
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
	cfg := &DaemonConfig{Backend: "unknown"}
	if !isFleetMode(cfg) {
		t.Error("isFleetMode with LOOM_ISSUE_BACKEND=fleet and backend=unknown = false, want true")
	}
}

func TestIsFleetMode_EnvVarOtherValue(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "unknown")
	cfg := &DaemonConfig{Backend: ""}
	if isFleetMode(cfg) {
		t.Error("isFleetMode with LOOM_ISSUE_BACKEND=unknown = true, want false")
	}
}

func TestIsFleetMode_FleetDBIsNotFleetMode(t *testing.T) {
	// "fleetdb" (embedded SQLite fleet store) is NOT the same as "fleet" (external fleet server)
	t.Setenv(fleetModeEnvVar, "")
	cfg := &DaemonConfig{Backend: "fleetdb"}
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

// captureBanner runs PrintDaemonBanner with stdout redirected.
func captureBanner(t *testing.T, cfg *DaemonConfig, lines []string) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	PrintDaemonBanner(cfg, "/tmp/ws", lines)
	os.Stdout = orig
	w.Close()

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read banner: %v", err)
	}
	return string(out)
}

func TestPrintDaemonBanner_CleanRunPrintsNothingExtra(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "")
	got := captureBanner(t, &DaemonConfig{}, nil)
	if !strings.Contains(got, "Loom Agent Supervisor") || !strings.Contains(got, "Press Ctrl+C to stop") {
		t.Fatalf("banner lost its usual content:\n%s", got)
	}
	if strings.Contains(got, "Degraded") {
		t.Errorf("a clean run must not mention degradation:\n%s", got)
	}
}

func TestPrintDaemonBanner_PrintsDegradedBlock(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "")
	lines := []string{"Degraded:", "  - skill-materialization-leases", "      effect: runs unlocked"}
	got := captureBanner(t, &DaemonConfig{}, lines)
	for _, want := range lines {
		if !strings.Contains(got, want) {
			t.Errorf("banner missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "Press Ctrl+C to stop") {
		t.Errorf("degraded block must not replace the banner tail:\n%s", got)
	}
}

func TestPrintDaemonBanner_PrintsDegradedBlockInFleetMode(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "")
	lines := []string{"Degraded:", "  - skill-materialization-leases"}
	got := captureBanner(t, &DaemonConfig{Backend: BackendFleet}, lines)
	if !strings.Contains(got, "Fleet Mode") {
		t.Fatalf("expected the fleet-mode banner:\n%s", got)
	}
	for _, want := range lines {
		if !strings.Contains(got, want) {
			t.Errorf("fleet-mode banner missing %q:\n%s", want, got)
		}
	}
}
