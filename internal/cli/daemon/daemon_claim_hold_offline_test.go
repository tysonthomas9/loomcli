package daemon

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

// These tests cover the two halves of PUPPET-203: releasing a hold with NO
// daemon to ask, and surviving a daemon that has written daemon.pid but not yet
// bound daemon.sock. They live apart from daemon_claim_hold_test.go on purpose
// — that file is large and sits on an unmerged branch, so appending to it buys
// merge conflicts for nothing.

// offlineHoldProject prepares a project directory with a .loom/ but no daemon:
// no lock, no state file, no socket, and LOOM_WORKSPACE cleared so the
// workspace-lock fallback cannot see a real daemon on this machine.
func offlineHoldProject(t *testing.T) string {
	t.Helper()
	dir := shortSocketDir(t)
	if err := os.MkdirAll(filepath.Join(dir, ".loom"), 0755); err != nil {
		t.Fatalf("mkdir .loom: %v", err)
	}
	t.Setenv("LOOM_WORKSPACE", "")
	t.Chdir(dir)
	return dir
}

// releaseFlags sets the release command's flag globals for one test.
func releaseFlags(t *testing.T, actor string, force bool) {
	t.Helper()
	prevActor, prevForce := daemonReleaseActor, daemonReleaseForce
	daemonReleaseActor, daemonReleaseForce = actor, force
	t.Cleanup(func() { daemonReleaseActor, daemonReleaseForce = prevActor, prevForce })
}

// writeHold puts a claim-hold record in the project's .loom directory and
// returns its path.
func writeHold(t *testing.T, dir string, h *supervisor.ClaimHold) string {
	t.Helper()
	path := filepath.Join(dir, ".loom", claimHoldFileName)
	if err := writeClaimHoldFile(path, h); err != nil {
		t.Fatalf("writeClaimHoldFile: %v", err)
	}
	return path
}

func TestResolveClaimHoldEndpointsUsesWorkspaceTuple(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "CLAIM-ENDPOINTS")
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Chdir(t.TempDir())

	lock, err := acquireWorkspaceDaemonLock()
	if err != nil {
		t.Fatalf("acquire workspace lock: %v", err)
	}
	if lock == nil {
		t.Fatal("expected workspace lock")
	}
	defer lock.Release()

	daemonDir := t.TempDir()
	socketPath := filepath.Join(daemonDir, ".loom", "daemon.sock")
	holdPath := filepath.Join(daemonDir, ".loom", claimHoldFileName)
	if err := lock.UpdatePaths(daemonDir, socketPath, holdPath); err != nil {
		t.Fatalf("update workspace paths: %v", err)
	}

	ep, err := resolveClaimHoldEndpoints()
	if err != nil {
		t.Fatalf("resolve claim-hold endpoints: %v", err)
	}
	if ep.projectDir != daemonDir || ep.socketPath != socketPath || ep.holdPath() != holdPath {
		t.Fatalf("endpoints = %+v, hold=%q; want project=%q socket=%q hold=%q",
			ep, ep.holdPath(), daemonDir, socketPath, holdPath)
	}
	if ep.source != controlSocketSourceWorkspaceLock {
		t.Fatalf("source = %q, want %q", ep.source, controlSocketSourceWorkspaceLock)
	}
}

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
// The release path reports what it cleared, and that wording is part of the
// contract: an operator has no other signal that the file, not a daemon,
// answered.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prev := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()
	fn()
	os.Stdout = prev
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// ── offline release ─────────────────────────────────────────────────────────

