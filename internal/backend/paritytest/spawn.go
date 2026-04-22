//go:build parity

package paritytest

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec" //nolint:norawexec // parity harness must spawn real bd + fleet-db subprocesses
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/tysonthomas9/loomcli/internal/backend"
	beadsbackend "github.com/tysonthomas9/loomcli/internal/backend/beads"
	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// beadsSpawnTimeout bounds how long we wait for `bd daemon start` to come up.
const beadsSpawnTimeout = 20 * time.Second

// fleetSpawnTimeout bounds how long we wait for the fleet-db /healthz endpoint.
const fleetSpawnTimeout = 20 * time.Second

// parityActor is the canonical actor string both backends use for fixture runs.
// Git user.email on the beads side and X-Actor header on the fleet-db side are
// both set to this so `created_by`/`actor` fields line up across backends.
const parityActor = "parity-harness"

// defaultWorkspaceID is the workspace key created on the fleet-db side. The
// beads side doesn't have a workspace concept so no matching setup is needed.
const defaultWorkspaceID = "PARITY"

// spawnBeads starts a bd daemon in a fresh temp workspace and returns a
// BeadsBackend wired to its socket. The daemon process is stopped via
// t.Cleanup on test exit. See fleet-db/test/parity/beads_caller.go for the
// reference pattern — this version yields a real *rpc.Client instead of
// shelling out to bd for each call.
//
// Preconditions:
//   - `bd` is on PATH (install via `make install-bd`)
//   - `git` is on PATH
//
// The workspace dir is isolated per test (t.TempDir()) so daemons don't
// collide. BD_ACTOR is forced to parityActor; a deterministic git identity
// is set in the workspace to match.
func spawnBeads(t *testing.T) (backend.IssueBackend, func()) {
	t.Helper()

	dir := t.TempDir()
	checkBeadsPrereqs(t)
	initBeadsWorkspace(t, dir)

	// Start the daemon in foreground so killing the context terminates it.
	ctx, cancel := context.WithCancel(context.Background())
	daemonCmd := startBeadsDaemon(t, ctx, dir, cancel)

	socketPath := rpc.ShortSocketPath(dir)
	client, err := waitForBeadsSocket(socketPath, beadsSpawnTimeout)
	if err != nil {
		if buf, ok := daemonCmd.Stderr.(*bytes.Buffer); ok && buf.Len() > 0 {
			t.Logf("bd daemon stderr: %s", buf.String())
		}
		cancel()
		_ = daemonCmd.Wait()
		t.Fatalf("bd daemon did not become ready in %s: %v", beadsSpawnTimeout, err)
	}
	client.SetActor(parityActor)

	be := beadsbackend.New(client)
	cleanup := func() {
		_ = client.Close()
		cancel()
		waitForProcessExit(daemonCmd, 2*time.Second)
	}
	t.Cleanup(cleanup)

	return be, cleanup
}

// checkBeadsPrereqs ensures bd and git are on PATH before we start spawning.
func checkBeadsPrereqs(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bd"); err != nil {
		t.Fatalf("bd not on PATH (run `make install-bd`): %v", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git not on PATH: %v", err)
	}
}

// initBeadsWorkspace sets up the directory as a git repo + beads workspace.
func initBeadsWorkspace(t *testing.T, dir string) {
	t.Helper()
	runOrFail(t, "git", []string{"-C", dir, "init", "-q"}, nil)
	runOrFail(t, "git", []string{"-C", dir, "config", "user.email", parityActor}, nil)
	runOrFail(t, "git", []string{"-C", dir, "config", "user.name", parityActor}, nil)
	runOrFail(t, "bd", []string{"init"}, map[string]string{
		"BD_ACTOR": parityActor,
		"HOME":     os.Getenv("HOME"),
		"PATH":     os.Getenv("PATH"),
	}, withDir(dir))
}

