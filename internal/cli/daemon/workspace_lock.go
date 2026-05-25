package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// Release drops the lock and removes the sidecar files. Safe on nil.
func (w *workspaceDaemonLock) Release() {
	if w == nil {
		return
	}
	if w.lockFile != nil {
		_ = lockfile.FlockUnlock(w.lockFile)
		_ = w.lockFile.Close()
	}
	_ = os.Remove(w.pidPath)
	_ = os.Remove(w.lockPath)
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

// readWorkspacePID best-effort reads the existing daemon's PID from
// the sidecar file. Returns 0 when the file is missing or unreadable.
func readWorkspacePID(path string) int {
	data, err := os.ReadFile(path) //nolint:gosec // user-private sidecar
	if err != nil {
		return 0
	}
	var info workspacePIDFile
	if err := json.Unmarshal(data, &info); err == nil {
		return info.PID
	}
	return 0
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
