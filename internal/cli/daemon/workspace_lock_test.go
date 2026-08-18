package daemon

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// TestAcquireWorkspaceDaemonLockSkipsWhenUnset verifies the single-project
// path: with no LOOM_WORKSPACE in the environment, the workspace lock
// returns (nil, nil) so the daemon falls back to per-cwd protection only.
func TestAcquireWorkspaceDaemonLockSkipsWhenUnset(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	lock, err := acquireWorkspaceDaemonLock()
	if err != nil {
		t.Fatalf("acquireWorkspaceDaemonLock: %v", err)
	}
	if lock != nil {
		lock.Release()
		t.Fatal("expected nil lock when LOOM_WORKSPACE is unset")
	}
}

// TestAcquireWorkspaceDaemonLockWritesPID locks the post-acquire state:
// lock file exists, sidecar daemon.pid contains the current PID, and
// Release cleans the PID sidecar while keeping the stable lock path.
func TestAcquireWorkspaceDaemonLockWritesPID(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)
	t.Setenv("LOOM_WORKSPACE", "playground")

	lock, err := acquireWorkspaceDaemonLock()
	if err != nil {
		t.Fatalf("acquireWorkspaceDaemonLock: %v", err)
	}
	if lock == nil {
		t.Fatal("expected non-nil lock when LOOM_WORKSPACE is set")
	}

	wantLock := filepath.Join(loomDir, "workspaces", "playground", "daemon.lock")
	wantPID := filepath.Join(loomDir, "workspaces", "playground", "daemon.pid")
	if _, statErr := os.Stat(wantLock); statErr != nil {
		t.Fatalf("daemon.lock not created at %s: %v", wantLock, statErr)
	}
	if got := readWorkspacePID(wantPID); got != os.Getpid() {
		t.Errorf("daemon.pid contains pid %d, want %d", got, os.Getpid())
	}

	lock.Release()
	if _, statErr := os.Stat(wantLock); statErr != nil {
		t.Errorf("Release should keep daemon.lock as a stable flock path; stat err=%v", statErr)
	}
	if _, statErr := os.Stat(wantPID); !os.IsNotExist(statErr) {
		t.Errorf("Release should remove daemon.pid; stat err=%v", statErr)
	}
}

// TestAcquireWorkspaceDaemonLockRefusesSecond is the B-DUAL repro:
// two daemons claiming the same workspace from different cwds must
// refuse, naming the first daemon's PID in the error.
func TestAcquireWorkspaceDaemonLockRefusesSecond(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)
	t.Setenv("LOOM_WORKSPACE", "playground")

	first, err := acquireWorkspaceDaemonLock()
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if first == nil {
		t.Fatal("first lock returned nil")
	}
	defer first.Release()

	second, err := acquireWorkspaceDaemonLock()
	if err == nil {
		if second != nil {
			second.Release()
		}
		t.Fatal("second acquire should fail while first lock is held")
	}
	if !errors.Is(err, lockfile.ErrLocked) {
		t.Errorf("second-acquire err = %v, want wraps lockfile.ErrLocked", err)
	}
	if !strings.Contains(err.Error(), "playground") {
		t.Errorf("err should name the workspace: %v", err)
	}
	pidStr := strconv.Itoa(os.Getpid())
	if !strings.Contains(err.Error(), pidStr) {
		t.Errorf("err should name the existing daemon's PID (%s): %v", pidStr, err)
	}
}

// TestAcquireWorkspaceDaemonLockReleasesOnRelease verifies that after
// Release, a subsequent acquire succeeds — proves Release is a real
// flock unlock and not just an in-memory reset.
func TestAcquireWorkspaceDaemonLockReleasesOnRelease(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)
	t.Setenv("LOOM_WORKSPACE", "playground")

	first, err := acquireWorkspaceDaemonLock()
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	first.Release()

	second, err := acquireWorkspaceDaemonLock()
	if err != nil {
		t.Fatalf("acquire after release should succeed: %v", err)
	}
	defer second.Release()
	if second == nil {
		t.Fatal("acquire after release returned nil")
	}
}

// TestDetectDaemonRuntimeForCommandFallsBackToWorkspaceLock verifies that
// daemon status/stop can find a workspace daemon from a different cwd after
// cwd-local runtime detection comes up empty.
func TestDetectDaemonRuntimeForCommandFallsBackToWorkspaceLock(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)
	t.Setenv("LOOM_WORKSPACE", "playground")

	lock, err := acquireWorkspaceDaemonLock()
	if err != nil {
		t.Fatalf("workspace lock: %v", err)
	}
	defer lock.Release()

	rt := detectDaemonRuntimeForCommand(t.TempDir())
	if !rt.Running {
		t.Fatalf("workspace lock is held, but runtime detection reported not running")
	}
	if rt.Source != "workspace-lock" {
		t.Fatalf("runtime source = %q, want workspace-lock", rt.Source)
	}
	if rt.PID != os.Getpid() {
		t.Fatalf("runtime PID = %d, want %d", rt.PID, os.Getpid())
	}
}

func TestDaemonStatusReadsWorkspaceSnapshotWhenRunFromElsewhere(t *testing.T) {
	loomDir := t.TempDir()
	workspaceDir := filepath.Join(t.TempDir(), "workspace")
	elsewhere := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)
	t.Setenv("LOOM_WORKSPACE", "playground")

	if err := bootstrap.MutateWorkspaceLocalState("playground", func(local *bootstrap.WorkspaceLocalState) error {
		local.Path = workspaceDir
		return nil
	}); err != nil {
		t.Fatalf("record workspace path: %v", err)
	}
	stateDir := filepath.Join(workspaceDir, ".loom")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("create workspace state dir: %v", err)
	}
	statePath := filepath.Join(stateDir, "daemon-agents.json")
	startedAt := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	if err := writeStateFile(statePath, startedAt, nil, nil, 3); err != nil {
		t.Fatalf("write workspace daemon state: %v", err)
	}

	lock, err := acquireWorkspaceDaemonLock()
	if err != nil {
		t.Fatalf("workspace lock: %v", err)
	}
	defer lock.Release()

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	if err := os.Chdir(elsewhere); err != nil {
		t.Fatalf("chdir elsewhere: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })
	runDaemonStatus(nil, nil)
	if err := w.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	os.Stdout = oldStdout

	var output bytes.Buffer
	if _, err := output.ReadFrom(r); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	got := output.String()
	if !strings.Contains(got, "Daemon: running (PID "+strconv.Itoa(os.Getpid())+")") {
		t.Fatalf("status did not detect workspace daemon:\n%s", got)
	}
	if !strings.Contains(got, "Started: "+startedAt.Format(time.RFC3339)) || !strings.Contains(got, "Agents: 0") {
		t.Fatalf("status did not read workspace snapshot:\n%s", got)
	}
	if strings.Contains(got, "no agent status available") || strings.Contains(got, elsewhere) {
		t.Fatalf("status still resolved snapshot from caller cwd:\n%s", got)
	}
}
