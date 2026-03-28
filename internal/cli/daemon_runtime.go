package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// DaemonRuntimeInfo describes the runtime state of a daemon as detected from
// multiple sources (lock file, state file, PID file). All daemon lifecycle
// commands should derive liveness from this struct instead of checking
// individual files directly.
type DaemonRuntimeInfo struct {
	Running bool   // true if a live daemon process was confirmed
	PID     int    // PID of the daemon (0 if unknown)
	Source  string // which source confirmed liveness: "lock", "state", "pid", ""
}

// DetectDaemonRuntime resolves daemon liveness from authoritative sources in
// precedence order:
//  1. Lock file (.loom/daemon.lock) — if a valid exclusive lock is held by a
//     live process, the daemon is running.
//  2. State file (.loom/daemon-agents.json) — if the state file contains a PID
//     that is alive, the daemon is running (covers the case where the PID file
//     was removed but the daemon is still up).
//  3. PID file (.loom/daemon.pid) — backward-compatible fallback for older
//     daemons that don't write lock/state files.
//
// A stale lock file with a dead PID is treated as "not running".
func DetectDaemonRuntime(projectDir string) DaemonRuntimeInfo {
	loomDir := filepath.Join(projectDir, ".loom")

	// --- 1. Lock file ---
	lockPath := filepath.Join(loomDir, "daemon.lock")
	if info, ok := detectFromLockFile(lockPath); ok {
		return info
	}

	// --- 2. State file ---
	stateFilePath := ResolveDaemonStatePath(projectDir)
	if info, ok := detectFromStateFile(stateFilePath); ok {
		return info
	}

	// --- 3. PID file (backward-compatible fallback) ---
	config, err := LoadDaemonConfig(projectDir)
	if err != nil {
		config = &DaemonConfig{
			Daemon: DaemonSettings{
				PIDFile: ".loom/daemon.pid",
			},
		}
	}
	pidFilePath := resolveDaemonPath(projectDir, config.Daemon.PIDFile)
	if info, ok := detectFromPIDFile(pidFilePath); ok {
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
			return DaemonRuntimeInfo{Running: true, PID: li.PID, Source: "lock"}, true
		}
		// Lock held but PID is dead — stale lock, treat as not running
		return DaemonRuntimeInfo{}, false
	}

	// Couldn't parse — lock is held so assume running
	return DaemonRuntimeInfo{Running: true, Source: "lock"}, true
}

// detectFromStateFile checks the daemon state file for a live PID.
func detectFromStateFile(stateFilePath string) (DaemonRuntimeInfo, bool) {
	state, err := readStateFile(stateFilePath)
	if err != nil || state == nil {
		return DaemonRuntimeInfo{}, false
	}
	if state.PID > 0 && lockfile.IsProcessRunning(state.PID) {
		return DaemonRuntimeInfo{Running: true, PID: state.PID, Source: "state"}, true
	}
	return DaemonRuntimeInfo{}, false
}

// detectFromPIDFile checks the PID file for a live process.
func detectFromPIDFile(pidFilePath string) (DaemonRuntimeInfo, bool) {
	data, err := os.ReadFile(pidFilePath) //nolint:gosec // controlled path
	if err != nil {
		return DaemonRuntimeInfo{}, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return DaemonRuntimeInfo{}, false
	}
	if lockfile.IsProcessRunning(pid) {
		return DaemonRuntimeInfo{Running: true, PID: pid, Source: "pid"}, true
	}
	return DaemonRuntimeInfo{}, false
}

// protectedRuntimePaths is the set of top-level directory/file names that must
// never be deleted by cleanup routines (git clean, stash-discard, recovery).
// These paths contain live daemon state, persistent storage, or required config.
var protectedRuntimePaths = []string{
	".beads",
	".loom",
	"sessions",
	"loom.yaml",
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

	for _, p := range protectedRuntimePaths {
		if clean == p || strings.HasPrefix(clean, p+"/") {
			return true
		}
	}
	return false
}
