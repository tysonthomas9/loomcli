package local

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
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

	dataDir, err := resolveDataDir(dataDirFlag)
	if err != nil {
		return err
	}
	if err := ensureRuntimeDirs(dataDir); err != nil {
		return err
	}
	_ = os.Setenv("LOOM_CONFIG_DIR", dataDir)
	_ = os.Setenv("LOOM_DESKTOP_DATA_DIR", dataDir)
	_ = os.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", dataDir)
	if os.Getenv("FLEET_DB_BIN") == "" {
		if fleetDBBin := bundledExecutable("fleet-db"); fleetDBBin != "" {
			_ = os.Setenv("FLEET_DB_BIN", fleetDBBin)
		}
	}
	if bindFlag == "" {
		bindFlag = "127.0.0.1"
	}
	port := portFlag
	if port == 0 {
		_, p, err := netutil.PickFreeLoopbackPort()
		if err != nil {
			return fmt.Errorf("pick local runtime port: %w", err)
		}
		port = p
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve loom executable: %w", err)
	}
	identity := currentExecutableIdentity()
	redisHash, err := currentFleetDBRedisHash(dataDir)
	if err != nil {
		return fmt.Errorf("load FleetDB Redis settings: %w", err)
	}
	logFile, err := os.OpenFile(serveLogPath(dataDir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open serve log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	url := "http://" + net.JoinHostPort(bindFlag, strconv.Itoa(port))
	info := &runtimeInfo{
		Status:    "starting",
		PID:       os.Getpid(),
		DataDir:   dataDir,
		URL:       url,
		Port:      port,
		StartedAt: time.Now().UTC(),
	}
	applyExecutableIdentity(info, identity)
	info.FleetDBRedisHash = redisHash
	if err := writeRuntime(dataDir, info); err != nil {
		return err
	}

	serveCmd := exec.CommandContext(serviceCtx, exe, "serve",
		"--bind", bindFlag,
		"--port", strconv.Itoa(port),
		"--fleet-mode",
	)
	serveCmd.Env = localEnv(dataDir, port)
	serveCmd.Stdout = logFile
	serveCmd.Stderr = logFile
	serveCmd.SysProcAttr = newDetachedSysProcAttr()
	if err := serveCmd.Start(); err != nil {
		info.Status = "failed"
		info.Error = err.Error()
		_ = writeRuntime(dataDir, info)
		return fmt.Errorf("start loom serve: %w", err)
	}

	info.Status = "starting"
	info.ServePID = serveCmd.Process.Pid
	if err := writeRuntime(dataDir, info); err != nil {
		_ = serveCmd.Process.Kill()
		return err
	}

	waitCtx, cancel := context.WithTimeout(serviceCtx, 30*time.Second)
	waitErr := waitForRuntime(waitCtx, url)
	cancel()
	if waitErr != nil {
		info.Status = "failed"
		info.Error = waitErr.Error()
		_ = writeRuntime(dataDir, info)
		_ = serveCmd.Process.Kill()
		return fmt.Errorf("local runtime did not become healthy: %w", waitErr)
	}

	info.Status = "running"
	info.Error = ""
	if err := writeRuntime(dataDir, info); err != nil {
		_ = serveCmd.Process.Kill()
		return err
	}
	startLocalDaemonSupervisor(serviceCtx, dataDir, exe, port)
	fmt.Fprintf(cmd.OutOrStdout(), "Loom local runtime: %s\n", url)

	err = serveCmd.Wait()
	info.Status = "stopped"
	info.Error = ""
	if err != nil && serviceCtx.Err() == nil {
		info.Status = "failed"
		info.Error = err.Error()
	}
	_ = writeRuntime(dataDir, info)
	if serviceCtx.Err() != nil {
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
		fmt.Fprintf(cmd.OutOrStdout(), "Loom local runtime already running: %s\n", result.URL)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Started Loom local service (pid %d)\n", result.PID)
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
	fmt.Fprintf(cmd.OutOrStdout(), "status: %s\nurl: %s\npid: %d\nserve_pid: %d\nhealthy: %t\ndata_dir: %s\n",
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
	if dataDir == "" {
		var err error
		dataDir, err = resolveDataDir("")
		if err != nil {
			return nil, err
		}
	}
	if info, err := readRuntime(dataDir); err == nil && processRunning(info.PID) {
		identity := currentExecutableIdentity()
		redisHash, err := currentFleetDBRedisHash(dataDir)
		if err != nil {
			return nil, fmt.Errorf("load FleetDB Redis settings: %w", err)
		}
		if runtimeMatchesExecutable(info, identity) && runtimeMatchesFleetDBRedisSettings(info, redisHash) {
			return &RuntimeStartResult{PID: info.PID, URL: info.URL, AlreadyRunning: true}, nil
		}
		if err := stopRuntimeProcess(info.PID, 15*time.Second); err != nil {
			return nil, fmt.Errorf("stop stale local runtime: %w", err)
		}
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
	logFile, err := os.OpenFile(serviceLogPath(dataDir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open service log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	args := []string{"local", "--data-dir", dataDir, "service"}
	if port > 0 {
		args = append(args, "--port", strconv.Itoa(port))
	}
	service := exec.Command(exe, args...)
	service.Env = append(os.Environ(), "LOOM_CONFIG_DIR="+dataDir)
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
	status, err := ReadRuntimeStatus(ctx, dataDir)
	if err == nil && status.Healthy {
		return status, nil
	}
	if _, err := StartRuntime(dataDir, port); err != nil {
		return status, err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err = ReadRuntimeStatus(ctx, dataDir)
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
	if !processRunning(info.PID) {
		info.Status = "stopped"
		_ = writeRuntime(dataDir, info)
		fmt.Fprintln(cmd.OutOrStdout(), "Loom local runtime is not running.")
		return nil
	}
	if err := stopRuntimeProcess(info.PID, 15*time.Second); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Loom local runtime stopped.")
	return nil
}

func stopRuntimeProcess(pid int, timeout time.Duration) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop process %d: %w", pid, err)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processRunning(pid) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("local runtime did not stop within %s", timeout)
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
	fmt.Fprintf(cmd.OutOrStdout(), "service: %s\nserve:   %s\n", serviceLogPath(dataDir), serveLogPath(dataDir))
	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