// startBeadsDaemon launches bd daemon in the given workspace. Caller owns
// the ctx cancel — it will SIGKILL the daemon when cancelled.
func startBeadsDaemon(t *testing.T, ctx context.Context, dir string, cancel context.CancelFunc) *exec.Cmd {
	t.Helper()
	daemonCmd := exec.CommandContext(ctx, "bd", "daemon", "start", "--foreground", "--local") //nolint:norawexec
	daemonCmd.Dir = dir
	daemonCmd.Env = append(os.Environ(), "BD_ACTOR="+parityActor)
	daemonCmd.Stdout = &bytes.Buffer{}
	daemonCmd.Stderr = &bytes.Buffer{}
	if err := daemonCmd.Start(); err != nil {
		cancel()
		t.Fatalf("bd daemon start: %v", err)
	}
	return daemonCmd
}

// waitForProcessExit blocks up to timeout for cmd.Wait() to return, then
// returns unconditionally. Used to give SIGKILL'd subprocesses a moment to
// tear down cleanly before the test function returns.
func waitForProcessExit(cmd *exec.Cmd, timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

// waitForBeadsSocket polls until the bd RPC socket is live, or the deadline
// expires. Returns a connected rpc.Client on success.
func waitForBeadsSocket(socketPath string, timeout time.Duration) (*rpc.Client, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		client, err := rpc.TryConnectWithTimeout(socketPath, 500*time.Millisecond)
		if err == nil && client != nil {
			return client, nil
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("socket %s never became live", socketPath)
}

// spawnFleetDB starts an embedded miniredis + a fleet-db subprocess on a
// random free port, then creates a workspace so issue operations succeed.
// Returns a paritytest-internal fleet-db HTTP adapter (see fleetadapter.go).
//
// Why not loomcli's fleet.New()?
//
// loomcli's internal/backend/fleet.FleetBackend targets the loom-webui
// server's "/api/workspaces/{ws}/..." prefix with a custom
// {"success":true,"data":...} envelope. Real fleet-db serves
// "/api/v1/{ws}/..." with bare JSON responses (or {"error":{...}} for
// failures). Pointing FleetBackend directly at fleet-db does not work. The
// parity harness therefore uses a tiny in-package adapter that speaks
// fleet-db's actual API. Extending loomcli's FleetBackend to also speak
// fleet-db directly, or building a proxy that translates both, is out of
// scope for the MVP and is called out in the task followups.
//
// Preconditions:
//   - fleet-db binary exists at /tmp/fleet-db (built via
//     `cd ~/codebase/fleet-db && go build -o /tmp/fleet-db ./cmd/fleet-db`)
func spawnFleetDB(t *testing.T) (backend.IssueBackend, func()) {
	t.Helper()

	binary := fleetDBBinary(t)
	mr := startMiniRedis(t)
	port := pickFreePortOrFatal(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	ctx, cancel := context.WithCancel(context.Background())
	cmd, logPath := startFleetDBProcess(t, ctx, binary, addr, mr.Addr())

	baseURL := "http://" + addr
	if err := waitForHealthz(baseURL, fleetSpawnTimeout); err != nil {
		logDump, _ := os.ReadFile(logPath) // #nosec G304 — test log diagnostic
		t.Logf("fleet-db log:\n%s", string(logDump))
		cancel()
		_ = cmd.Wait()
		t.Fatalf("fleet-db did not become healthy in %s: %v", fleetSpawnTimeout, err)
	}
	if err := createFleetWorkspace(baseURL, defaultWorkspaceID); err != nil {
		cancel()
		_ = cmd.Wait()
		t.Fatalf("create workspace: %v", err)
	}

	be := newFleetDBAdapter(baseURL, defaultWorkspaceID, parityActor)
	cleanup := func() {
		cancel()
		waitForProcessExit(cmd, 3*time.Second)
	}
	t.Cleanup(cleanup)

	return be, cleanup
}

// fleetDBBinary resolves which fleet-db binary to spawn and skips the test
// if it doesn't exist. FLEETDB_BIN overrides the default /tmp/fleet-db.
func fleetDBBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("FLEETDB_BIN")
	if binary == "" {
		binary = "/tmp/fleet-db"
	}
	if _, err := os.Stat(binary); err != nil {
		t.Skipf("fleet-db binary not found at %s (set FLEETDB_BIN or build: cd ~/codebase/fleet-db && go build -o /tmp/fleet-db ./cmd/fleet-db): %v", binary, err)
	}
	return binary
}

// startMiniRedis starts an in-process miniredis and registers cleanup.
func startMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	return mr
}

// pickFreePortOrFatal wraps pickFreePort with test fatalling.
func pickFreePortOrFatal(t *testing.T) int {
	t.Helper()
	port, err := pickFreePort()
	if err != nil {
		t.Fatalf("pick free port: %v", err)
	}
	return port
}

// startFleetDBProcess spawns fleet-db with the parity-harness baseline flag
// set: auth-dev-mode so X-Actor works, everything optional (compaction,
// retention, archive, authz, rate-limit) disabled so we aren't diffing
// cron-driven noise. Output goes to a log file in t.TempDir() so the test
// can dump it on failure without polluting test output on success.
func startFleetDBProcess(t *testing.T, ctx context.Context, binary, addr, redisAddr string) (*exec.Cmd, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "fleet-db.log")
	logFile, err := os.Create(logPath) // #nosec G304 — log path is inside t.TempDir()
	if err != nil {
		t.Fatalf("create fleet-db log: %v", err)
	}
	t.Cleanup(func() { _ = logFile.Close() })

	args := []string{
		"--addr=" + addr,
		"--redis-addr=" + redisAddr,
		"--redis-dial-timeout=2s",
		"--redis-max-retries=0",
		"--redis-cb-fail-threshold=0",
		"--redis-durability-profile=managed",
		"--rpc-enabled=false",
		"--log-format=text",
		"--log-level=warn",
		"--auth-enabled=true",
		"--auth-dev-mode=true",
		"--authz-enabled=false",
		"--rate-limit-enabled=false",
		"--compaction-enabled=false",
		"--retention-enabled=false",
		"--archive-enabled=false",
	}
	cmd := exec.CommandContext(ctx, binary, args...) //nolint:norawexec,gosec
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fleet-db: %v", err)
	}
	return cmd, logPath
}

