package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

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
type DaemonRuntimeInfo struct {
	Running bool   // true if a live daemon process was confirmed
	PID     int    // PID of the daemon (0 if unknown)
	Source  string // which source confirmed liveness: "lock", "state", ""
}

type daemonLockProbe uint8

const (
	daemonLockAbsent daemonLockProbe = iota
	daemonLockUnlocked
	daemonLockHeld
)

var daemonProcessIdentityFn = lockfile.IsLoomDaemonProcess
var daemonProcessIdentitySupportedFn = lockfile.DaemonProcessIdentitySupported

// DetectDaemonRuntime resolves daemon liveness from authoritative sources in
// precedence order:
//  1. Lock file (.loom/daemon.lock) — if a valid exclusive lock is held by a
//     process, the daemon is running. Exact process identity determines
//     whether its PID is safe to return for lifecycle signaling.
//  2. State file (.loom/daemon-agents.json) — if the state file contains a PID
//     whose Loom-daemon identity can be verified, status may report the daemon
//     running with an unknown PID (covers a removed lock path without granting
//     state-only signal authority).
//
// An existing unlocked lock is authoritative "not running" evidence. A held
// lock with stale PID metadata remains running with an unknown PID.
func DetectDaemonRuntime(projectDir string) DaemonRuntimeInfo {
	loomDir := filepath.Join(projectDir, ".loom")

	// --- 1. Lock file ---
	lockPath := filepath.Join(loomDir, "daemon.lock")
	if info, probe := probeDaemonLockFile(lockPath); probe == daemonLockHeld {
		return info
	} else if probe == daemonLockUnlocked {
		// An existing lock file that we can lock is authoritative evidence
		// that no daemon owns this workspace. Do not fall through to a stale
		// state file whose PID may have been reused by an unrelated process.
		return DaemonRuntimeInfo{}
	}

	// --- 2. State file ---
	stateFilePath := config.ResolveDaemonStatePath(projectDir)
	if info, ok := detectFromStateFile(stateFilePath); ok {
		return info
	}

	return DaemonRuntimeInfo{}
}

// detectFromLockFile attempts to probe the daemon lock file. If the lock is
// held by a live process, returns (info, true). Otherwise returns (_, false).
func detectFromLockFile(lockPath string) (DaemonRuntimeInfo, bool) {
	info, probe := probeDaemonLockFile(lockPath)
	return info, probe == daemonLockHeld
}

func probeDaemonLockFile(lockPath string) (DaemonRuntimeInfo, daemonLockProbe) {
	// Try to open the lock file for exclusive locking probe
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0) //nolint:gosec // controlled path
	if err != nil {
		return DaemonRuntimeInfo{}, daemonLockAbsent
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
		return DaemonRuntimeInfo{}, daemonLockUnlocked
	}
	if lockErr != lockfile.ErrLocked {
		// Unexpected error — skip this source
		return DaemonRuntimeInfo{}, daemonLockAbsent
	}

	info := DaemonRuntimeInfo{Running: true, Source: "lock"}

	// Lock is held — read PID from JSON content
	_, _ = f.Seek(0, 0)
	data := make([]byte, 512)
	n, _ := f.Read(data)
	if n == 0 {
		// Lock held but no content — daemon is running, PID unknown
		return info, daemonLockHeld
	}

	var li lockfile.LockInfo
	if err := json.Unmarshal(data[:n], &li); err == nil && li.PID > 0 {
		if daemonProcessIdentityFn(li.PID) ||
			(!daemonProcessIdentitySupportedFn() && lockfile.IsProcessRunning(li.PID)) {
			info.PID = li.PID
		}
	}

	// The flock itself is authoritative. Malformed, dead, or foreign PID
	// metadata stays unknown so lifecycle commands never signal that PID.
	return info, daemonLockHeld
}

// daemonStateMinimal is a minimal struct for reading the daemon state file
// without importing the daemon package (avoids import cycle).
type daemonStateMinimal struct {
	PID int `json:"pid"`
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
	if state.PID > 0 && daemonProcessIdentitySupportedFn() && daemonProcessIdentityFn(state.PID) {
		// State is not an ownership primitive and does not bind the process to
		// this workspace. It may confirm liveness for status, but never return
		// a signalable PID; lifecycle commands require the held lock source.
		return DaemonRuntimeInfo{Running: true, Source: "state"}, true
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
