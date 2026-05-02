package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
	"github.com/tysonthomas9/loomcli/internal/netutil"
	"github.com/tysonthomas9/loomcli/internal/webui/localredis"
)

// EnvFleetDBBin overrides the fleet-db binary path. When set, binary
// discovery skips PATH and the standard locations.
const EnvFleetDBBin = "FLEET_DB_BIN"

// fleetDBBinName is the executable name used in PATH lookup and standard
// install locations.
const fleetDBBinName = "fleet-db"

// FleetDBBinaryDiagnostic describes embedded fleet-db binary discovery in a
// form suitable for doctor output and startup errors.
type FleetDBBinaryDiagnostic struct {
	Path        string
	Checked     []string
	Runnable    bool
	ProbeOutput string
	Err         error
	Remediation string
}

type embeddedRuntimeLock struct {
	path string
	file *os.File
}

func acquireEmbeddedRuntimeLock(fleetDir string) (*embeddedRuntimeLock, error) {
	path := filepath.Join(fleetDir, "embedded.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600) //nolint:gosec // user-private runtime lock
	if err != nil {
		return nil, fmt.Errorf("embedded: open runtime lock %s: %w", path, err)
	}
	if err := lockfile.TryLockExclusive(f); err != nil {
		_ = f.Close()
		if errors.Is(err, lockfile.ErrLocked) {
			return nil, fmt.Errorf("embedded: fleet-db is already running for this loom data dir (lock %s is held); set %s to a shared fleet-db URL for concurrent clients or stop the existing local process", path, EnvFleetDBURL)
		}
		return nil, fmt.Errorf("embedded: lock runtime %s: %w", path, err)
	}
	if err := f.Truncate(0); err != nil {
		_ = lockfile.FlockUnlock(f)
		_ = f.Close()
		return nil, fmt.Errorf("embedded: truncate runtime lock %s: %w", path, err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = lockfile.FlockUnlock(f)
		_ = f.Close()
		return nil, fmt.Errorf("embedded: seek runtime lock %s: %w", path, err)
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		_ = lockfile.FlockUnlock(f)
		_ = f.Close()
		return nil, fmt.Errorf("embedded: write runtime lock %s: %w", path, err)
	}
	return &embeddedRuntimeLock{path: path, file: f}, nil
}

func (l *embeddedRuntimeLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	var err error
	if unlockErr := lockfile.FlockUnlock(l.file); unlockErr != nil {
		err = unlockErr
	}
	if closeErr := l.file.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	l.file = nil
	if err != nil {
		return fmt.Errorf("%s: %w", l.path, err)
	}
	return nil
}

// healthCheckTimeout caps the per-attempt HTTP timeout while polling
// /healthz. Long enough for slow startup, short enough to detect a
// hung subprocess quickly.
const healthCheckTimeout = 2 * time.Second

// startupTimeout caps the total wait between subprocess launch and the
// first successful /healthz response. fleet-db typically starts in
// <500ms; 30s is a generous ceiling for cold-cache filesystems.
const startupTimeout = 30 * time.Second

// EmbeddedFleetDB owns an embedded fleet-db subprocess and the
// in-process miniredis it talks to. Used by `loom serve` to run a
// zero-install fleet-db for local single-user mode.
//
// Lifecycle:
//
//	emb, err := bootstrap.StartEmbedded(ctx, dataDir, logger)
//	defer emb.Stop()
//	url := emb.URL()  // pass to fleetdb client
//
// Stop is idempotent and safe to call from a deferred function plus a
// signal handler.
type EmbeddedFleetDB struct {
	url      string
	cmd      *exec.Cmd
	redisMgr *localredis.Manager
	runLock  *embeddedRuntimeLock
	logger   *slog.Logger

	stopOnce      sync.Once
	stopRequested atomic.Bool   // true once Stop is invoked; suppresses "unexpected exit" log
	waitErr       chan error    // sole receiver of cmd.Wait result; size 1 + closed afterwards
	done          chan struct{} // closed by Stop after redisMgr.Close
}

// StartEmbedded spawns the fleet-db subprocess + miniredis. It returns
// once /healthz reports ready; if startup fails it tears down whatever
// it managed to start.
//
// dataDir is the per-user loom directory (typically LoomDir()) — the
// miniredis snapshot lives at <dataDir>/fleet-db/redis-snapshot.json.
// In cloud mode (LOOM_FLEET_DB_URL set) callers should not call this
// at all; this function unconditionally spawns a subprocess.
//
//nolint:funlen // Process bootstrap needs to keep setup and cleanup ordering explicit.
func StartEmbedded(ctx context.Context, dataDir string, logger *slog.Logger) (*EmbeddedFleetDB, error) {
	if logger == nil {
		logger = slog.Default()
	}

	diag := DiagnoseFleetDBBinary()
	if diag.Err != nil {
		return nil, diag.Err
	}
	binPath := diag.Path
	if !diag.Runnable {
		return nil, fmt.Errorf("embedded: fleet-db binary %s is not runnable. %s", binPath, diag.Remediation)
	}

	fleetDir := filepath.Join(dataDir, "fleet-db")
	if err := os.MkdirAll(fleetDir, 0755); err != nil {
		return nil, fmt.Errorf("embedded: mkdir %s: %w", fleetDir, err)
	}
	runLock, err := acquireEmbeddedRuntimeLock(fleetDir)
	if err != nil {
		return nil, err
	}
	releaseLockOnError := true
	defer func() {
		if releaseLockOnError {
			_ = runLock.Release()
		}
	}()

	snapshotPath := filepath.Join(fleetDir, "redis-snapshot.json")
	// fleetKeys=true so the snapshot persists fleet-db's keyspace
	// across CLI invocations (each `loom <cmd>` re-boots a fleet-db
	// subprocess against this same on-disk snapshot).
	redisMgr, err := localredis.NewManager(snapshotPath, true /* fleetKeys */, logger)
	if err != nil {
		return nil, fmt.Errorf("embedded: start miniredis: %w", err)
	}
	redisMgr.Start(ctx)

	httpAddr, _, err := netutil.PickFreeLoopbackPort()
	if err != nil {
		_ = redisMgr.Close()
		return nil, fmt.Errorf("embedded: pick http port: %w", err)
	}

	// Args:
	//   --redis-durability-profile=managed  miniredis rejects CONFIG SET; "managed" skips
	//                                       all CONFIG calls (designed for managed-Redis providers,
	//                                       fits embedded-miniredis equally well)
	//   --auth-dev-mode                     accept X-Actor as identity (no JWT setup)
	//   --authz-enabled=false               single-user mode skips RBAC
	//   --rpc-enabled=false                 embedded mode uses HTTP only; avoid binding /var/run
	cmd := exec.CommandContext(ctx, binPath, //nolint:gosec // binPath is from controlled discovery
		"--redis-durability-profile=managed",
		"--auth-dev-mode",
		"--authz-enabled=false",
		"--rpc-enabled=false",
	)
	cmd.Env = append(os.Environ(),
		"FLEET_SERVER_ADDR="+httpAddr,
		"FLEET_REDIS_ADDR="+redisMgr.Addr(),
	)
	// Pipe stderr/stdout into the loom logger so the user sees fleet-db
	// startup errors in their loom serve log.
	stdoutTail := newTailWriter(4096)
	stderrTail := newTailWriter(4096)
	cmd.Stdout = io.MultiWriter(newSlogWriter(logger, slog.LevelDebug, "fleet-db"), stdoutTail)
	cmd.Stderr = io.MultiWriter(newSlogWriter(logger, slog.LevelInfo, "fleet-db"), stderrTail)
	// New process group so SIGINT to loom doesn't double-fire to fleet-db.
	cmd.SysProcAttr = newDetachedSysProcAttr()

	if err := cmd.Start(); err != nil {
		_ = redisMgr.Close()
		return nil, fmt.Errorf("embedded: start %s: %w", binPath, err)
	}
	logger.Info("embedded fleet-db started", "pid", cmd.Process.Pid, "addr", httpAddr, "redis", redisMgr.Addr(), "binary", binPath)

	emb := &EmbeddedFleetDB{
		url:      "http://" + httpAddr,
		cmd:      cmd,
		redisMgr: redisMgr,
		runLock:  runLock,
		logger:   logger,
		waitErr:  make(chan error, 1),
		done:     make(chan struct{}),
	}
	// Reaper goroutine. The sole cmd.Wait() caller in the lifecycle —
	// Stop reads from waitErr instead of calling Wait again to avoid the
	// double-Wait race that otherwise produced "waitid: no child
	// processes" on shutdown.
	go emb.reapAndPublish()

	healthCtx, healthCancel := context.WithTimeout(ctx, startupTimeout)
	defer healthCancel()
	if err := netutil.WaitForHealthz(healthCtx, emb.url, healthCheckTimeout); err != nil {
		_ = emb.Stop()
		return nil, fmt.Errorf("embedded: fleet-db not ready after %s: %w (binary=%s addr=%s redis=%s snapshot=%s stderr=%q stdout=%q)",
			startupTimeout, err, binPath, httpAddr, redisMgr.Addr(), snapshotPath, stderrTail.String(), stdoutTail.String())
	}

	releaseLockOnError = false
	return emb, nil
}

// URL returns the base HTTP URL of the embedded fleet-db (no trailing slash).
func (e *EmbeddedFleetDB) URL() string { return e.url }

// Stop signals fleet-db to terminate, waits briefly for clean shutdown,
// and closes the miniredis. Idempotent.
func (e *EmbeddedFleetDB) Stop() error {
	var err error
	e.stopOnce.Do(func() {
		e.stopRequested.Store(true)
		if e.cmd != nil && e.cmd.Process != nil {
			_ = e.cmd.Process.Signal(os.Interrupt)
			// Wait for the reaper goroutine to publish the exit result.
			// 5s grace; on timeout, Kill and drain.
			select {
			case waitErr := <-e.waitErr:
				if waitErr != nil && !isSignalledExit(waitErr) {
					err = fmt.Errorf("embedded: fleet-db exit: %w", waitErr)
				}
			case <-time.After(5 * time.Second):
				_ = e.cmd.Process.Kill()
				<-e.waitErr // drain so the reaper can finish
				err = errors.New("embedded: fleet-db did not exit within 5s, killed")
			}
		}
		if e.redisMgr != nil {
			if rerr := e.redisMgr.Close(); rerr != nil && err == nil {
				err = fmt.Errorf("embedded: miniredis close: %w", rerr)
			}
		}
		if e.runLock != nil {
			if lerr := e.runLock.Release(); lerr != nil && err == nil {
				err = fmt.Errorf("embedded: release runtime lock: %w", lerr)
			}
		}
		close(e.done)
	})
	return err
}

// Done is closed when Stop completes. Useful for callers that want to
// block on a goroutine waiting for shutdown.
func (e *EmbeddedFleetDB) Done() <-chan struct{} { return e.done }

// reapAndPublish is the sole cmd.Wait() caller. It publishes the exit
// result to waitErr (consumed by Stop) and logs an unexpected exit only
// if the user did not initiate Stop — distinguishing a crash from a
// graceful shutdown.
func (e *EmbeddedFleetDB) reapAndPublish() {
	if e.cmd == nil {
		close(e.waitErr)
		return
	}
	err := e.cmd.Wait()
	e.waitErr <- err
	close(e.waitErr)
	if !e.stopRequested.Load() && err != nil && !isSignalledExit(err) {
		e.logger.Error("embedded fleet-db exited unexpectedly", "err", err)
	}
}

// DiscoverFleetDBBinary locates the fleet-db executable.
//
// Resolution order:
//  1. FLEET_DB_BIN env var (if set, must point at an existing file)
//  2. fleet-db on PATH
//  3. Sibling of the loom binary (filepath.Dir(os.Args[0])/fleet-db)
//  4. <LoomDir>/bin/fleet-db
func DiscoverFleetDBBinary() (string, error) {
	diag := DiagnoseFleetDBBinary()
	if diag.Err != nil {
		return "", diag.Err
	}
	return diag.Path, nil
}

// DiagnoseFleetDBBinary locates and probes the embedded fleet-db executable.
func DiagnoseFleetDBBinary() FleetDBBinaryDiagnostic {
	var checked []string
	remediation := fmt.Sprintf("Install fleet-db on PATH, set %s=/path/to/fleet-db, or place it at %s.", EnvFleetDBBin, filepath.Join("<loom-dir>", "bin", fleetDBBinName))

	if p := os.Getenv(EnvFleetDBBin); p != "" {
		checked = append(checked, EnvFleetDBBin+"="+p)
		if err := validateFleetDBBinaryPath(p); err != nil {
			return FleetDBBinaryDiagnostic{Path: p, Checked: checked, Err: fmt.Errorf("%s=%s: %w. %s", EnvFleetDBBin, p, err, remediation), Remediation: remediation}
		}
		return probeFleetDBBinary(p, checked, remediation)
	}
	if p, err := exec.LookPath(fleetDBBinName); err == nil {
		checked = append(checked, "PATH:"+p)
		if err := validateFleetDBBinaryPath(p); err == nil {
			return probeFleetDBBinary(p, checked, remediation)
		}
	} else {
		checked = append(checked, "PATH:"+fleetDBBinName)
	}
	if exe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), fleetDBBinName)
		checked = append(checked, sibling)
		if err := validateFleetDBBinaryPath(sibling); err == nil {
			return probeFleetDBBinary(sibling, checked, remediation)
		}
	}
	if dir := LoomDir(); dir != "" {
		bundled := filepath.Join(dir, "bin", fleetDBBinName)
		checked = append(checked, bundled)
		if err := validateFleetDBBinaryPath(bundled); err == nil {
			return probeFleetDBBinary(bundled, checked, remediation)
		}
	}
	return FleetDBBinaryDiagnostic{
		Checked:     checked,
		Err:         fmt.Errorf("fleet-db binary not found; checked %s. %s", strings.Join(checked, ", "), remediation),
		Remediation: remediation,
	}
}

