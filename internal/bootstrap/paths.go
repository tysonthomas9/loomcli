// Package bootstrap holds startup-time wiring: where the per-user state
// cache lives, how to resolve the active workspace, how to start an
// embedded fleet-db. Nothing here is "config" in the loom-yaml sense —
// this is bootstrapping infrastructure.
package bootstrap

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// testLoomDir lazily creates a per-process temp config dir used when
// running under `go test` without LOOM_CONFIG_DIR. One dir per process
// keeps state coherent across Save/Load calls within a test binary.
var testLoomDir = sync.OnceValue(func() string {
	dir, err := os.MkdirTemp("", "loom-test-config-*")
	if err != nil {
		return ""
	}
	return dir
})

// EnvFleetDBRuntimeDir overrides the host-level embedded FleetDB runtime
// directory. The directory contains embedded.lock, runtime.json, and the
// miniredis snapshot. Tests set this to avoid touching the operator's
// real host runtime.
const EnvFleetDBRuntimeDir = "LOOM_FLEET_DB_RUNTIME_DIR"

// LoomDir returns loom's per-user data directory.
//
// Resolution order:
//  1. LOOM_CONFIG_DIR env var (the directory holds state.json + the
//     per-client local state, not yaml config or the host-level embedded
//     FleetDB runtime).
//  2. Under `go test` (testing.Testing()): a per-process temp dir —
//     tests must NEVER touch the real ~/.loom. Note this guard does not
//     extend to subprocesses a test spawns; tests that exec the loom
//     binary must pass LOOM_CONFIG_DIR explicitly.
//  3. $HOME/.loom
//
// Returns "" if the home directory cannot be resolved AND the env var
// is unset (or temp-dir creation fails under test); callers should
// treat that as a fatal bootstrap error.
func LoomDir() string {
	if dir := os.Getenv("LOOM_CONFIG_DIR"); dir != "" {
		return dir
	}
	if testing.Testing() {
		return testLoomDir()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".loom")
}

// defaultLoomDir returns the host-level default Loom data directory
// without consulting LOOM_CONFIG_DIR. Embedded FleetDB uses this stable
// base so starting `loom serve` under a foreign LOOM_CONFIG_DIR does not
// create a second local FleetDB instance.
func defaultLoomDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".loom")
}

// FleetDBRuntimeDir returns the host-level embedded FleetDB runtime
// directory. Unlike LoomDir, it intentionally ignores LOOM_CONFIG_DIR so
// multiple local clients share the same embedded FleetDB owner.
func FleetDBRuntimeDir() string {
	if dir := os.Getenv(EnvFleetDBRuntimeDir); dir != "" {
		return dir
	}
	if dir := defaultLoomDir(); dir != "" {
		return filepath.Join(dir, "fleet-db")
	}
	return ""
}

// FleetDBSettingsDir returns the directory that owns FleetDB-local
// settings such as embedded Redis configuration. It is the parent of the
// runtime directory so an override can keep runtime metadata and settings
// in the same isolated tree.
func FleetDBSettingsDir() string {
	runtimeDir := FleetDBRuntimeDir()
	if runtimeDir == "" {
		return ""
	}
	return filepath.Dir(runtimeDir)
}

// StateFilePath returns the absolute path to the per-user state cache.
// The file is created on first write — callers that read a missing file
// should treat it as an empty StateCache.
func StateFilePath() string {
	dir := LoomDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "state.json")
}

// WorkspaceDir returns the default on-disk checkout root for a workspace of the
// given name: <LoomDir>/workspaces/<name>. It is the single source of truth for
// this layout — config.GetWorkspaceDir delegates here, and path self-heal
// re-derives it. Returns "" when the loom dir cannot be resolved.
func WorkspaceDir(name string) string {
	dir := LoomDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "workspaces", name)
}
