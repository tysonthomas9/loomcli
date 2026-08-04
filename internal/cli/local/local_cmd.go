package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/netutil"
)

var (
	dataDirFlag string
	portFlag    int
	bindFlag    string
	jsonFlag    bool

	readRuntimeStatusFn = ReadRuntimeStatus
	restartRuntimeFn    = RestartRuntime
)

var localCmd = &cobra.Command{
	Use:     "local",
	Short:   "Manage the local desktop runtime",
	GroupID: "config",
	Long: `Manage the local desktop runtime used by Loom.app.

The local runtime stores state under the app data directory, starts a local
Loom web/API server, and writes runtime metadata that the desktop shell can
read. On macOS the app should run 'loom local service' through a per-user
LaunchAgent.`,
}

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Run the local runtime service in the foreground",
	RunE:  runService,
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the local runtime service in the background",
	RunE:  runStart,
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show local runtime status",
	RunE:  runStatus,
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the local runtime service",
	RunE:  runStop,
}

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the local runtime service",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := runStop(cmd, args); err != nil {
			return err
		}
		time.Sleep(500 * time.Millisecond)
		return runStart(cmd, args)
	},
}

var installServiceCmd = &cobra.Command{
	Use:   "install-service",
	Short: "Install the persistent local runtime service",
	RunE:  runInstallService,
}

var uninstallServiceCmd = &cobra.Command{
	Use:   "uninstall-service",
	Short: "Uninstall the persistent local runtime service",
	RunE:  runUninstallService,
}

var drainCmd = &cobra.Command{
	Use:   "drain",
	Short: "Mark the local runtime as draining",
	RunE: func(_ *cobra.Command, _ []string) error {
		return updatePauseState(true)
	},
}

var resumeCmd = &cobra.Command{
	Use:   "resume",
	Short: "Resume local runtime task claims",
	RunE: func(_ *cobra.Command, _ []string) error {
		return updatePauseState(false)
	},
}

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Print local runtime log paths",
	RunE:  runLogs,
}

func init() {
	localCmd.PersistentFlags().StringVar(&dataDirFlag, "data-dir", "", "Local runtime data directory (default: macOS app data dir)")
	serviceCmd.Flags().IntVar(&portFlag, "port", 0, "Local web/API port (0 picks a free port)")
	serviceCmd.Flags().StringVar(&bindFlag, "bind", "127.0.0.1", "Local web/API bind address")
	startCmd.Flags().IntVar(&portFlag, "port", 0, "Local web/API port (0 picks a free port)")
	statusCmd.Flags().BoolVar(&jsonFlag, "json", false, "Print JSON status")
	installServiceCmd.Flags().IntVar(&portFlag, "port", 0, "Local web/API port (0 picks a free port)")
	localCmd.AddCommand(serviceCmd, startCmd, statusCmd, stopCmd, restartCmd, installServiceCmd, uninstallServiceCmd, drainCmd, resumeCmd, logsCmd)
	cli.RegisterCommand(localCmd)
}