// pickFreePort grabs an ephemeral port by binding to :0, closing the listener,
// and returning the port. A brief race window exists before fleet-db binds,
// but in practice the OS won't reassign the port that quickly on a quiet box.
func pickFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port, nil
}

// waitForHealthz polls GET /healthz until it returns 200 or the deadline
// expires. Used to serialize spawnFleetDB returns with server readiness.
func waitForHealthz(baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 1 * time.Second}
	url := baseURL + "/healthz"
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("healthz returned %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("healthz never succeeded")
}

// runCmdOpts configures runOrFail.
type runCmdOpts struct {
	dir string
}

// withDir overrides the child process's working directory.
func withDir(dir string) func(*runCmdOpts) {
	return func(o *runCmdOpts) { o.dir = dir }
}

// runOrFail executes a command and t.Fatals on error. Used only in the
// spawn helpers; each caller explicitly opts in via //nolint:norawexec on
// the exec.Command line above.
func runOrFail(t *testing.T, bin string, args []string, env map[string]string, opts ...func(*runCmdOpts)) {
	t.Helper()

	o := runCmdOpts{}
	for _, fn := range opts {
		fn(&o)
	}

	cmd := exec.Command(bin, args...) //nolint:norawexec,gosec
	if o.dir != "" {
		cmd.Dir = o.dir
	}
	if env != nil {
		cmd.Env = envFromMap(env)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %v: %v\nstdout: %s\nstderr: %s", bin, args, err, stdout.String(), stderr.String())
	}
}

func envFromMap(m map[string]string) []string {
	env := make([]string, 0, len(m))
	for k, v := range m {
		env = append(env, k+"="+v)
	}
	return env
}
