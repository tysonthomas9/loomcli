package cli

import (
	"bytes"
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

func TestPrintDaemonBannerNormalAndFleetModes(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "")
	normal := captureCLIStdout(t, func() {
		PrintDaemonBanner(&DaemonConfig{
			Agents: []AgentEntry{{Worktree: "nova", Role: "task"}},
		}, "/workspace")
	})
	if !strings.Contains(normal, "Loom Agent Supervisor") ||
		!strings.Contains(normal, "Agents: 1") ||
		!strings.Contains(normal, "nova (task)") {
		t.Fatalf("normal banner = %q", normal)
	}

	fleet := captureCLIStdout(t, func() {
		PrintDaemonBanner(&DaemonConfig{Backend: BackendFleet}, "/workspace")
	})
	if !strings.Contains(fleet, "Fleet Mode") ||
		!strings.Contains(fleet, "Agent supervision disabled") {
		t.Fatalf("fleet banner = %q", fleet)
	}
}

func captureCLIStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = old
		_ = r.Close()
	}()
	fn()
	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy stdout: %v", err)
	}
	return buf.String()
}
