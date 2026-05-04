// Package bootstrap holds startup-time wiring: where the per-user state
// cache lives, how to resolve the active workspace, how to start an
// embedded fleet-db. Nothing here is "config" in the loom-yaml sense —
// this is bootstrapping infrastructure.
package bootstrap

import (
	"os"
	"path/filepath"
)

// LoomDir returns loom's per-user data directory.
//
// Resolution order:
//  1. LOOM_CONFIG_DIR env var (the directory holds state.json + the
//     embedded fleet-db data dir, not yaml config).
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
