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

// TestWorkspacePIDCarriesSocketPath covers the whole point of the sidecar
// extension: once the daemon records its paths, a control command standing in
// an unrelated cwd can still find the socket, and detectWorkspaceDaemonRuntime
// hands those paths back together with liveness.
func TestWorkspacePIDCarriesSocketPath(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "PIDSOCKET")
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	lock, err := acquireWorkspaceDaemonLock()
	if err != nil {
		t.Fatalf("acquireWorkspaceDaemonLock: %v", err)
	}
	if lock == nil {
		t.Fatal("expected a workspace lock when LOOM_WORKSPACE is set")
	}
	defer lock.Release()

	projectDir := t.TempDir()
	socketPath := filepath.Join(projectDir, ".loom", "daemon.sock")
	holdPath := filepath.Join(projectDir, ".loom", "claim-hold.json")
	if err := lock.UpdatePaths(projectDir, socketPath, holdPath); err != nil {
		t.Fatalf("UpdatePaths: %v", err)
	}

	info, ok := readWorkspacePIDFile(lock.pidPath)
	if !ok {
		t.Fatalf("sidecar %s did not parse after UpdatePaths", lock.pidPath)
	}
	if info.Socket != socketPath {
		t.Errorf("socket = %q, want %q", info.Socket, socketPath)
	}
	if info.Cwd != projectDir {
		t.Errorf("cwd = %q, want %q", info.Cwd, projectDir)
	}
	if info.ClaimHold != holdPath {
		t.Errorf("claim_hold_path = %q, want %q", info.ClaimHold, holdPath)
	}
	// The PID written when the lock was taken must survive the second write:
	// callers use it for liveness.
	if info.PID != os.Getpid() {
		t.Errorf("pid = %d, want %d", info.PID, os.Getpid())
	}

	// The sidecar is the lookup path used while fleet-db is unavailable, so
	// runtime detection must surface the paths alongside Running/PID.
	rt := detectWorkspaceDaemonRuntime()
	if !rt.Running {
		t.Fatal("detectWorkspaceDaemonRuntime: expected Running with the lock held")
	}
	if rt.Source != "workspace-lock" {
		t.Errorf("source = %q, want %q", rt.Source, "workspace-lock")
	}
	if rt.Socket != socketPath {
		t.Errorf("runtime socket = %q, want %q", rt.Socket, socketPath)
	}
	if rt.Cwd != projectDir {
		t.Errorf("runtime cwd = %q, want %q", rt.Cwd, projectDir)
	}
}

// TestWorkspacePIDFileParsesOldSidecar pins backward compatibility: a sidecar
// written by a daemon that predates the Cwd/Socket/ClaimHold fields must still
// parse, leaving them empty rather than failing the read outright. Callers then
// treat the empty socket as "unknown" and fall back to the cwd-derived path.
func TestWorkspacePIDFileParsesOldSidecar(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "daemon.pid")
	const old = `{"pid":4242,"started_at":"2026-08-27T18:02:31.928721+02:00"}`
	if err := os.WriteFile(pidPath, []byte(old), 0o644); err != nil {
		t.Fatalf("write old sidecar: %v", err)
	}

	info, ok := readWorkspacePIDFile(pidPath)
	if !ok {
		t.Fatal("old sidecar failed to parse")
	}
	if info.PID != 4242 {
		t.Errorf("pid = %d, want 4242", info.PID)
	}
	if info.Socket != "" || info.Cwd != "" || info.ClaimHold != "" {
		t.Errorf("expected empty new fields, got socket=%q cwd=%q claim_hold=%q", info.Socket, info.Cwd, info.ClaimHold)
	}
	if got := readWorkspacePID(pidPath); got != 4242 {
		t.Errorf("readWorkspacePID = %d, want 4242", got)
	}

	// Round trip: annotating an old sidecar preserves its PID and start time
	// while adding the new fields.
	if err := updateWorkspacePID(pidPath, "/proj", "/proj/.loom/daemon.sock", "/proj/.loom/claim-hold.json"); err != nil {
		t.Fatalf("updateWorkspacePID: %v", err)
	}
	updated, ok := readWorkspacePIDFile(pidPath)
	if !ok {
		t.Fatal("updated sidecar failed to parse")
	}
	if updated.PID != 4242 {
		t.Errorf("pid after update = %d, want 4242 (preserved)", updated.PID)
	}
	if updated.StartedAt != info.StartedAt {
		t.Errorf("started_at after update = %v, want %v (preserved)", updated.StartedAt, info.StartedAt)
	}
	if updated.Socket != "/proj/.loom/daemon.sock" {
		t.Errorf("socket after update = %q", updated.Socket)
	}
}

