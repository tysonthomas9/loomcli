package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
	"github.com/tysonthomas9/loomcli/internal/netutil"
)

// EnvFlueBin overrides the flue binary path used to run workflow
// projects from source (`flue dev`).
const EnvFlueBin = "LOOM_FLUE_BIN"

// ErrFlueAlreadyRunning is returned when another process owns the
// local Flue runtime for this loom data directory.
var ErrFlueAlreadyRunning = errors.New("flue runtime already running")

const flueBinName = "flue"

// flueRuntimeInfo is written to <dataDir>/flue/runtime.json so other
// loom processes (the daemon, CLI commands) can discover and attach to
// a running Flue child instead of starting a second one. Mirrors the
// embedded fleet-db runtime.json contract.
type flueRuntimeInfo struct {
	PID        int       `json:"pid"`
	URL        string    `json:"url"`
	ProjectDir string    `json:"project_dir"`
	StartedAt  time.Time `json:"started_at"`
}

func flueDir(dataDir string) string         { return filepath.Join(dataDir, "flue") }
func flueRuntimePath(dataDir string) string { return filepath.Join(flueDir(dataDir), "runtime.json") }

// FlueConfig configures StartFlue.
type FlueConfig struct {
	// ProjectDir is the Flue workflow project source directory
	// (contains flue.config.ts / agents/). Required in dev mode.
	ProjectDir string
	// DistServerPath, when set, runs `node <DistServerPath>` (a built
	// dist/server.mjs) instead of `flue dev` from ProjectDir.
	DistServerPath string
	// Env is appended to the child environment (e.g. LOOM_FLEET_BASE_URL).
	Env []string
	// Logger receives child stdio and lifecycle logs. Defaults to
	// slog.Default().
	Logger *slog.Logger
}

// EmbeddedFlue owns a Flue child process serving the execution plane
// in local mode. Same lifecycle contract as EmbeddedFleetDB: Stop is
// idempotent, the reaper goroutine is the sole cmd.Wait caller, and a
// runtime.json + flock pair prevents double-ownership.
type EmbeddedFlue struct {
	url        string
	projectDir string
	cmd        *exec.Cmd
	runLock    *embeddedRuntimeLock
	dataDir    string
	logger     *slog.Logger

	stopOnce      sync.Once
	stopRequested atomic.Bool
	waitErr       chan error
	done          chan struct{}
}

// ReuseFlueRuntime returns the URL of an already-running Flue child
// recorded in <dataDir>/flue/runtime.json, verifying liveness and
// health. ok=false (with nil error) when no runtime file exists.
func ReuseFlueRuntime(ctx context.Context, dataDir string) (url string, ok bool, err error) {
	data, err := os.ReadFile(flueRuntimePath(dataDir)) //nolint:gosec // under loom data dir
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	var info flueRuntimeInfo
	if err := json.Unmarshal(data, &info); err != nil || info.URL == "" {
		return "", false, fmt.Errorf("parse %s: invalid flue runtime metadata", flueRuntimePath(dataDir))
	}
	if info.PID <= 0 || !lockfile.IsProcessRunning(info.PID) {
		_ = os.Remove(flueRuntimePath(dataDir))
		return "", false, fmt.Errorf("flue runtime pid %d is not running", info.PID)
	}
	healthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := netutil.WaitForHealthz(healthCtx, info.URL, healthCheckTimeout); err != nil {
		return "", false, fmt.Errorf("flue runtime %s pid %d is not healthy: %w", info.URL, info.PID, err)
	}
	return info.URL, true, nil
}

