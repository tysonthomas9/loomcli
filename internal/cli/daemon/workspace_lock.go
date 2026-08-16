package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// workspaceDaemonLock holds a flock at <LoomDir>/workspaces/<ws>/daemon.lock
// plus a sibling daemon.pid file. Together they prevent a second
// `loom daemon` from supervising the same workspace from a different
// cwd, which the existing per-cwd daemon.lock cannot detect (each cwd
// has its own lock file).
//
// Only allocated when LOOM_WORKSPACE is set; single-project mode keeps
// the historical per-cwd lock as the sole protection.
type workspaceDaemonLock struct {
	lockPath string
	pidPath  string
	lockFile *os.File
}

// acquireWorkspaceDaemonLock takes the workspace-scoped lock when a
// workspace is configured. Returns (nil, nil) when no workspace is in
// scope — caller continues with cwd-only protection.
//
// On collision, returns an error naming the existing daemon's PID so
// the caller can present an actionable message.
func acquireWorkspaceDaemonLock() (*workspaceDaemonLock, error) {
	workspace := strings.TrimSpace(os.Getenv("LOOM_WORKSPACE"))
	if workspace == "" {
		return nil, nil
	}
	wsDir := cfgpkg.GetWorkspaceDir(workspace)
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		return nil, fmt.Errorf("workspace lock: mkdir %s: %w", wsDir, err)
	}
	lockPath := filepath.Join(wsDir, "daemon.lock")
	pidPath := filepath.Join(wsDir, "daemon.pid")

	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644) //nolint:gosec // user-private lock
	if err != nil {
		return nil, fmt.Errorf("workspace lock: open %s: %w", lockPath, err)
	}
	if err := lockfile.TryLockExclusive(lf); err != nil {
		_ = lf.Close()
		if errors.Is(err, lockfile.ErrLocked) {
			return nil, &workspaceLockBusyError{
				workspace:    workspace,
				lockPath:     lockPath,
				existingPID:  readWorkspacePID(pidPath),
				wrappedError: err,
			}
		}
		return nil, fmt.Errorf("workspace lock: acquire %s: %w", lockPath, err)
	}

	if err := writeWorkspacePID(pidPath, os.Getpid()); err != nil {
		_ = lockfile.FlockUnlock(lf)
		_ = lf.Close()
		return nil, fmt.Errorf("workspace lock: write %s: %w", pidPath, err)
	}

	return &workspaceDaemonLock{
		lockPath: lockPath,
		pidPath:  pidPath,
		lockFile: lf,
	}, nil
}

// Release drops the lock and removes the PID sidecar. Safe on nil.
//
// The lock file itself intentionally remains on disk. Removing a flocked file
// after unlock can delete a successor daemon's newly-acquired lock path during
// handoff, allowing a third daemon to create and lock a different inode.
func (w *workspaceDaemonLock) Release() {
	if w == nil {
		return
	}
	if w.lockFile != nil {
		_ = os.Remove(w.pidPath)
		_ = lockfile.FlockUnlock(w.lockFile)
		_ = w.lockFile.Close()
		w.lockFile = nil
	}
}

// workspaceLockBusyError carries the existing daemon's PID so callers
// can render a message of the form "daemon already supervising
// workspace X (pid N)". The wrapped lockfile.ErrLocked stays
// recoverable via errors.Is.
type workspaceLockBusyError struct {
	workspace    string
	lockPath     string
	existingPID  int
	wrappedError error
}

func (e *workspaceLockBusyError) Error() string {
	if e.existingPID > 0 {
		return fmt.Sprintf("daemon already supervising workspace %q (pid %d, lock %s). "+
			"Stop the existing daemon with `kill %d` or unset LOOM_WORKSPACE to use the per-cwd lock only.",
			e.workspace, e.existingPID, e.lockPath, e.existingPID)
	}
	return fmt.Sprintf("daemon already supervising workspace %q (pid unknown, lock %s). "+
		"Inspect %s for the running daemon or unset LOOM_WORKSPACE to use the per-cwd lock only.",
		e.workspace, e.lockPath, e.lockPath)
}

func (e *workspaceLockBusyError) Unwrap() error {
	return e.wrappedError
}

// detectWorkspaceDaemonRuntime probes the workspace-scoped daemon lock.
// It is used as a fallback for commands run from a different cwd than the
// daemon that owns the active LOOM_WORKSPACE.
func detectWorkspaceDaemonRuntime() cli.DaemonRuntimeInfo {
	workspace := strings.TrimSpace(os.Getenv("LOOM_WORKSPACE"))
	if workspace == "" {
		return cli.DaemonRuntimeInfo{}
	}
	wsDir := cfgpkg.GetWorkspaceDir(workspace)
	lockPath := filepath.Join(wsDir, "daemon.lock")
	pidPath := filepath.Join(wsDir, "daemon.pid")

	lf, err := os.OpenFile(lockPath, os.O_RDWR, 0) //nolint:gosec // user-private lock
	if err != nil {
		return cli.DaemonRuntimeInfo{}
	}
	defer func() { _ = lf.Close() }()

	lockErr := lockfile.TryLockExclusive(lf)
	if lockErr == nil {
		_ = lockfile.FlockUnlock(lf)
		return cli.DaemonRuntimeInfo{}
	}
	if !errors.Is(lockErr, lockfile.ErrLocked) {
		return cli.DaemonRuntimeInfo{}
	}

	// wsDir is the provenance: the live workspace supervisor keeps its
	// daemon-agents.json, daemon.lock and daemon.sock under <wsDir>/.loom,
	// which is generally NOT the caller's cwd.
	info := readWorkspacePIDFile(pidPath)
	if info.PID > 0 && lockfile.IsProcessRunning(info.PID) {
		return cli.DaemonRuntimeInfo{
			Running:   true,
			PID:       info.PID,
			Source:    "workspace-lock",
			StartedAt: info.StartedAt,
			Dir:       wsDir,
		}
	}
	// Lock held but the PID sidecar is missing, unreadable or dead: the
	// daemon is running, but nothing here identifies it. StartedAt stays
	// zero (unknown) rather than borrowing an unverified timestamp.
	return cli.DaemonRuntimeInfo{Running: true, Source: "workspace-lock", Dir: wsDir}
}

// readWorkspacePID best-effort reads the existing daemon's PID from
// the sidecar file. Returns 0 when the file is missing or unreadable.
func readWorkspacePID(path string) int {
	return readWorkspacePIDFile(path).PID
}

// readWorkspacePIDFile best-effort reads the whole daemon PID sidecar. It is
// the single parser for the file; a missing or unparseable file yields the
// zero value (PID 0, zero StartedAt).
func readWorkspacePIDFile(path string) workspacePIDFile {
	data, err := os.ReadFile(path) //nolint:gosec // user-private sidecar
	if err != nil {
		return workspacePIDFile{}
	}
	var info workspacePIDFile
	if err := json.Unmarshal(data, &info); err != nil {
		return workspacePIDFile{}
	}
	return info
}

// writeWorkspacePID stores the daemon PID as JSON so the format is
// extensible without a parser rewrite.
func writeWorkspacePID(path string, pid int) error {
	data, err := json.Marshal(workspacePIDFile{PID: pid, StartedAt: time.Now()})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644) //nolint:gosec // user-private sidecar
}

type workspacePIDFile struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}
