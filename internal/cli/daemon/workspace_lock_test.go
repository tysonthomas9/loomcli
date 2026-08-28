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
	want, _ := readWorkspacePIDFile(filepath.Join(wsDir, "daemon.pid"))
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

	if got, _ := readWorkspacePIDFile(filepath.Join(dir, "absent.pid")); got.PID != 0 || !got.StartedAt.IsZero() {
		t.Errorf("missing file = %+v, want zero value", got)
	}

	garbage := filepath.Join(dir, "garbage.pid")
	if err := os.WriteFile(garbage, []byte("<<<not json>>>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := readWorkspacePIDFile(garbage); got.PID != 0 || !got.StartedAt.IsZero() {
		t.Errorf("garbage file = %+v, want zero value", got)
	}

	// readWorkspacePID stays a thin view over the same parser.
	if got := readWorkspacePID(garbage); got != 0 {
		t.Errorf("readWorkspacePID(garbage) = %d, want 0", got)
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

// TestResolveClaimHoldEndpoints_FromWorkspaceSidecar is the regression guard
// for the merge of PUPPET-202's endpoint set with PUPPET-204's cwd-independent
// resolution: standing in a socket-less cwd, the WHOLE endpoint set — socket,
// project dir and hold file — must come from the one workspace-lock sidecar.
// Resolving them from different places is how `loom daemon release` dials one
// daemon and clears another workspace's hold.
func TestResolveClaimHoldEndpoints_FromWorkspaceSidecar(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "HOLDENDPOINTS")
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

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

	daemonDir := t.TempDir()
	socketPath := filepath.Join(daemonDir, ".loom", "daemon.sock")
	holdPath := filepath.Join(daemonDir, ".loom", "claim-hold.json")
	if err := lock.UpdatePaths(daemonDir, socketPath, holdPath); err != nil {
		t.Fatalf("UpdatePaths: %v", err)
	}

	ep, err := resolveClaimHoldEndpoints()
	if err != nil {
		t.Fatalf("resolveClaimHoldEndpoints: %v", err)
	}
	if ep.socketPath != socketPath {
		t.Errorf("socketPath = %q, want %q", ep.socketPath, socketPath)
	}
	if ep.holdPath() != holdPath {
		t.Errorf("holdPath() = %q, want %q", ep.holdPath(), holdPath)
	}
	if ep.projectDir != daemonDir {
		t.Errorf("projectDir = %q, want %q", ep.projectDir, daemonDir)
	}
	if ep.source != controlSocketSourceWorkspaceLock {
		t.Errorf("source = %q, want %q", ep.source, controlSocketSourceWorkspaceLock)
	}
}