// StartFlue spawns the Flue child and returns once /healthz responds.
// dataDir is the per-user loom directory (LoomDir()).
//
//nolint:funlen // process bootstrap keeps setup/cleanup ordering explicit
func StartFlue(ctx context.Context, dataDir string, cfg FlueConfig) (*EmbeddedFlue, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	dir := flueDir(dataDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("flue: mkdir %s: %w", dir, err)
	}
	runLock, err := acquireEmbeddedRuntimeLock(dir)
	if err != nil {
		if errors.Is(err, ErrEmbeddedAlreadyRunning) {
			return nil, fmt.Errorf("%w: %s", ErrFlueAlreadyRunning, dir)
		}
		return nil, err
	}
	releaseLockOnError := true
	defer func() {
		if releaseLockOnError {
			_ = runLock.Release()
		}
	}()

	httpAddr, port, err := netutil.PickFreeLoopbackPort()
	if err != nil {
		return nil, fmt.Errorf("flue: pick http port: %w", err)
	}

	var cmd *exec.Cmd
	switch {
	case cfg.DistServerPath != "":
		nodeBin, err := exec.LookPath("node")
		if err != nil {
			return nil, fmt.Errorf("flue: node binary not found on PATH (required to run a built bundle): %w", err)
		}
		cmd = exec.CommandContext(ctx, nodeBin, cfg.DistServerPath) //nolint:gosec // operator-supplied path
		cmd.Dir = filepath.Dir(cfg.DistServerPath)
	case cfg.ProjectDir != "":
		flueBin, diag := discoverFlueBinary(cfg.ProjectDir)
		if diag != nil {
			return nil, diag
		}
		cmd = exec.CommandContext(ctx, flueBin, "dev", "--port", strconv.Itoa(port)) //nolint:gosec // binary from controlled discovery
		cmd.Dir = cfg.ProjectDir
	default:
		return nil, errors.New("flue: FlueConfig requires ProjectDir or DistServerPath")
	}
	cmd.Env = append(os.Environ(),
		"PORT="+strconv.Itoa(port),
		"FLUE_MODE=local",
	)
	cmd.Env = append(cmd.Env, cfg.Env...)

	stdoutTail := newTailWriter(4096)
	stderrTail := newTailWriter(4096)
	cmd.Stdout = io.MultiWriter(newSlogWriter(logger, slog.LevelDebug, "flue"), stdoutTail)
	cmd.Stderr = io.MultiWriter(newSlogWriter(logger, slog.LevelInfo, "flue"), stderrTail)
	cmd.SysProcAttr = newDetachedSysProcAttr()

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("flue: start %s: %w", cmd.Path, err)
	}
	logger.Info("flue child started", "pid", cmd.Process.Pid, "addr", httpAddr, "project", cfg.ProjectDir, "dist", cfg.DistServerPath)

	emb := &EmbeddedFlue{
		url:        "http://" + httpAddr,
		projectDir: cfg.ProjectDir,
		cmd:        cmd,
		runLock:    runLock,
		dataDir:    dataDir,
		logger:     logger,
		waitErr:    make(chan error, 1),
		done:       make(chan struct{}),
	}
	go emb.reapAndPublish()

	// flue dev cold-builds the project before listening; allow a
	// generous ceiling (Vite build + npm cold cache).
	healthCtx, healthCancel := context.WithTimeout(ctx, 120*time.Second)
	defer healthCancel()
	if err := netutil.WaitForHealthz(healthCtx, emb.url, healthCheckTimeout); err != nil {
		_ = emb.Stop()
		return nil, fmt.Errorf("flue: not ready: %w (addr=%s stderr=%q stdout=%q)",
			err, httpAddr, stderrTail.String(), stdoutTail.String())
	}
	info := flueRuntimeInfo{
		PID:        cmd.Process.Pid,
		URL:        emb.url,
		ProjectDir: cfg.ProjectDir,
		StartedAt:  time.Now().UTC(),
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err == nil {
		err = os.WriteFile(flueRuntimePath(dataDir), data, 0600)
	}
	if err != nil {
		_ = emb.Stop()
		return nil, fmt.Errorf("flue: write runtime metadata: %w", err)
	}

	releaseLockOnError = false
	return emb, nil
}

// URL returns the base HTTP URL of the Flue child.
func (e *EmbeddedFlue) URL() string { return e.url }

// WaitExit blocks until the child exits and returns its exit error.
// Used by `loom workflow dev` to drive supervised restarts. Safe to
// call concurrently with Stop: the reaper closes waitErr after
// publishing, so late readers observe the closed channel rather than
// blocking.
func (e *EmbeddedFlue) WaitExit() error {
	err := <-e.waitErr
	return err
}

