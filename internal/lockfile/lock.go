// Package lockfile provides loom's advisory file-locking primitives with
// per-platform implementations (unix/windows/wasm), a liveness check on a PID,
// and the daemon.lock protocol: its JSON metadata (PID, database, version) and
// a cheap "is a daemon already running" probe callers use before paying for an
// RPC timeout. Used by internal/cli/daemon and its supervisor, internal/rpc,
// sessions, usage, and internal/configlock.
package lockfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LockInfo represents the metadata stored in the daemon.lock file
type LockInfo struct {
	PID       int       `json:"pid"`
	ParentPID int       `json:"parent_pid,omitempty"`
	Database  string    `json:"database"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"started_at"`
}

// TryDaemonLock attempts to acquire and immediately release the daemon lock
// to check if a daemon is running. Returns true if daemon is running.
//
// This is a cheap probe operation that should be called before attempting
// RPC connections to avoid unnecessary connection timeouts.
func TryDaemonLock(runtimeDir string) (running bool, pid int) {
	lockPath := filepath.Join(runtimeDir, "daemon.lock")

	// Open lock file with read-write access (required for LockFileEx on Windows)
	// #nosec G304 - controlled path from config
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	if err != nil {
		return false, 0
	}
	defer func() { _ = f.Close() }()

	// Try to acquire lock non-blocking
	if err := flockExclusive(f); err != nil {
		if err == errDaemonLocked {
			// Lock is held - daemon is running
			// Try to read PID from JSON format (best effort)
			_, _ = f.Seek(0, 0)
			var lockInfo LockInfo
			if err := json.NewDecoder(f).Decode(&lockInfo); err == nil {
				pid = lockInfo.PID
			}
			return true, pid
		}
		// Other errors mean we can't determine status
		return false, 0
	}

	// We got the lock - no daemon running
	// Release immediately (file close will do this)
	return false, 0
}

// ReadLockInfo reads and parses the daemon lock file
// Returns lock info if available, or error if file doesn't exist or can't be parsed
func ReadLockInfo(runtimeDir string) (*LockInfo, error) {
	lockPath := filepath.Join(runtimeDir, "daemon.lock")

	// #nosec G304 - controlled path from config
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return nil, err
	}

	var lockInfo LockInfo
	if err := json.Unmarshal(data, &lockInfo); err != nil {
		return nil, fmt.Errorf("cannot parse lock file: %w", err)
	}

	return &lockInfo, nil
}