func runService(cmd *cobra.Command, _ []string) error {
	serviceCtx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	cfg, err := prepareLocalServiceConfig()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(serveLogPath(cfg.dataDir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open serve log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	info := newRuntimeInfo(cfg)
	if err := writeRuntime(cfg.dataDir, info); err != nil {
		return err
	}

	serveCmd, err := startServeProcess(serviceCtx, cfg, logFile, info)
	if err != nil {
		return err
	}
	if err := awaitServeHealthy(serviceCtx, cfg, info, serveCmd); err != nil {
		return err
	}

	startLocalDaemonSupervisor(serviceCtx, cfg.dataDir, cfg.exe, cfg.port, cfg.url)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Loom local runtime: %s\n", info.URL)
	return waitServeExit(serviceCtx, serveCmd, cfg.dataDir, info)
}

// localServiceConfig is the resolved environment for a single runService
// invocation: data dir, bind address + port, this process's executable, and
// the fleet-db identity stamps written to runtime.json.
type localServiceConfig struct {
	dataDir   string
	bindAddr  string
	port      int
	exe       string
	identity  executableIdentity
	redisHash string
	url       string
}

func prepareLocalServiceConfig() (*localServiceConfig, error) {
	dataDir, err := resolveDataDir(dataDirFlag)
	if err != nil {
		return nil, err
	}
	if err := ensureRuntimeDirs(dataDir); err != nil {
		return nil, err
	}
	_ = os.Setenv("LOOM_CONFIG_DIR", dataDir)
	_ = os.Setenv("LOOM_DESKTOP_DATA_DIR", dataDir)
	_ = os.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", dataDir)
	if os.Getenv("FLEET_DB_BIN") == "" {
		if fleetDBBin := bundledExecutable("fleet-db"); fleetDBBin != "" {
			_ = os.Setenv("FLEET_DB_BIN", fleetDBBin)
		}
	}
	bind := bindFlag
	if bind == "" {
		bind = "127.0.0.1"
		bindFlag = bind
	}
	port := portFlag
	if port == 0 {
		_, p, perr := netutil.PickFreeLoopbackPort()
		if perr != nil {
			return nil, fmt.Errorf("pick local runtime port: %w", perr)
		}
		port = p
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve loom executable: %w", err)
	}
	redisHash, err := currentFleetDBRedisHash(dataDir)
	if err != nil {
		return nil, fmt.Errorf("load FleetDB Redis settings: %w", err)
	}
	return &localServiceConfig{
		dataDir:   dataDir,
		bindAddr:  bind,
		port:      port,
		exe:       exe,
		identity:  currentExecutableIdentity(),
		redisHash: redisHash,
		url:       "http://" + net.JoinHostPort(bind, strconv.Itoa(port)),
	}, nil
}

func newRuntimeInfo(cfg *localServiceConfig) *runtimeInfo {
	info := &runtimeInfo{
		Status:    "starting",
		PID:       os.Getpid(),
		DataDir:   cfg.dataDir,
		URL:       cfg.url,
		Port:      cfg.port,
		StartedAt: time.Now().UTC(),
	}
	applyExecutableIdentity(info, cfg.identity)
	info.FleetDBRedisHash = cfg.redisHash
	return info
}

// startServeProcess spawns `loom serve` as a child, records its PID in
// runtime.json, and returns the running *exec.Cmd. Caller owns Wait().
func startServeProcess(ctx context.Context, cfg *localServiceConfig, logFile *os.File, info *runtimeInfo) (*exec.Cmd, error) {
	// exe is this process's resolved binary path; args are fixed subcommand +
	// CLI-validated bindFlag / port. The guard refuses to re-exec a *.test
	// binary (fork-bomb protection; see reexec_guard.go).
	serveCmd, err := loomReexecCommandContext(ctx, cfg.exe, "serve",
		"--bind", cfg.bindAddr,
		"--port", strconv.Itoa(cfg.port),
		"--fleet-mode",
	)
	if err != nil {
		info.Status = "failed"
		info.Error = err.Error()
		_ = writeRuntime(cfg.dataDir, info)
		return nil, fmt.Errorf("start loom serve: %w", err)
	}
	serveCmd.Env = localEnv(cfg.dataDir, cfg.port)
	serveCmd.Dir = cfg.dataDir
	serveCmd.Stdout = logFile
	serveCmd.Stderr = logFile
	serveCmd.SysProcAttr = newDetachedSysProcAttr()
	if err := serveCmd.Start(); err != nil {
		info.Status = "failed"
		info.Error = err.Error()
		_ = writeRuntime(cfg.dataDir, info)
		return nil, fmt.Errorf("start loom serve: %w", err)
	}
	info.ServePID = serveCmd.Process.Pid
	if err := writeRuntime(cfg.dataDir, info); err != nil {
		_ = serveCmd.Process.Kill()
		return nil, err
	}
	return serveCmd, nil
}

// serveStartupLogTailBytes caps how much of loom-serve.log we splice into
// the user-facing startup error and into runtime.json.error. 4 KB is enough
// to capture the last few lines without bloating either surface.
const serveStartupLogTailBytes = 4096

// serveStartupError wraps the health-check error with a tail of
// loom-serve.log so the caller learns the real spawn failure instead of a
// generic "connection refused". Implements Unwrap() so errors.Is/As still
// see the original health error.
type serveStartupError struct {
	healthErr error
	logPath   string
	logTail   string
	maxBytes  int
}

func (e *serveStartupError) Error() string {
	var b strings.Builder
	b.WriteString("local runtime did not become healthy: ")
	b.WriteString(e.healthErr.Error())
	b.WriteString("\nrecent loom-serve.log (last ")
	b.WriteString(strconv.Itoa(e.maxBytes))
	b.WriteString(" bytes from ")
	b.WriteString(e.logPath)
	b.WriteString("):\n")
	for _, line := range strings.Split(e.logTail, "\n") {
		b.WriteString("  ")
		b.WriteString(strings.TrimRight(line, "\r"))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (e *serveStartupError) Unwrap() error { return e.healthErr }

// wrapServeStartupError augments healthErr with the tail of loom-serve.log
// when the log has content. When the log is missing or empty it falls back
// to today's exact error format so existing callers/tests that match the
// prefix continue to work.
func wrapServeStartupError(dataDir string, healthErr error) error {
	tail := serveStartupLogTail(dataDir, serveStartupLogTailBytes)
	if tail == "" {
		return fmt.Errorf("local runtime did not become healthy: %w", healthErr)
	}
	return &serveStartupError{
		healthErr: healthErr,
		logPath:   serveLogPath(dataDir),
		logTail:   tail,
		maxBytes:  serveStartupLogTailBytes,
	}
}

// awaitServeHealthy polls the serve URL until it becomes ready or the
// 30s budget expires. Marks the runtime info accordingly; on timeout the
// child serve process is killed.
func awaitServeHealthy(ctx context.Context, cfg *localServiceConfig, info *runtimeInfo, serveCmd *exec.Cmd) error {
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := waitForRuntime(waitCtx, cfg.url); err != nil {
		wrapped := wrapServeStartupError(cfg.dataDir, err)
		info.Status = "failed"
		info.Error = wrapped.Error()
		_ = writeRuntime(cfg.dataDir, info)
		_ = serveCmd.Process.Kill()
		return wrapped
	}
	info.Status = "running"
	info.Error = ""
	if err := writeRuntime(cfg.dataDir, info); err != nil {
		_ = serveCmd.Process.Kill()
		return err
	}
	return nil
}

// waitServeExit blocks until the child serve process exits, then updates
// runtime.json with the final status. Returns nil when shutdown was caused
// by a signal (serviceCtx cancellation).
func waitServeExit(ctx context.Context, serveCmd *exec.Cmd, dataDir string, info *runtimeInfo) error {
	err := serveCmd.Wait()
	info.Status = "stopped"
	info.Error = ""
	if err != nil && ctx.Err() == nil {
		info.Status = "failed"
		info.Error = err.Error()
	}
	_ = writeRuntime(dataDir, info)
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func runStart(cmd *cobra.Command, _ []string) error {
	dataDir, err := resolveDataDir(dataDirFlag)
	if err != nil {
		return err
	}
	result, err := StartRuntime(dataDir, portFlag)
	if err != nil {
		return err
	}
	if result.AlreadyRunning {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Loom local runtime already running: %s\n", result.URL)
		return nil
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Started Loom local service (pid %d)\n", result.PID)
	return nil
}

func runStatus(cmd *cobra.Command, _ []string) error {
	dataDir, err := resolveDataDir(dataDirFlag)
	if err != nil {
		return err
	}
	status, err := ReadRuntimeStatus(cmd.Context(), dataDir)
	if err != nil {
		status = &RuntimeStatusSnapshot{Error: err.Error()}
		if jsonFlag {
			return writeJSON(cmd.OutOrStdout(), status)
		}
		return fmt.Errorf("local runtime status unavailable: %w", err)
	}
	if jsonFlag {
		return writeJSON(cmd.OutOrStdout(), status)
	}
	info := status.Runtime
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "status: %s\nurl: %s\npid: %d\nserve_pid: %d\nhealthy: %t\ndata_dir: %s\n",
		info.Status, info.URL, info.PID, info.ServePID, status.Healthy, info.DataDir)
	return nil
}

// ReadRuntimeStatus returns a one-shot local runtime status snapshot.
func ReadRuntimeStatus(ctx context.Context, dataDir string) (*RuntimeStatusSnapshot, error) {
	if dataDir == "" {
		var err error
		dataDir, err = resolveDataDir("")
		if err != nil {
			return nil, err
		}
	}
	info, err := readRuntime(dataDir)
	status := &RuntimeStatusSnapshot{Runtime: runtimeSnapshot(info)}
	if err != nil {
		status.Error = err.Error()
		return status, err
	}
	healthCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := checkRuntimeHealth(healthCtx, info.URL); err != nil {
		status.Error = err.Error()
	} else {
		status.Healthy = true
	}
	return status, nil
}

// RuntimeStartResult describes the outcome of starting the local runtime.
type RuntimeStartResult struct {
	PID            int    `json:"pid"`
	URL            string `json:"url,omitempty"`
	AlreadyRunning bool   `json:"already_running"`
}

// StartRuntime starts the local runtime service if it is not already running.
func StartRuntime(dataDir string, port int) (*RuntimeStartResult, error) {
	return startRuntime(dataDir, port, false)
}

// RestartRuntime stops any recorded live runtime process and starts a new one.
func RestartRuntime(dataDir string, port int) (*RuntimeStartResult, error) {
	return startRuntime(dataDir, port, true)
}

func startRuntime(dataDir string, port int, force bool) (*RuntimeStartResult, error) {
	if dataDir == "" {
		var err error
		dataDir, err = resolveDataDir("")
		if err != nil {
			return nil, err
		}
	}
	if result, err := reuseRunningRuntime(dataDir, force); err != nil || result != nil {
		return result, err
	}
	if _, err := currentFleetDBRedisHash(dataDir); err != nil {
		return nil, fmt.Errorf("load FleetDB Redis settings: %w", err)
	}
	if err := ensureRuntimeDirs(dataDir); err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve loom executable: %w", err)
	}
	return spawnDetachedService(exe, dataDir, port)
}

// reuseRunningRuntime returns an AlreadyRunning result when the previous
// service is still alive and matches our executable+config identity. When
// force is true or identity drifts, it stops the stale process and returns
// (nil, nil) so the caller can spawn a fresh one. Errors propagate.
func reuseRunningRuntime(dataDir string, force bool) (*RuntimeStartResult, error) {
	info, err := readRuntime(dataDir)
	if err != nil {
		return nil, nil
	}
	if !processRunning(info.PID) {
		if processRunning(info.ServePID) {
			if err := stopRuntimeProcesses(info, 15*time.Second); err != nil {
				return nil, fmt.Errorf("stop orphaned local runtime: %w", err)
			}
		}
		return nil, nil
	}
	if !force {
		identity := currentExecutableIdentity()
		redisHash, err := currentFleetDBRedisHash(dataDir)
		if err != nil {
			return nil, fmt.Errorf("load FleetDB Redis settings: %w", err)
		}
		if runtimeMatchesExecutable(info, identity) && runtimeMatchesFleetDBRedisSettings(info, redisHash) {
			return &RuntimeStartResult{PID: info.PID, URL: info.URL, AlreadyRunning: true}, nil
		}
	}
	if err := stopRuntimeProcesses(info, 15*time.Second); err != nil {
		return nil, fmt.Errorf("stop stale local runtime: %w", err)
	}
	return nil, nil
}

// spawnDetachedService launches `loom local service` as a detached child
// process whose lifetime is independent of this CLI invocation.
func spawnDetachedService(exe, dataDir string, port int) (*RuntimeStartResult, error) {
	logFile, err := os.OpenFile(serviceLogPath(dataDir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("open service log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	args := []string{"local", "--data-dir", dataDir, "service"}
	if port > 0 {
		args = append(args, "--port", strconv.Itoa(port))
	}
	// exe is this process's resolved binary path; args are fixed subcommand
	// strings + CLI-validated dataDir / port. The guard refuses to re-exec a
	// *.test binary (fork-bomb protection; see reexec_guard.go).
	service, err := loomReexecCommand(exe, args...)
	if err != nil {
		return nil, err
	}
	service.Env = append(os.Environ(), "LOOM_CONFIG_DIR="+dataDir)
	service.Dir = dataDir
	service.Stdout = logFile
	service.Stderr = logFile
	service.SysProcAttr = newDetachedSysProcAttr()
	if err := service.Start(); err != nil {
		return nil, fmt.Errorf("start local service: %w", err)
	}
	pid := service.Process.Pid
	if err := service.Process.Release(); err != nil {
		return nil, fmt.Errorf("release local service process: %w", err)
	}
	return &RuntimeStartResult{PID: pid}, nil
}

// EnsureRuntimeStarted starts the local runtime if needed and waits until it
// reports healthy or the caller's context expires.
func EnsureRuntimeStarted(ctx context.Context, dataDir string, port int) (*RuntimeStatusSnapshot, error) {
	if dataDir == "" {
		var err error
		dataDir, err = resolveDataDir("")
		if err != nil {
			return nil, err
		}
	}
	status, err := readRuntimeStatusFn(ctx, dataDir)
	if err == nil && status.Healthy {
		return status, nil
	}
	if status != nil && status.Runtime != nil && status.Runtime.PID > 0 {
		if _, err := restartRuntimeFn(dataDir, port); err != nil {
			return status, err
		}
	} else if _, err := StartRuntime(dataDir, port); err != nil {
		return status, err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err = readRuntimeStatusFn(ctx, dataDir)
		if err == nil && status.Healthy {
			return status, nil
		}
		select {
		case <-ctx.Done():
			if err != nil {
				return status, err
			}
			return status, ctx.Err()
		case <-ticker.C:
		}
	}
}

func runStop(cmd *cobra.Command, _ []string) error {
	dataDir, err := resolveDataDir(dataDirFlag)
	if err != nil {
		return err
	}
	info, err := readRuntime(dataDir)
	if err != nil {
		return fmt.Errorf("read runtime: %w", err)
	}
	if !runtimeProcessRunning(info) {
		info.Status = "stopped"
		_ = writeRuntime(dataDir, info)
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Loom local runtime is not running.")
		return nil
	}
	if err := stopRuntimeProcesses(info, 15*time.Second); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Loom local runtime stopped.")
	return nil
}

func stopRuntimeProcess(pid int, timeout time.Duration) error {
	return stopRuntimePIDs([]int{pid}, timeout)
}

func stopRuntimeProcesses(info *runtimeInfo, timeout time.Duration) error {
	return stopRuntimePIDs(runtimePIDs(info), timeout)
}

func runtimeProcessRunning(info *runtimeInfo) bool {
	for _, pid := range runtimePIDs(info) {
		if processRunning(pid) {
			return true
		}
	}
	return false
}

func runtimePIDs(info *runtimeInfo) []int {
	if info == nil {
		return nil
	}
	pids := make([]int, 0, 2)
	seen := map[int]struct{}{}
	for _, pid := range []int{info.PID, info.ServePID} {
		if pid <= 0 {
			continue
		}
		if _, ok := seen[pid]; ok {
			continue
		}
		seen[pid] = struct{}{}
		pids = append(pids, pid)
	}
	return pids
}

func stopRuntimePIDs(pids []int, timeout time.Duration) error {
	if len(pids) == 0 {
		return nil
	}
	for _, pid := range pids {
		if !processRunning(pid) {
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			return fmt.Errorf("find process %d: %w", pid, err)
		}
		if err := proc.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("stop process %d: %w", pid, err)
		}
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		running := runningPIDs(pids)
		if len(running) == 0 {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("local runtime processes %s did not stop within %s", formatPIDs(runningPIDs(pids)), timeout)
}

func runningPIDs(pids []int) []int {
	running := make([]int, 0, len(pids))
	for _, pid := range pids {
		if processRunning(pid) {
			running = append(running, pid)
		}
	}
	return running
}

func formatPIDs(pids []int) string {
	parts := make([]string, 0, len(pids))
	for _, pid := range pids {
		parts = append(parts, strconv.Itoa(pid))
	}
	return strings.Join(parts, ", ")
}

func runInstallService(cmd *cobra.Command, _ []string) error {
	dataDir, err := resolveDataDir(dataDirFlag)
	if err != nil {
		return err
	}
	if err := ensureRuntimeDirs(dataDir); err != nil {
		return err
	}
	return installLaunchAgent(cmd.OutOrStdout(), dataDir, portFlag)
}

func runUninstallService(cmd *cobra.Command, _ []string) error {
	return uninstallLaunchAgent(cmd.OutOrStdout())
}

func updatePauseState(paused bool) error {
	dataDir, err := resolveDataDir(dataDirFlag)
	if err != nil {
		return err
	}
	info, err := readRuntime(dataDir)
	if err != nil {
		return fmt.Errorf("read runtime: %w", err)
	}
	info.ClaimsPaused = paused
	if paused {
		info.Status = "draining"
	} else if processRunning(info.PID) {
		info.Status = "running"
	}
	return writeRuntime(dataDir, info)
}

func runLogs(cmd *cobra.Command, _ []string) error {
	dataDir, err := resolveDataDir(dataDirFlag)
	if err != nil {
		return err
	}
	if err := ensureRuntimeDirs(dataDir); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "service: %s\nserve:   %s\n", serviceLogPath(dataDir), serveLogPath(dataDir))
	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
