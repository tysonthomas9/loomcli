package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// resolvePath resolves a path relative to baseDir, or returns as-is if absolute.
func resolvePath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, path)
}

// DaemonRuntimeInfo describes the runtime state of a daemon as detected from
// multiple sources (lock file, state file). All daemon lifecycle
// commands should derive liveness from this struct instead of checking
// individual files directly.
//
// Provenance contract: Dir is the ONLY directory callers may use to locate
// this daemon's sidecar files (state file, control socket). Detection may
// resolve a daemon that does not live under the caller's cwd — notably the
// LOOM_WORKSPACE fallback — so re-deriving those paths from os.Getwd()
// describes a different daemon than the one whose liveness was proved.
//
// StartedAt is zero when unknown and MUST be rendered as such ("unknown"),
// never formatted as the zero time.
type DaemonRuntimeInfo struct {
	Running bool   // true if a live daemon process was confirmed
	PID     int    // PID of the daemon (0 if unknown)
	Source  string // which source confirmed liveness: "lock", "state", "workspace-lock", ""
	// StartedAt is the daemon start time as recorded by the same evidence
	// that proved liveness. Zero means unknown.
	StartedAt time.Time
	// Dir is the directory the evidence came from. State file and control
	// socket paths must be derived from this, not from the cwd.
	Dir string
	// Cwd and Socket are the daemon's own project dir and control socket
	// path, when the confirming source knows them. Only the workspace-lock
	// sidecar carries them today; other sources leave them empty, and an
	// empty value means "unknown", not "none".
	Cwd    string
	Socket string
}

// DetectDaemonRuntime resolves daemon liveness from authoritative sources in
// precedence order:
//  1. Lock file (.loom/daemon.lock) — if a valid exclusive lock is held by a
//     live process, the daemon is running.
//  2. State file (.loom/daemon-agents.json) — if the state file contains a PID
//     that is alive, the daemon is running (covers the case where the PID file
//     was removed but the daemon is still up).
//
// A stale lock file with a dead PID is treated as "not running".
func DetectDaemonRuntime(projectDir string) DaemonRuntimeInfo {
	loomDir := filepath.Join(projectDir, ".loom")

	// --- 1. Lock file ---
	lockPath := filepath.Join(loomDir, "daemon.lock")
	if info, ok := detectFromLockFile(lockPath); ok {
		info.Dir = projectDir
		return info
	}

	// --- 2. State file ---
	stateFilePath := config.ResolveDaemonStatePath(projectDir)
	if info, ok := detectFromStateFile(stateFilePath); ok {
		info.Dir = projectDir
		return info
	}

	return DaemonRuntimeInfo{}
}

// detectFromLockFile attempts to probe the daemon lock file. If the lock is
// held by a live process, returns (info, true). Otherwise returns (_, false).
func detectFromLockFile(lockPath string) (DaemonRuntimeInfo, bool) {
	// Try to open the lock file for exclusive locking probe
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0) //nolint:gosec // controlled path
	if err != nil {
		return DaemonRuntimeInfo{}, false
	}
	defer f.Close()

	// Try to acquire the lock non-blocking — if we CAN acquire it, no daemon
	// holds the lock. Release it immediately to avoid blocking a concurrent
	// daemon startup.
	lockErr := lockfile.TryLockExclusive(f)
	if lockErr == nil {
		// We got the lock — no daemon running. Release immediately so a
		// daemon starting concurrently won't see our probe as contention.
		_ = lockfile.FlockUnlock(f)
		return DaemonRuntimeInfo{}, false
	}
	if lockErr != lockfile.ErrLocked {
		// Unexpected error — skip this source
		return DaemonRuntimeInfo{}, false
	}

	// Lock is held — read PID from JSON content
	_, _ = f.Seek(0, 0)
	data := make([]byte, 512)
	n, _ := f.Read(data)
	if n == 0 {
		// Lock held but no content — daemon is running, PID unknown
		return DaemonRuntimeInfo{Running: true, Source: "lock"}, true
	}

	var li lockfile.LockInfo
	if err := json.Unmarshal(data[:n], &li); err == nil && li.PID > 0 {
		if lockfile.IsProcessRunning(li.PID) {
			// StartedAt comes from the record written by the very process we
			// just proved alive while it holds the flock, so it is bound to
			// this daemon's identity.
			return DaemonRuntimeInfo{Running: true, PID: li.PID, Source: "lock", StartedAt: li.StartedAt}, true
		}
		// Lock held but PID is dead — stale lock, treat as not running
		return DaemonRuntimeInfo{}, false
	}

	// Couldn't parse — lock is held so assume running
	return DaemonRuntimeInfo{Running: true, Source: "lock"}, true
}

// daemonStateMinimal is a minimal struct for reading the daemon state file
// without importing the daemon package (avoids import cycle).
type daemonStateMinimal struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

// detectFromStateFile checks the daemon state file for a live PID.
func detectFromStateFile(stateFilePath string) (DaemonRuntimeInfo, bool) {
	data, err := os.ReadFile(stateFilePath) //nolint:gosec // controlled path
	if err != nil {
		return DaemonRuntimeInfo{}, false
	}
	var state daemonStateMinimal
	if err := json.Unmarshal(data, &state); err != nil {
		return DaemonRuntimeInfo{}, false
	}
	if state.PID > 0 && lockfile.IsProcessRunning(state.PID) {
		// This branch is already PID-verified, so started_at belongs to the
		// live daemon rather than a leftover snapshot.
		return DaemonRuntimeInfo{Running: true, PID: state.PID, Source: "state", StartedAt: state.StartedAt}, true
	}
	return DaemonRuntimeInfo{}, false
}

// ProtectedRuntimePaths is the set of top-level directory/file names that must
// never be deleted by cleanup routines (git clean, stash-discard, recovery).
// These paths contain live daemon state or persistent local runtime data.
var ProtectedRuntimePaths = []string{
	".loom",
	"sessions",
	"AGENTS.md",
}

// IsProtectedRuntimePath returns true if relPath (relative to the repo root)
// falls under a protected runtime directory or matches a protected file name.
// The path is normalized to forward slashes for consistent matching.
func IsProtectedRuntimePath(relPath string) bool {
	// Normalize to forward slashes and clean
	clean := filepath.ToSlash(filepath.Clean(relPath))
	// Strip leading "./" if present
	clean = strings.TrimPrefix(clean, "./")

	for _, p := range ProtectedRuntimePaths {
		if clean == p || strings.HasPrefix(clean, p+"/") {
			return true
		}
	}
	return false
}
