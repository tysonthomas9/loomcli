// Package bootstrap holds startup-time wiring: where the per-user state
// cache lives, how to resolve the active workspace, how to start an
// embedded fleet-db. Nothing here is "config" in the loom-yaml sense —
// this is bootstrapping infrastructure.
package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

// LoomDir returns loom's per-user data directory.
//
// Resolution order (production):
//  1. LOOM_CONFIG_DIR env var (the directory holds state.json + the
//     embedded fleet-db data dir, not yaml config).
//  2. $HOME/.loom
//
// In production, returns "" if the home directory cannot be resolved
// AND the env var is unset; callers should treat that as a fatal
// bootstrap error.
//
// Under `go test`, falling back to $HOME/.loom would let an unprotected
// test atomically overwrite the user's real state.json (see LOOM-11).
// To make that class of bug fail loud, this function panics when called
// from a test binary with LOOM_CONFIG_DIR unset — tests must set
// LOOM_CONFIG_DIR (typically `t.Setenv("LOOM_CONFIG_DIR", t.TempDir())`)
// before invoking any bootstrap state API.
func LoomDir() string {
	if dir := os.Getenv("LOOM_CONFIG_DIR"); dir != "" {
		return dir
	}
	if testing.Testing() {
		panic("bootstrap.LoomDir: refusing to fall back to $HOME/.loom under go test — set LOOM_CONFIG_DIR (e.g. t.Setenv(\"LOOM_CONFIG_DIR\", t.TempDir())) before calling bootstrap state APIs in tests")
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
