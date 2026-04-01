package cli

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Thread-safe package-level state for active session env vars.
// Set by the parent loom process before invoking an agent, cleared after.
var (
	sessionEnvMu    sync.RWMutex
	sessionBeadsDir string
	sessionID       string
)

// SetActiveSessionEnv sets the beads directory and session ID that will be
// injected into agent subprocess environments. Thread-safe.
func SetActiveSessionEnv(beadsDir, sid string) {
	sessionEnvMu.Lock()
	defer sessionEnvMu.Unlock()
	sessionBeadsDir = beadsDir
	sessionID = sid
}

// ClearActiveSessionEnv clears the active session env vars. Thread-safe.
func ClearActiveSessionEnv() {
	sessionEnvMu.Lock()
	defer sessionEnvMu.Unlock()
	sessionBeadsDir = ""
	sessionID = ""
}

// GetActiveSessionEnv returns the current beads directory and session ID.
// Thread-safe.
func GetActiveSessionEnv() (beadsDir, sid string) {
	sessionEnvMu.RLock()
	defer sessionEnvMu.RUnlock()
	return sessionBeadsDir, sessionID
}

// activeSessionEnvVars returns a slice of "KEY=VALUE" strings for any
// non-empty session env vars. Used by backend_claude.go (and other backends)
// when constructing subprocess environments.
func activeSessionEnvVars() []string {
	sessionEnvMu.RLock()
	defer sessionEnvMu.RUnlock()

	var vars []string
	if sessionBeadsDir != "" {
		vars = append(vars, "LOOM_BEADS_DIR="+sessionBeadsDir)
	}
	if sessionID != "" {
		vars = append(vars, "LOOM_SESSION_ID="+sessionID)
	}
	return vars
}

// resolveWebUIURL returns the local webui server URL for session notifications.
// Uses LOOM_WEBUI_URL env if set, otherwise defaults to http://127.0.0.1:8080.
func resolveWebUIURL() string {
	if url := os.Getenv("LOOM_WEBUI_URL"); url != "" {
		return url
	}
	return "http://127.0.0.1:8080"
}

// resolveNotifyToken returns the bearer token for authenticating to the
// POST /api/sessions/notify endpoint. Checks LOOM_NOTIFY_TOKEN env var first,
// then falls back to reading <beads_dir>/notify.token from disk.
// Returns empty string if both fail (server will reject with 403).
func resolveNotifyToken() string {
	if token := os.Getenv("LOOM_NOTIFY_TOKEN"); token != "" {
		return token
	}
	beadsDir := GetBeadsDir()
	if beadsDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(beadsDir, "notify.token")) //nolint:gosec // beadsDir from GetBeadsDir(), filename is constant
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