func validateFleetDBBinaryPath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("is a directory")
	}
	if runtime.GOOS != "windows" && info.Mode()&0111 == 0 {
		return fmt.Errorf("not executable")
	}
	return nil
}

func probeFleetDBBinary(path string, checked []string, remediation string) FleetDBBinaryDiagnostic {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--help") //nolint:gosec // path was validated by discovery
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if len(text) > 500 {
		text = text[:500]
	}
	if err != nil {
		if looksLikeFleetDBHelp(text) {
			return FleetDBBinaryDiagnostic{Path: path, Checked: checked, Runnable: true, ProbeOutput: text, Remediation: remediation}
		}
		return FleetDBBinaryDiagnostic{
			Path:        path,
			Checked:     checked,
			ProbeOutput: text,
			Err:         fmt.Errorf("fleet-db binary %s is not runnable: %w. %s", path, err, remediation),
			Remediation: remediation,
		}
	}
	if !strings.Contains(strings.ToLower(text), "fleet") {
		return FleetDBBinaryDiagnostic{
			Path:        path,
			Checked:     checked,
			ProbeOutput: text,
			Err:         fmt.Errorf("fleet-db binary %s probe output did not look like fleet-db. %s", path, remediation),
			Remediation: remediation,
		}
	}
	return FleetDBBinaryDiagnostic{Path: path, Checked: checked, Runnable: true, ProbeOutput: text, Remediation: remediation}
}

