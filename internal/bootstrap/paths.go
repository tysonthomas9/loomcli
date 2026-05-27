// Package bootstrap holds startup-time wiring: where the per-user state
// cache lives, how to resolve the active workspace, how to start an
// embedded fleet-db. Nothing here is "config" in the loom-yaml sense —
// this is bootstrapping infrastructure.
package bootstrap

import (
	"os"
	"path/filepath"
)

// EnvFleetDBRuntimeDir overrides the host-level embedded FleetDB runtime
// directory. The directory contains embedded.lock, runtime.json, and the
// miniredis snapshot. Tests set this to avoid touching the operator's
// real host runtime.
const EnvFleetDBRuntimeDir = "LOOM_FLEET_DB_RUNTIME_DIR"

// LoomDir returns loom's per-user data directory.
//
// Resolution order:
//  1. LOOM_CONFIG_DIR env var (the directory holds state.json + the
//     per-client local state, not the host-level embedded FleetDB runtime).
//  2. $HOME/.loom
//
// Returns "" if the home directory cannot be resolved AND the env var
// is unset; callers should treat that as a fatal bootstrap error.
func LoomDir() string {
	if dir := os.Getenv("LOOM_CONFIG_DIR"); dir != "" {
		return dir
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
