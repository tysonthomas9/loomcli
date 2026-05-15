package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// DaemonAgentStatus represents the status of a single supervised agent
type DaemonAgentStatus struct {
	Worktree               string    `json:"worktree"`
	Role                   string    `json:"role"`
	Repo                   string    `json:"repo,omitempty"`
	PID                    int       `json:"pid"`
	Status                 string    `json:"status"` // "running", "starting", "stopped", "failed"
	TaskID                 string    `json:"task_id,omitempty"`
	EpicID                 string    `json:"epic_id,omitempty"`
	CurrentBackend         string    `json:"current_backend,omitempty"`
	RestartCount           int       `json:"restart_count"`
	LastStart              time.Time `json:"last_start,omitempty"`
	LastExit               time.Time `json:"last_exit,omitempty"`
	LastExitCode           int       `json:"last_exit_code,omitempty"`
	StopReason             string    `json:"stop_reason,omitempty"`
	StoppedAt              time.Time `json:"stopped_at,omitempty"`
	WorktreePath           string    `json:"worktree_path,omitempty"`
	LastErrorClass         string    `json:"last_error_class,omitempty"`
	NoWorkCount            int       `json:"no_work_count,omitempty"`
	BackoffUntil           time.Time `json:"backoff_until,omitempty"`
	RemoteBranch           string    `json:"remote_branch,omitempty"`
	OwnershipLeaseID       string    `json:"ownership_lease_id,omitempty"`
	OwnershipFencingToken  int64     `json:"ownership_fencing_token,omitempty"`
	OwnershipLastHeartbeat time.Time `json:"ownership_last_heartbeat,omitempty"`
}

// DaemonState represents the complete daemon state in daemon-agents.json
type DaemonState struct {
	PID       int                 `json:"pid"`
	StartedAt time.Time           `json:"started_at"`
	Agents    []DaemonAgentStatus `json:"agents"`
}

// Cobra command variables
var daemonDryRun bool
var daemonStopForce bool
var daemonStopTimeout int // seconds; 0 = default (60)

// daemonCmd is the parent command for daemon subcommands
var daemonCmd = &cobra.Command{
	Use:     "daemon",
	Short:   "Manage the agent supervisor daemon",
	GroupID: "agents",
	Long: `Start, stop, and monitor the loom agent supervisor daemon.

The daemon reads FleetDB daemon profiles for the active workspace and
supervises the configured agents, automatically restarting them on failure
with exponential backoff.

Commands:
  loom daemon                      Start the supervisor in the foreground
  loom daemon status               Show daemon and agent status
  loom daemon logs                 View agent logs
  loom daemon stop                 Stop the running daemon
  loom daemon stop <agent>         Stop a single agent
  loom daemon start <agent>        Start a previously stopped agent
  loom daemon restart <agent>      Restart a single agent with fresh state
  loom daemon queue <agent>        Preview an agent's filtered work queue

Configuration is managed with FleetDB-backed daemon, role, and agent commands.`,
	Run: runDaemon,
}

// daemonStatusCmd shows daemon and agent status
var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon and agent status",
	Run:   runDaemonStatus,
}

// daemonStopCmd stops the running daemon or a single agent
var daemonStopCmd = &cobra.Command{
	Use:   "stop [agent-name]",
	Short: "Stop the running daemon or a single agent",
	Long: `Stop the running daemon (no arguments) or stop a single agent by name.

Examples:
  loom daemon stop          Stop the entire daemon
  loom daemon stop falcon   Stop only the "falcon" agent`,
	Run: runDaemonStop,
}

// daemonAgentStartCmd starts a previously stopped agent
var daemonAgentStartCmd = &cobra.Command{
	Use:   "start <agent-name>",
	Short: "Start a previously stopped agent",
	Args:  cobra.ExactArgs(1),
	Run:   runDaemonAgentStart,
}

// daemonAgentRestartCmd restarts a single agent with fresh state
var daemonAgentRestartCmd = &cobra.Command{
	Use:   "restart <agent-name>",
	Short: "Restart a single agent with fresh counters",
	Args:  cobra.ExactArgs(1),
	Run:   runDaemonAgentRestart,
}

// daemonConfigCmd shows the effective resolved daemon configuration
var daemonConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Show effective running configuration",
	Long: `Display the fully resolved FleetDB-backed daemon configuration as YAML.

Shows the active workspace daemon profile, roles, agents, and defaults.
Sensitive values (Redis URLs) are masked. The command works whether or not the
daemon is running.`,
	Run: runDaemonConfig,
}

