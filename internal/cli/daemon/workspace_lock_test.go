package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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

// TestDetectWorkspaceDaemonRuntime_CarriesProvenance is the PUPPET-57 half of
// the workspace fallback: when this probe is what proves liveness, it must
// also say WHICH daemon it proved (Dir) and when that daemon started, because
// the caller's cwd describes a different — possibly dead — daemon.
func TestDetectWorkspaceDaemonRuntime_CarriesProvenance(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)
	t.Setenv("LOOM_WORKSPACE", "provenance")

	lock, err := acquireWorkspaceDaemonLock()
	if err != nil {
		t.Fatalf("acquireWorkspaceDaemonLock: %v", err)
	}
	if lock == nil {
		t.Fatal("expected a lock when LOOM_WORKSPACE is set")
	}
	defer lock.Release()

	wsDir := filepath.Join(loomDir, "workspaces", "provenance")
	want := readWorkspacePIDFile(filepath.Join(wsDir, "daemon.pid"))
	if want.PID != os.Getpid() {
		t.Fatalf("daemon.pid PID = %d, want %d", want.PID, os.Getpid())
	}
	if want.StartedAt.IsZero() {
		t.Fatal("daemon.pid started_at is zero; the workspace fallback would have no start time to report")
	}

	rt := detectWorkspaceDaemonRuntime()
	if !rt.Running || rt.PID != os.Getpid() {
		t.Fatalf("expected the held workspace lock to be detected live, got %+v", rt)
	}
	if rt.Dir != wsDir {
		t.Errorf("Dir = %q, want %q — sidecar files must be located under the workspace dir", rt.Dir, wsDir)
	}
	if !rt.StartedAt.Equal(want.StartedAt) {
		t.Errorf("StartedAt = %v, want %v (from daemon.pid)", rt.StartedAt, want.StartedAt)
	}
	if rt.Source != "workspace-lock" {
		t.Errorf("Source = %q, want %q", rt.Source, "workspace-lock")
	}
}

// TestDetectWorkspaceDaemonRuntime_DeadPIDHasZeroStartedAt: a sidecar naming a
// dead PID identifies nothing, so the probe keeps its historical
// Running=true/PID=0 shape and must not report that corpse's start time.
func TestDetectWorkspaceDaemonRuntime_DeadPIDHasZeroStartedAt(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)
	t.Setenv("LOOM_WORKSPACE", "deadpid")

	wsDir := filepath.Join(loomDir, "workspaces", "deadpid")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The exact corpse from the PUPPET-57 report: a dead PID with a start
	// time six days old.
	if err := os.WriteFile(filepath.Join(wsDir, "daemon.pid"),
		[]byte(`{"pid":999999999,"started_at":"2026-08-09T18:23:08+02:00"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Hold the workspace flock from a separate open file description so the
	// probe observes a genuinely locked file.
	lockPath := filepath.Join(wsDir, "daemon.lock")
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lf.Close() }()
	if err := lockfile.TryLockExclusive(lf); err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}

	rt := detectWorkspaceDaemonRuntime()
	if !rt.Running {
		t.Fatalf("a held workspace lock means a daemon is running, got %+v", rt)
	}
	if rt.PID != 0 {
		t.Errorf("PID = %d, want 0 — a dead sidecar PID identifies nothing", rt.PID)
	}
	if !rt.StartedAt.IsZero() {
		t.Errorf("StartedAt = %v, want zero — the corpse's start time must not be reported", rt.StartedAt)
	}
	if rt.Dir != wsDir {
		t.Errorf("Dir = %q, want %q", rt.Dir, wsDir)
	}
}

// TestReadWorkspacePIDFile_MissingAndGarbage: the single parser must never
// return half-parsed metadata that a caller could mistake for evidence.
func TestReadWorkspacePIDFile_MissingAndGarbage(t *testing.T) {
	dir := t.TempDir()

	if got := readWorkspacePIDFile(filepath.Join(dir, "absent.pid")); got.PID != 0 || !got.StartedAt.IsZero() {
		t.Errorf("missing file = %+v, want zero value", got)
	}

	garbage := filepath.Join(dir, "garbage.pid")
	if err := os.WriteFile(garbage, []byte("<<<not json>>>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readWorkspacePIDFile(garbage); got.PID != 0 || !got.StartedAt.IsZero() {
		t.Errorf("garbage file = %+v, want zero value", got)
	}

	// readWorkspacePID stays a thin view over the same parser.
	if got := readWorkspacePID(garbage); got != 0 {
		t.Errorf("readWorkspacePID(garbage) = %d, want 0", got)
	}
}