func TestReleaseClearsFileWhenNoDaemon(t *testing.T) {
	dir := offlineHoldProject(t)
	path := writeHold(t, dir, &supervisor.ClaimHold{
		Held: true, Actor: "union-autodeploy", Reason: "deploy",
		Since: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	releaseFlags(t, "union-autodeploy", false)

	var err error
	out := captureStdout(t, func() { err = runDaemonRelease(nil, nil) })
	if err != nil {
		t.Fatalf("release with no daemon: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("claim-hold file still present: %v", statErr)
	}
	if !strings.Contains(out, "no daemon running") || !strings.Contains(out, path) {
		t.Fatalf("output = %q, want it to name the cleared path", out)
	}
}

func TestReleaseIdempotentWithNoFile(t *testing.T) {
	offlineHoldProject(t)
	releaseFlags(t, "union-autodeploy", false)

	var err error
	out := captureStdout(t, func() { err = runDaemonRelease(nil, nil) })
	if err != nil {
		t.Fatalf("release with no file: %v", err)
	}
	if !strings.Contains(out, "not held") {
		t.Fatalf("output = %q, want %q", out, "Claims: not held")
	}
}

func TestReleaseOfflineRefusesForeignActorWithoutForce(t *testing.T) {
	dir := offlineHoldProject(t)
	path := writeHold(t, dir, &supervisor.ClaimHold{
		Held: true, Actor: "union-autodeploy", Reason: "deploy",
		Since: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	releaseFlags(t, "someone-else", false)

	err := runDaemonRelease(nil, nil)
	if err == nil {
		t.Fatal("a foreign actor released the hold without --force")
	}
	// Same wording as supervisor.ReleaseClaimHold: the two paths must be
	// indistinguishable to an operator.
	if !strings.Contains(err.Error(), "claims held by union-autodeploy") ||
		!strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want the socket path's ownership message", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("hold file was removed despite the refusal: %v", statErr)
	}
}

func TestReleaseOfflineForeignActorWithForce(t *testing.T) {
	dir := offlineHoldProject(t)
	path := writeHold(t, dir, &supervisor.ClaimHold{
		Held: true, Actor: "union-autodeploy", Reason: "deploy",
		Since: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	releaseFlags(t, "someone-else", true)

	var err error
	captureStdout(t, func() { err = runDaemonRelease(nil, nil) })
	if err != nil {
		t.Fatalf("forced release: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("claim-hold file still present after --force: %v", statErr)
	}
}

func TestReleaseOfflineCorruptFileNeedsForce(t *testing.T) {
	dir := offlineHoldProject(t)
	path := filepath.Join(dir, ".loom", claimHoldFileName)
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatalf("write corrupt record: %v", err)
	}

	releaseFlags(t, "union-autodeploy", false)
	err := runDaemonRelease(nil, nil)
	if err == nil {
		t.Fatal("an unparseable record was cleared without --force")
	}
	if !strings.Contains(err.Error(), "unparseable") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want it to name the unparseable record and --force", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("corrupt file removed despite the refusal: %v", statErr)
	}

	releaseFlags(t, "union-autodeploy", true)
	captureStdout(t, func() { err = runDaemonRelease(nil, nil) })
	if err != nil {
		t.Fatalf("forced release of an unparseable record: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("corrupt file still present after --force: %v", statErr)
	}
}

// ── waiting for a late control socket ───────────────────────────────────────

// livePIDFile makes claimHoldDaemonRuntime report a running daemon at pid, via
// the state file — the same source DetectDaemonRuntime falls back to when the
// lock file is absent.
func livePIDFile(t *testing.T, dir string, pid int) {
	t.Helper()
	data, err := json.Marshal(struct {
		PID int `json:"pid"`
	}{PID: pid})
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".loom", "daemon-agents.json"), data, 0644); err != nil {
		t.Fatalf("write state file: %v", err)
	}
}

// holdGetRequest is the cheapest control request that exercises a full round
// trip against a real daemon.
func holdGetRequest() DaemonControlRequest {
	return DaemonControlRequest{Operation: ctrlOpClaimHoldGet}
}

func TestDialWaitsForLateSocket(t *testing.T) {
	dir := offlineHoldProject(t)
	livePIDFile(t, dir, os.Getpid())

	ep, err := resolveClaimHoldEndpoints()
	if err != nil {
		t.Fatalf("resolveClaimHoldEndpoints: %v", err)
	}

	// The daemon binds its socket well after the PID is observable — the
	// startup window this whole change exists for.
	d, _ := newHoldTestDaemon(t)
	timer := time.AfterFunc(1500*time.Millisecond, func() {
		_ = d.startControlServer(ep.socketPath)
	})
	t.Cleanup(func() {
		timer.Stop()
		if d.controlListener != nil {
			_ = d.controlListener.Close()
		}
	})

	start := time.Now()
	resp, err := dialClaimHoldSocket(ep, holdGetRequest())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("dial with a late socket: %v", err)
	}
	if !resp.Success {
		t.Fatalf("response failed: %s", resp.Error)
	}
	if elapsed < time.Second {
		t.Fatalf("returned in %s — the socket cannot have been bound yet", elapsed)
	}
	if elapsed >= claimHoldSocketWait {
		t.Fatalf("waited %s, want well under %s", elapsed, claimHoldSocketWait)
	}
}

func TestDialGivesUpWhenDaemonExits(t *testing.T) {
	dir := offlineHoldProject(t)

	// A real process, so lockfile.IsProcessRunning has something true to say
	// before it becomes false.
	// A fixed, argument-free command: the test needs a PID that is genuinely
	// alive and then genuinely gone, which no DI seam can fake.
	cmd := exec.Command("sleep", "30") //nolint:norawexec // fixed test fixture, no user input
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	livePIDFile(t, dir, cmd.Process.Pid)
	reaped := make(chan struct{})
	time.AfterFunc(600*time.Millisecond, func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait() // reap, or the zombie still answers kill(pid, 0)
		close(reaped)
	})
	t.Cleanup(func() {
		select {
		case <-reaped:
		case <-time.After(5 * time.Second):
			t.Error("sleep process was never reaped")
		}
	})

	ep, err := resolveClaimHoldEndpoints()
	if err != nil {
		t.Fatalf("resolveClaimHoldEndpoints: %v", err)
	}
	start := time.Now()
	_, err = dialClaimHoldSocket(ep, holdGetRequest())
	if !errors.Is(err, errNoDaemonRunning) {
		t.Fatalf("err = %v, want errNoDaemonRunning once the daemon PID died", err)
	}
	if elapsed := time.Since(start); elapsed >= claimHoldSocketWait {
		t.Fatalf("waited the full %s for a dead daemon", elapsed)
	}
}

func TestDialNoDaemonReturnsImmediately(t *testing.T) {
	offlineHoldProject(t)
	ep, err := resolveClaimHoldEndpoints()
	if err != nil {
		t.Fatalf("resolveClaimHoldEndpoints: %v", err)
	}

	start := time.Now()
	_, err = dialClaimHoldSocket(ep, holdGetRequest())
	elapsed := time.Since(start)
	if !errors.Is(err, errNoDaemonRunning) {
		t.Fatalf("err = %v, want errNoDaemonRunning", err)
	}
	// The point of the typed error: no daemon means no wait at all.
	if elapsed > 2*time.Second {
		t.Fatalf("stalled %s with nothing running", elapsed)
	}
}