func init() {
	daemonCmd.Flags().BoolVar(&daemonDryRun, "dry-run", false,
		"Validate config and print what would be started without actually starting")
	daemonCmd.AddCommand(daemonStatusCmd)
	daemonCmd.AddCommand(daemonLogsCmd)
	daemonStopCmd.Flags().BoolVarP(&daemonStopForce, "force", "f", false,
		"Skip graceful yield, send SIGTERM immediately (agent) or SIGKILL (daemon)")
	daemonStopCmd.Flags().IntVarP(&daemonStopTimeout, "timeout", "t", 0,
		"Yield timeout in seconds (default: 60; only for agent stop)")
	daemonCmd.AddCommand(daemonStopCmd)
	daemonCmd.AddCommand(daemonAgentStartCmd)
	daemonCmd.AddCommand(daemonAgentRestartCmd)
	daemonCmd.AddCommand(daemonQueueCmd)
	daemonCmd.AddCommand(daemonConfigCmd)
	cli.RegisterCommand(daemonCmd)
}

// setupSignalHandler installs signal handlers for the daemon process.
//
// SIGINT/SIGTERM/SIGHUP close the returned shutdown channel so the main loop
// can run graceful shutdown.
//
// SIGUSR1 triggers an on-demand goroutine stack dump to the daemon log. This
// is the user-facing escape hatch when the daemon appears wedged — `kill
// -USR1 <pid>` produces a full stack trace without needing pprof or a debug
// port. The handler does NOT close shutdown; the daemon keeps running.
func setupSignalHandler() chan struct{} {
	shutdown := make(chan struct{})
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	dumpChan := make(chan os.Signal, 1)
	signal.Notify(dumpChan, syscall.SIGUSR1)

	go func() {
		sig := <-sigChan
		signal.Stop(sigChan)
		log.Printf("[daemon] Shutdown signal received: %v (PID=%d, PGID=%d)", sig, os.Getpid(), syscall.Getpgrp())
		fmt.Printf("\n[daemon] Shutdown signal received (%v), stopping gracefully...\n", sig)
		close(shutdown)
	}()

	go func() {
		for sig := range dumpChan {
			log.Printf("[daemon] SIGUSR1 received (%v) — dumping goroutines", sig)
			supervisor.DumpGoroutinesToLog("SIGUSR1")
		}
	}()

	return shutdown
}

func runDaemon(cmd *cobra.Command, args []string) {
	// runDaemonBody returns a non-zero code when a critical supervisor
	// goroutine died. Its defers (lock file, PID file, state file cleanup)
	// run before this function returns, so os.Exit below fires only after
	// the daemon has cleaned up its on-disk footprint.
	_ = cmd
	_ = args
	exitCode := runDaemonBody()
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

// runDaemonBody is the body of `loom daemon`, factored out so its defers can
// run before runDaemon calls os.Exit on a fatal supervisor failure.
func runDaemonBody() int {
	isolateProcessGroup()

	projectDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine working directory: %v\n", err)
		return 1
	}

	config, err := cfgpkg.LoadDaemonConfig(projectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: loading config: %v\n", err)
		return 1
	}

	if len(config.Agents) == 0 {
		fmt.Fprintf(os.Stderr, "Error: no agents configured in FleetDB for the active workspace\n")
		return 1
	}

	paths := resolveDaemonPaths(projectDir, config)
	ValidateDaemonPaths(projectDir, paths.pidFile, paths.logDir)

	if daemonDryRun {
		printDryRunInfo(config, paths.pidFile, paths.logDir, paths.stateFile)
		return 0
	}

	prepareDaemonDirs(paths.pidFile, paths.logDir)
	lockFile := acquireDaemonLock(paths.lockFile)
	defer lockFile.Close()
	defer os.Remove(paths.lockFile)

	initPIDFile(paths.pidFile)
	defer os.Remove(paths.pidFile)
	// runDaemonMainLoop waits for the state updater to stop before returning;
	// keep that invariant so this deferred cleanup cannot race a state write.
	defer os.Remove(paths.stateFile)

	shutdown, daemon := initDaemonServices(config, projectDir, paths)

	return runDaemonMainLoop(config, projectDir, paths, shutdown, daemon, lockFile)
}

// awaitDaemonExit blocks until the daemon's main loop should exit. Returns 0
// on graceful shutdown (shutdown channel closed) or 2 if the supervisor
// signaled a fatal error (panic in a critical goroutine, unexpected return,
// or liveness watchdog timeout).
func awaitDaemonExit(shutdown <-chan struct{}, fatalCh <-chan error) int {
	select {
	case <-shutdown:
		fmt.Println("\nShutting down...")
		return 0
	case err := <-fatalCh:
		log.Printf("[daemon] FATAL: %v — exiting non-zero", err)
		fmt.Fprintf(os.Stderr, "\n[daemon] FATAL: %v\n", err)
		return 2
	}
}

