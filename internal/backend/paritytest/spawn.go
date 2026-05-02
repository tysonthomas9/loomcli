//go:build parity

package paritytest

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec" //nolint:norawexec // parity harness must spawn real bd + fleet-db subprocesses
	"path/filepath"
	"syscall"

	"github.com/tysonthomas9/loomcli/internal/netutil"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// fleetSpawnTimeout bounds how long we wait for the fleet-db /healthz endpoint.
const fleetSpawnTimeout = 20 * time.Second

// parityActor is the canonical actor string used for fleet-db fixture runs.
const parityActor = "parity-harness"

// defaultWorkspaceID is the workspace key created on the fleet-db side. The
// parity harness creates it before running issue operations.
const defaultWorkspaceID = "PARITY"

// terminateProcess gracefully shuts down a child process started with
// plain exec.Command: send SIGTERM, wait up to `timeout` for a clean exit,
// then SIGKILL and give the OS a short window to reap so we don't leave
// zombies. cmd.Wait() is called exactly once via a single goroutine — the
// outer select guards against a process that refuses to die, but we still
// hand the OS enough time to clean the PID.
//
// The Wait error is intentionally discarded: the process was being
// terminated, so a non-zero exit is expected and not actionable here.
func terminateProcess(cmd *exec.Cmd, timeout time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	// Best-effort graceful shutdown — ignore errors (process may already
	// have exited between Start and our signal).
	_ = cmd.Process.Signal(syscall.SIGTERM)

	select {
	case <-done:
		return
	case <-time.After(timeout):
	}

	// Graceful window expired — escalate to SIGKILL and give the OS a
	// short window to reap. If the process still doesn't go away we return
	// anyway; the goroutine stays alive but the child is definitely dead
	// per OS semantics of SIGKILL.
	_ = cmd.Process.Kill()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
	}
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
	be, cleanup, _ := spawnFleetDBWithRedis(t)
	return be, cleanup
}

func spawnFleetDBWithRedis(t *testing.T) (backend.IssueBackend, func(), *miniredis.Miniredis) {
	t.Helper()

	binary := fleetDBBinary(t)
	mr := startMiniRedis(t)
	port := pickFreePortOrFatal(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	cmd, logPath := startFleetDBProcess(t, binary, addr, mr.Addr())

	baseURL := "http://" + addr
	healthCtx, healthCancel := context.WithTimeout(context.Background(), fleetSpawnTimeout)
	if err := netutil.WaitForHealthz(healthCtx, baseURL, time.Second); err != nil {
		healthCancel()
		logDump, _ := os.ReadFile(logPath) // #nosec G304 — test log diagnostic
		t.Logf("fleet-db log:\n%s", string(logDump))
		terminateProcess(cmd, 3*time.Second)
		t.Fatalf("fleet-db did not become healthy in %s: %v", fleetSpawnTimeout, err)
	}
	healthCancel()
	if err := createFleetWorkspace(baseURL, defaultWorkspaceID); err != nil {
		terminateProcess(cmd, 3*time.Second)
		t.Fatalf("create workspace: %v", err)
	}

	be := newFleetDBAdapter(baseURL, defaultWorkspaceID, parityActor)
	cleanup := func() {
		terminateProcess(cmd, 3*time.Second)
	}
	t.Cleanup(cleanup)

	return be, cleanup, mr
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

// pickFreePortOrFatal wraps netutil.PickFreeLoopbackPort with test fatalling.
func pickFreePortOrFatal(t *testing.T) int {
	t.Helper()
	_, port, err := netutil.PickFreeLoopbackPort()
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
//
// Lifecycle is caller-managed via terminateProcess — we avoid
// exec.CommandContext to prevent racing on cmd.Wait() from its internal
// goroutine.
func startFleetDBProcess(t *testing.T, binary, addr, redisAddr string) (*exec.Cmd, string) {
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
	cmd := exec.Command(binary, args...) //nolint:norawexec,gosec
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fleet-db: %v", err)
	}
	return cmd, logPath
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
//
// env is passed verbatim to cmd.Env. Callers that need ambient inheritance
// must pass `append(os.Environ(), "KEY=val")` — there is no implicit merge
// here because silently inheriting the parent's env (or silently not) is
// exactly the bug this signature now makes explicit.
func runOrFail(t *testing.T, bin string, args []string, env []string, opts ...func(*runCmdOpts)) {
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
		cmd.Env = env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %v: %v\nstdout: %s\nstderr: %s", bin, args, err, stdout.String(), stderr.String())
	}
}