func looksLikeFleetDBHelp(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "usage of fleet-db") ||
		(strings.Contains(lower, "auth-dev-mode") && strings.Contains(lower, "redis-addr"))
}

// slogWriter routes child-process stdio into a slog logger so output
// lands in loom's structured log instead of disappearing or polluting
// the operator's terminal directly.
type slogWriter struct {
	logger *slog.Logger
	level  slog.Level
	source string
	buf    []byte
}

// maxLineBuffer caps the in-flight partial-line buffer. A misbehaving
// child that writes a stream with no newline can't grow buf without
// bound — once we hit the cap we flush the partial line and reset.
const maxLineBuffer = 64 * 1024

func newSlogWriter(logger *slog.Logger, level slog.Level, source string) io.Writer {
	return &slogWriter{logger: logger, level: level, source: source}
}

type tailWriter struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func newTailWriter(max int) *tailWriter {
	return &tailWriter{max: max}
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.max {
		w.buf = append([]byte(nil), w.buf[len(w.buf)-w.max:]...)
	}
	return len(p), nil
}

func (w *tailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.TrimSpace(string(w.buf))
}

func (w *slogWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.logger.Log(context.Background(), w.level, string(w.buf[:i]), "source", w.source)
		// Re-slice into a fresh buffer so the underlying array can be
		// freed instead of growing forever as we advance the slice.
		w.buf = append([]byte(nil), w.buf[i+1:]...)
	}
	if len(w.buf) >= maxLineBuffer {
		w.logger.Log(context.Background(), w.level, string(w.buf), "source", w.source, "truncated", true)
		w.buf = w.buf[:0]
	}
	return len(p), nil
}

// isSignalledExit reports whether err is a "killed by signal" exit, in
// which case Stop succeeded — fleet-db received SIGINT and exited as
// expected.
func isSignalledExit(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	// Process exited via signal — treat as clean shutdown.
	return exitErr.ExitCode() == -1
}
