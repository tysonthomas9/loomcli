package bootstrap

import (
	"path/filepath"
	"testing"
)

func TestFleetDBRuntimeDirIgnoresLoomConfigDir(t *testing.T) {
	home := t.TempDir()
	configDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	t.Setenv(EnvFleetDBRuntimeDir, "")

	want := filepath.Join(home, ".loom", "fleet-db")
	if got := FleetDBRuntimeDir(); got != want {
		t.Fatalf("FleetDBRuntimeDir() = %q, want %q", got, want)
	}
	if got := LoomDir(); got != configDir {
		t.Fatalf("LoomDir() = %q, want LOOM_CONFIG_DIR %q", got, configDir)
	}
}

func TestFleetDBRuntimeDirEnvOverride(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "fleet-db-runtime")
	t.Setenv(EnvFleetDBRuntimeDir, runtimeDir)

	if got := FleetDBRuntimeDir(); got != runtimeDir {
		t.Fatalf("FleetDBRuntimeDir() = %q, want %q", got, runtimeDir)
	}
	if got := FleetDBSettingsDir(); got != filepath.Dir(runtimeDir) {
		t.Fatalf("FleetDBSettingsDir() = %q, want %q", got, filepath.Dir(runtimeDir))
	}
}
