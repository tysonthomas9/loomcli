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
	"sync"
	"sync/atomic"
	"time"

	"github.com/tysonthomas9/loomcli/internal/netutil"
	"github.com/tysonthomas9/loomcli/internal/webui/localredis"
)

// EnvFleetDBBin overrides the fleet-db binary path. When set, binary
// discovery skips PATH and the standard locations.
const EnvFleetDBBin = "FLEET_DB_BIN"

// fleetDBBinName is the executable name used in PATH lookup and standard
// install locations.
const fleetDBBinName = "fleet-db"

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
func StartEmbedded(ctx context.Context, dataDir string, logger *slog.Logger) (*EmbeddedFleetDB, error) {
	if logger == nil {
		logger = slog.Default()
	}

	binPath, err := DiscoverFleetDBBinary()
	if err != nil {
		return nil, err
	}

	fleetDir := filepath.Join(dataDir, "fleet-db")
	if err := os.MkdirAll(fleetDir, 0755); err != nil {
		return nil, fmt.Errorf("embedded: mkdir %s: %w", fleetDir, err)
	}

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
	cmd := exec.CommandContext(ctx, binPath, //nolint:gosec // binPath is from controlled discovery
		"--redis-durability-profile=managed",
		"--auth-dev-mode",
		"--authz-enabled=false",
	)
	cmd.Env = append(os.Environ(),
		"FLEET_SERVER_ADDR="+httpAddr,
		"FLEET_REDIS_ADDR="+redisMgr.Addr(),
	)
	// Pipe stderr/stdout into the loom logger so the user sees fleet-db
	// startup errors in their loom serve log.
	cmd.Stdout = newSlogWriter(logger, slog.LevelDebug, "fleet-db")
	cmd.Stderr = newSlogWriter(logger, slog.LevelInfo, "fleet-db")
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
		return nil, fmt.Errorf("embedded: fleet-db not ready after %s: %w", startupTimeout, err)
	}

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
	if p := os.Getenv(EnvFleetDBBin); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("%s=%s: %w", EnvFleetDBBin, p, err)
		}
		return p, nil
	}
	if p, err := exec.LookPath(fleetDBBinName); err == nil {
		return p, nil
	}
	if exe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), fleetDBBinName)
		if _, err := os.Stat(sibling); err == nil {
			return sibling, nil
		}
	}
	if dir := LoomDir(); dir != "" {
		bundled := filepath.Join(dir, "bin", fleetDBBinName)
		if _, err := os.Stat(bundled); err == nil {
			return bundled, nil
		}
	}
	return "", fmt.Errorf("fleet-db binary not found (set %s, install on PATH, or place at <loom-dir>/bin/%s)", EnvFleetDBBin, fleetDBBinName)
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
