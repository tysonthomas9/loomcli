package backends

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// Thread-safe package-level state for active session env vars.
// Set by the parent loom process before invoking an agent, cleared after.
var (
	sessionEnvMu      sync.RWMutex
	sessionRuntimeDir string
	sessionID         string
)

// Thread-safe package-level state for Claude session resume.
// Set by auto-mode before invoking InvokeNonInteractive, consumed (read+cleared)
// inside the invoker. Follows the same pattern as activeSessionEnv above.
var (
	resumeMu              sync.RWMutex
	resumeSessionID       string
	lastCapturedSessionID string
)

// SetResumeSessionID sets the Claude session ID to resume on the next
// non-interactive invocation. Thread-safe.
func SetResumeSessionID(id string) {
	resumeMu.Lock()
	defer resumeMu.Unlock()
	resumeSessionID = id
}

// GetResumeSessionID returns the current resume session ID. Thread-safe.
func GetResumeSessionID() string {
	resumeMu.RLock()
	defer resumeMu.RUnlock()
	return resumeSessionID
}

// ClearResumeSessionID clears the resume session ID. Thread-safe.
func ClearResumeSessionID() {
	resumeMu.Lock()
	defer resumeMu.Unlock()
	resumeSessionID = ""
}

// consumeResumeSessionID atomically reads and clears the resume session ID.
func consumeResumeSessionID() string {
	resumeMu.Lock()
	defer resumeMu.Unlock()
	id := resumeSessionID
	resumeSessionID = ""
	return id
}

// SetLastCapturedSessionID stores the Claude session ID captured from the most
// recent non-interactive invocation's stream output. Thread-safe.
func SetLastCapturedSessionID(id string) {
	resumeMu.Lock()
	defer resumeMu.Unlock()
	lastCapturedSessionID = id
}

// GetLastCapturedSessionID returns the session ID captured from the most recent
// non-interactive invocation. Thread-safe.
func GetLastCapturedSessionID() string {
	resumeMu.RLock()
	defer resumeMu.RUnlock()
	return lastCapturedSessionID
}

// ClearLastCapturedSessionID clears the last captured session ID. Thread-safe.
func ClearLastCapturedSessionID() {
	resumeMu.Lock()
	defer resumeMu.Unlock()
	lastCapturedSessionID = ""
}

// Thread-safe package-level state for non-local runtime metadata (e.g. a Flue
// Daytona-per-task sandbox). Set by the backend invoker during the run and read
// by the session finalizer, mirroring lastCapturedSessionID above.
var (
	runtimeMetaMu       sync.RWMutex
	lastRuntimeMetadata *sessions.RuntimeMetadata
)

// SetLastRuntimeMetadata records the sandbox/runtime metadata for the most
// recent non-interactive invocation so the finalizer can attach it to the
// session record. Thread-safe.
func SetLastRuntimeMetadata(m *sessions.RuntimeMetadata) {
	runtimeMetaMu.Lock()
	defer runtimeMetaMu.Unlock()
	lastRuntimeMetadata = m
}

// GetLastRuntimeMetadata returns the runtime metadata captured from the most
// recent non-interactive invocation, or nil for ordinary local runs. Thread-safe.
func GetLastRuntimeMetadata() *sessions.RuntimeMetadata {
	runtimeMetaMu.RLock()
	defer runtimeMetaMu.RUnlock()
	return lastRuntimeMetadata
}

// ClearLastRuntimeMetadata clears the captured runtime metadata. Thread-safe.
func ClearLastRuntimeMetadata() {
	runtimeMetaMu.Lock()
	defer runtimeMetaMu.Unlock()
	lastRuntimeMetadata = nil
}

// SetActiveSessionRuntimeEnv sets the workspace runtime directory and session ID that will be
// injected into agent subprocess environments. Thread-safe.
func SetActiveSessionRuntimeEnv(runtimeDir, sid string) {
	sessionEnvMu.Lock()
	defer sessionEnvMu.Unlock()
	sessionRuntimeDir = runtimeDir
	sessionID = sid
}

// ClearActiveSessionEnv clears the active session env vars. Thread-safe.
func ClearActiveSessionEnv() {
	sessionEnvMu.Lock()
	defer sessionEnvMu.Unlock()
	sessionRuntimeDir = ""
	sessionID = ""
}

// GetActiveSessionRuntimeEnv returns the current workspace runtime directory and session ID.
// Thread-safe.
func GetActiveSessionRuntimeEnv() (runtimeDir, sid string) {
	sessionEnvMu.RLock()
	defer sessionEnvMu.RUnlock()
	return sessionRuntimeDir, sessionID
}

// activeSessionEnvVars returns a slice of "KEY=VALUE" strings for any
// non-empty session env vars. Used by backend_claude.go (and other backends)
// when constructing subprocess environments.
func activeSessionEnvVars() []string {
	sessionEnvMu.RLock()
	defer sessionEnvMu.RUnlock()

	var vars []string
	if sessionRuntimeDir != "" {
		vars = append(vars, "LOOM_WORKSPACE_RUNTIME_DIR="+sessionRuntimeDir)
	}
	if sessionID != "" {
		vars = append(vars, "LOOM_SESSION_ID="+sessionID)
	}
	return vars
}

// resolveWebUIURL returns the local webui server URL for session notifications.
// Uses LOOM_WEBUI_URL env if set, otherwise defaults to http://127.0.0.1:8080.
func ResolveWebUIURL() string {
	if url := os.Getenv("LOOM_WEBUI_URL"); url != "" {
		return url
	}
	return "http://127.0.0.1:8080"
}

// resolveNotifyToken returns the bearer token for authenticating to the
// POST /api/sessions/notify endpoint. Checks LOOM_NOTIFY_TOKEN env var first,
// then falls back to reading <workspace_runtime_dir>/notify.token from disk.
// Returns empty string if both fail (server will reject with 403).
func ResolveNotifyToken() string {
	if token := os.Getenv("LOOM_NOTIFY_TOKEN"); token != "" {
		return token
	}
	runtimeDir := cli.GetWorkspaceRuntimeDir()
	if runtimeDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(runtimeDir, "notify.token")) //nolint:gosec // runtimeDir from cli.GetWorkspaceRuntimeDir(), filename is constant
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