// daemonPaths holds resolved filesystem paths for the daemon.
type daemonPaths struct {
	pidFile   string
	logDir    string
	stateFile string
	lockFile  string
	eventsDir string
}

// resolveDaemonPaths resolves all daemon-related filesystem paths.
func resolveDaemonPaths(projectDir string, config *cfgpkg.DaemonConfig) daemonPaths {
	pidFile := supervisor.ResolveDaemonPath(projectDir, config.Daemon.PIDFile)
	return daemonPaths{
		pidFile:   pidFile,
		logDir:    supervisor.ResolveDaemonPath(projectDir, config.Daemon.LogDir),
		stateFile: cfgpkg.ResolveDaemonStatePath(projectDir),
		lockFile:  filepath.Join(filepath.Dir(pidFile), "daemon.lock"),
		eventsDir: supervisor.ResolveDaemonPath(projectDir, config.Daemon.EventsDir),
	}
}

// initPIDFile removes any stale PID file and writes the current PID.
func initPIDFile(pidFilePath string) {
	os.Remove(pidFilePath)
	if err := writePIDFile(pidFilePath, os.Getpid()); err != nil {
		fmt.Fprintf(os.Stderr, "Error: writing PID file: %v\n", err)
		os.Exit(1)
	}
}

// initDaemonServices sets up shutdown handler, event bus, fleet-db store access, and creates the daemon.
func initDaemonServices(config *cfgpkg.DaemonConfig, projectDir string, paths daemonPaths) (chan struct{}, *Daemon) {
	shutdown := setupSignalHandler()

	eventBus := events.NewBus(paths.eventsDir)

	if otelExp := initOTelExporter(config, eventBus); otelExp != nil {
		// Caller cannot defer this, but otel exporter registers its own shutdown hook
		_ = otelExp
	}

	storeHandle, storeErr := cmdstore.OpenStore(cmdstore.RootContext())
	if storeErr != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to open fleet-db store for daemon: %v\n", storeErr)
		os.Exit(1)
	}
	var st store.Store
	if storeHandle != nil {
		st = storeHandle.Store
	}

	daemon, err := NewDaemon(config, projectDir, eventBus, cli.DefaultIssueBackend(), st)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: creating daemon: %v\n", err)
		os.Exit(1)
	}

	return shutdown, daemon
}

// runDaemonMainLoop starts the daemon, state updater, and waits for shutdown.
// Returns a process exit code: 0 on graceful shutdown, 2 if a critical
// supervisor goroutine died (panic, unexpected return, or liveness watchdog
// timeout).
func runDaemonMainLoop(config *cfgpkg.DaemonConfig, projectDir string, paths daemonPaths, shutdown chan struct{}, daemon *Daemon, lockFile *os.File) int {
	cli.PrintDaemonBanner(config, projectDir)

	maxRetries := 3
	if config.Daemon.RestartPolicy.MaxRetries != nil {
		maxRetries = *config.Daemon.RestartPolicy.MaxRetries
	}

	startedAt := time.Now()
	if err := writeStateFile(paths.stateFile, startedAt, daemon.Agents(), maxRetries); err != nil {
		fmt.Printf("Warning: failed to write initial state file: %v\n", err)
	}

	if err := daemon.Start(); err != nil {
		cleanupOnStartFailure(paths.pidFile, paths.stateFile, lockFile, paths.lockFile)
		fmt.Fprintf(os.Stderr, "Error: starting daemon: %v\n", err)
		return 1
	}

	startDaemonSockets(daemon, projectDir, config)
	stateUpdateDone := startStateUpdater(shutdown, paths.stateFile, startedAt, daemon, maxRetries)

	exitCode := awaitDaemonExit(shutdown, daemon.sup.FatalChannel())

	// Bounded graceful drain. If daemon.Stop() hangs (e.g. AgentsMu is
	// deadlocked), still exit so the user sees the failure rather than a
	// process that refuses to die.
	stopDone := make(chan struct{})
	go func() {
		daemon.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(30 * time.Second):
		log.Printf("[daemon] daemon.Stop() did not return within 30s; forcing exit")
	}

	// stateUpdateDone closes only when the state updater observes shutdown
	// being closed; on the FatalCh path, shutdown is still open. Close it
	// here so the updater exits cleanly.
	select {
	case <-shutdown:
		// already closed
	default:
		close(shutdown)
	}
	select {
	case <-stateUpdateDone:
	case <-time.After(10 * time.Second):
		log.Printf("[daemon] state updater did not exit within 10s; forcing exit")
	}

	if exitCode == 0 {
		fmt.Println("Daemon stopped.")
	}
	return exitCode
}