// TestResolveControlSocketForCommandFallsBackToWorkspaceLock verifies the
// resolution order the hold/release commands depend on: an existing socket in
// cwd wins, and otherwise the workspace-lock sidecar supplies the path.
func TestResolveControlSocketForCommandFallsBackToWorkspaceLock(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "SOCKFALLBACK")
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	// A cwd with no .loom/daemon.sock — the situation `cd /tmp && loom daemon
	// release` used to fail in.
	cwd := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(prev) }()

	lock, err := acquireWorkspaceDaemonLock()
	if err != nil {
		t.Fatalf("acquireWorkspaceDaemonLock: %v", err)
	}
	if lock == nil {
		t.Fatal("expected a workspace lock when LOOM_WORKSPACE is set")
	}
	defer lock.Release()

	// Before the daemon records anything, resolution stays on the cwd path.
	got, source, err := resolveControlSocketForCommand()
	if err != nil {
		t.Fatalf("resolveControlSocketForCommand: %v", err)
	}
	if source != controlSocketSourceCwd {
		t.Errorf("source = %q, want %q", source, controlSocketSourceCwd)
	}
	if !strings.HasSuffix(got, filepath.Join(".loom", "daemon.sock")) {
		t.Errorf("socket = %q, want a cwd-derived .loom/daemon.sock", got)
	}

	daemonDir := t.TempDir()
	socketPath := filepath.Join(daemonDir, ".loom", "daemon.sock")
	if err := lock.UpdatePaths(daemonDir, socketPath, filepath.Join(daemonDir, ".loom", "claim-hold.json")); err != nil {
		t.Fatalf("UpdatePaths: %v", err)
	}

	got, source, err = resolveControlSocketForCommand()
	if err != nil {
		t.Fatalf("resolveControlSocketForCommand: %v", err)
	}
	if got != socketPath {
		t.Errorf("socket = %q, want %q", got, socketPath)
	}
	if source != controlSocketSourceWorkspaceLock {
		t.Errorf("source = %q, want %q", source, controlSocketSourceWorkspaceLock)
	}
	if want := "socket " + socketPath + " (source: workspace-lock)"; describeControlSocket(got, source) != want {
		t.Errorf("describeControlSocket = %q, want %q", describeControlSocket(got, source), want)
	}
}

// TestResolveControlSocketForCommandWithoutWorkspace pins the unchanged
// single-project behavior: with no LOOM_WORKSPACE there is no workspace lock
// to consult, so resolution is exactly the cwd path it has always been.
func TestResolveControlSocketForCommandWithoutWorkspace(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	cwdPath, err := resolveControlSocketFromCwd()
	if err != nil {
		t.Fatalf("resolveControlSocketFromCwd: %v", err)
	}
	got, source, err := resolveControlSocketForCommand()
	if err != nil {
		t.Fatalf("resolveControlSocketForCommand: %v", err)
	}
	if got != cwdPath {
		t.Errorf("socket = %q, want cwd path %q", got, cwdPath)
	}
	if source != controlSocketSourceCwd {
		t.Errorf("source = %q, want %q", source, controlSocketSourceCwd)
	}
}
