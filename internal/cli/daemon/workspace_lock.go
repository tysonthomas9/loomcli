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

// UpdatePaths records the daemon's project dir, control socket and claim hold
// file in the PID sidecar. Safe on nil (single-project mode has no workspace
// lock, and control commands then fall back to the cwd-derived socket).
func (w *workspaceDaemonLock) UpdatePaths(cwd, socket, claimHold string) error {
	if w == nil || w.pidPath == "" {
		return nil
	}
	return updateWorkspacePID(w.pidPath, cwd, socket, claimHold)
}

// Release drops the lock and marks the PID sidecar stopped. Safe on nil.
//
// The lock file itself intentionally remains on disk. Removing a flocked file
// after unlock can delete a successor daemon's newly-acquired lock path during
// handoff, allowing a third daemon to create and lock a different inode.
// The sidecar's resolved paths also intentionally remain: an offline
// `daemon release` may run from another cwd after the daemon has stopped and
// still needs to find the persistent claim-hold file for this workspace.
func (w *workspaceDaemonLock) Release() {
	if w == nil {
		return
	}
	if w.lockFile != nil {
		_ = markWorkspacePIDStopped(w.pidPath)
		_ = lockfile.FlockUnlock(w.lockFile)
		_ = w.lockFile.Close()
		w.lockFile = nil
	}
}

func markWorkspacePIDStopped(path string) error {
	info, ok := readWorkspacePIDFile(path)
	if !ok {
		return nil
	}
	info.PID = 0
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644) //nolint:gosec // user-private sidecar
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

	info, _ := readWorkspacePIDFile(pidPath)
	rt := cli.DaemonRuntimeInfo{
		Running: true,
		Source:  "workspace-lock",
		Cwd:     info.Cwd,
		Socket:  info.Socket,
	}
	if info.PID > 0 && lockfile.IsProcessRunning(info.PID) {
		rt.PID = info.PID
	}
	return rt
}

// readWorkspacePID best-effort reads the existing daemon's PID from
// the sidecar file. Returns 0 when the file is missing or unreadable.
func readWorkspacePID(path string) int {
	info, ok := readWorkspacePIDFile(path)
	if !ok {
		return 0
	}
	return info.PID
}

// readWorkspacePIDFile parses the whole sidecar. Returns ok=false when the
// file is missing or not valid JSON. A sidecar written before the Cwd/Socket/
// ClaimHold fields existed parses fine, leaving those fields empty.
func readWorkspacePIDFile(path string) (workspacePIDFile, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // user-private sidecar
	if err != nil {
		return workspacePIDFile{}, false
	}
	var info workspacePIDFile
	if err := json.Unmarshal(data, &info); err != nil {
		return workspacePIDFile{}, false
	}
	return info, true
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

// updateWorkspacePID records the daemon's resolved paths in the sidecar so
// control commands can find the socket from any cwd. It preserves the PID and
// StartedAt already written by acquireWorkspaceDaemonLock — that call happens
// before resolveDaemonPaths' results are known here, so the paths arrive in a
// second write rather than the first.
//
// Deliberately NOT routed through daemonregistry: the hold has to work while
// fleet-db is being redeployed, so the lookup path must stay filesystem-only.
func updateWorkspacePID(path, cwd, socket, claimHold string) error {
	info, ok := readWorkspacePIDFile(path)
	if !ok {
		info = workspacePIDFile{PID: os.Getpid(), StartedAt: time.Now()}
	}
	info.Cwd = cwd
	info.Socket = socket
	info.ClaimHold = claimHold
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644) //nolint:gosec // user-private sidecar
}

// workspacePIDFile is the JSON shape of the daemon.pid sidecar.
//
// Everything past PID/StartedAt is optional and omitempty: a sidecar written
// by an older daemon has none of these fields and must still parse, with the
// new fields left empty. Readers must therefore treat an empty Socket as
// "unknown", never as "no socket".
type workspacePIDFile struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	Cwd       string    `json:"cwd,omitempty"`             // daemon project dir
	Socket    string    `json:"socket,omitempty"`          // control socket path
	ClaimHold string    `json:"claim_hold_path,omitempty"` // claim hold state file
}