// acquireDaemonLock opens and locks the daemon lock file, writing lock info.
func acquireDaemonLock(lockFilePath string) *os.File {
	lf, err := os.OpenFile(lockFilePath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: opening lock file: %v\n", err)
		os.Exit(1)
	}
	if err := lockfile.TryLockExclusive(lf); err != nil {
		lf.Close()
		if err == lockfile.ErrLocked {
			fmt.Fprintf(os.Stderr, "Error: daemon already running (lock held on %s)\n", lockFilePath)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Error: acquiring daemon lock: %v\n", err)
		os.Exit(1)
	}

	lockInfo, _ := json.Marshal(lockfile.LockInfo{
		PID:       os.Getpid(),
		StartedAt: time.Now(),
	})
	_, _ = lf.Seek(0, 0)
	_ = lf.Truncate(0)
	_, _ = lf.Write(lockInfo)

	return lf
}

// startDaemonSockets starts the control socket and IPC socket servers.
func startDaemonSockets(daemon *Daemon, projectDir string, config *cfgpkg.DaemonConfig) {
	socketPath := resolveDaemonSocketPath(projectDir, config.Daemon.PIDFile)
	if err := daemon.startControlServer(socketPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: control socket unavailable: %v\n", err)
	}

	wireDaemonNotifyBus(daemon)

	ipcSocketPath := resolveAgentIPCSocketPath(projectDir, config.Daemon.PIDFile)
	if err := daemon.startIPCServer(ipcSocketPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: agent IPC socket unavailable: %v\n", err)
	} else {
		daemon.sup.IpcSocketPath = ipcSocketPath
	}
}

func runDaemonStatus(cmd *cobra.Command, args []string) {
	projectDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine working directory: %v\n", err)
		os.Exit(1)
	}

	// Use shared runtime detection (lockfile -> state -> PID fallback)
	rt := cli.DetectDaemonRuntime(projectDir)
	if !rt.Running {
		fmt.Println("Daemon: not running")
		return
	}

	fmt.Printf("Daemon: running (PID %d)\n", rt.PID)

	// Read and display agent status
	stateFilePath := cfgpkg.ResolveDaemonStatePath(projectDir)
	state, err := ReadStateFile(stateFilePath)
	if err != nil {
		fmt.Printf("  (no agent status available: %v)\n", err)
		return
	}

	fmt.Printf("Started: %s\n", state.StartedAt.Format(time.RFC3339))
	fmt.Printf("Agents: %d\n", len(state.Agents))
	fmt.Println("")

	// Format agent table
	for _, agent := range state.Agents {
		printAgentStatus(agent)
	}
}

func runDaemonStop(cmd *cobra.Command, args []string) {
	// If an agent name is provided, stop that single agent via control socket
	if len(args) > 0 {
		// --timeout flag: 0 = force (skip yield), >0 = custom, unset = default 60s
		timeout := 60 * time.Second
		if cmd.Flags().Changed("timeout") {
			timeout = time.Duration(daemonStopTimeout) * time.Second
		}
		runDaemonAgentStop(args[0], daemonStopForce, timeout)
		return
	}

	projectDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine working directory: %v\n", err)
		os.Exit(1)
	}

	// Use shared runtime detection (lockfile -> state -> PID fallback)
	rt := cli.DetectDaemonRuntime(projectDir)
	if !rt.Running {
		fmt.Println("Daemon is not running.")
		return
	}

	if rt.PID == 0 {
		fmt.Fprintf(os.Stderr, "Error: daemon appears to be running (detected via %s) but PID could not be determined.\n", rt.Source)
		fmt.Fprintf(os.Stderr, "To recover, inspect or remove .loom/daemon.lock and retry:\n")
		fmt.Fprintf(os.Stderr, "  cat .loom/daemon.lock        # check daemon metadata\n")
		fmt.Fprintf(os.Stderr, "  rm .loom/daemon.lock          # force-clear stale lock\n")
		os.Exit(1)
	}

	if daemonStopForce {
		stopDaemonForce(rt.PID)
	} else {
		stopDaemonGraceful(rt.PID)
	}
}

func runDaemonConfig(cmd *cobra.Command, args []string) {
	projectDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine working directory: %v\n", err)
		os.Exit(1)
	}

	config, err := cfgpkg.LoadDaemonConfig(projectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: loading config: %v\n", err)
		os.Exit(1)
	}

	display := resolvedConfigForDisplay(config)

	data, err := yaml.Marshal(display)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: marshaling config: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(string(data))
}