// Stop signals the child to terminate, waits briefly, kills on
// timeout. Idempotent.
func (e *EmbeddedFlue) Stop() error {
	var err error
	e.stopOnce.Do(func() {
		e.stopRequested.Store(true)
		if e.cmd != nil && e.cmd.Process != nil {
			_ = e.cmd.Process.Signal(os.Interrupt)
			select {
			case waitErr := <-e.waitErr:
				if waitErr != nil && !isSignalledExit(waitErr) {
					err = fmt.Errorf("flue: exit: %w", waitErr)
				}
			case <-time.After(5 * time.Second):
				_ = e.cmd.Process.Kill()
				<-e.waitErr
				err = errors.New("flue: did not exit within 5s, killed")
			}
		}
		if e.runLock != nil {
			pid := 0
			if e.cmd != nil && e.cmd.Process != nil {
				pid = e.cmd.Process.Pid
			}
			removeFlueRuntimeIfOwner(e.dataDir, pid, e.url)
			if lerr := e.runLock.Release(); lerr != nil && err == nil {
				err = fmt.Errorf("flue: release runtime lock: %w", lerr)
			}
		}
		close(e.done)
	})
	return err
}

// Done is closed when Stop completes.
func (e *EmbeddedFlue) Done() <-chan struct{} { return e.done }

// reapAndPublish is the sole cmd.Wait caller. Publishing then closing
// waitErr lets both WaitExit and Stop read the exit result without
// racing each other for a single buffered value (mirrors
// EmbeddedFleetDB.reapAndPublish).
func (e *EmbeddedFlue) reapAndPublish() {
	if e.cmd == nil {
		close(e.waitErr)
		return
	}
	err := e.cmd.Wait()
	e.waitErr <- err
	close(e.waitErr)
	if !e.stopRequested.Load() && err != nil && !isSignalledExit(err) {
		e.logger.Error("flue child exited unexpectedly", "err", err)
	}
}

func removeFlueRuntimeIfOwner(dataDir string, pid int, url string) {
	data, err := os.ReadFile(flueRuntimePath(dataDir)) //nolint:gosec // under loom data dir
	if err != nil {
		return
	}
	var info flueRuntimeInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return
	}
	if info.PID == pid && info.URL == url {
		_ = os.Remove(flueRuntimePath(dataDir))
	}
}

// discoverFlueBinary resolves the flue executable:
//  1. LOOM_FLUE_BIN env var
//  2. <projectDir>/node_modules/.bin/flue (the project's @flue/cli devDependency)
//  3. flue on PATH
//  4. sibling of the loom binary
//  5. <LoomDir>/bin/flue
func discoverFlueBinary(projectDir string) (string, error) {
	var checked []string
	remediation := fmt.Sprintf("Add @flue/cli to the workflow project's devDependencies (pnpm install), install flue on PATH, set %s=/path/to/flue, or place it at %s.", EnvFlueBin, filepath.Join("<loom-dir>", "bin", flueBinName))
	if p := os.Getenv(EnvFlueBin); p != "" {
		if err := validateFleetDBBinaryPath(p); err != nil {
			return "", fmt.Errorf("%s=%s: %w. %s", EnvFlueBin, p, err, remediation)
		}
		return p, nil
	}
	if projectDir != "" {
		local := filepath.Join(projectDir, "node_modules", ".bin", flueBinName)
		checked = append(checked, local)
		if err := validateFleetDBBinaryPath(local); err == nil {
			return local, nil
		}
	}
	if p, err := exec.LookPath(flueBinName); err == nil {
		return p, nil
	}
	checked = append(checked, "PATH:"+flueBinName)
	if exe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), flueBinName)
		checked = append(checked, sibling)
		if err := validateFleetDBBinaryPath(sibling); err == nil {
			return sibling, nil
		}
	}
	if dir := LoomDir(); dir != "" {
		bundled := filepath.Join(dir, "bin", flueBinName)
		checked = append(checked, bundled)
		if err := validateFleetDBBinaryPath(bundled); err == nil {
			return bundled, nil
		}
	}
	return "", fmt.Errorf("flue binary not found; checked %v. %s", checked, remediation)
}
