package cli

import (
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// DaemonAgentStatus represents the status of a single supervised agent
type DaemonAgentStatus struct {
	Worktree       string    `json:"worktree"`
	Role           string    `json:"role"`
	Repo           string    `json:"repo,omitempty"`
	PID            int       `json:"pid"`
	Status         string    `json:"status"` // "running", "starting", "stopped", "failed"
	TaskID         string    `json:"task_id,omitempty"`
	EpicID         string    `json:"epic_id,omitempty"`
	CurrentBackend string    `json:"current_backend,omitempty"`
	RestartCount   int       `json:"restart_count"`
	LastStart      time.Time `json:"last_start,omitempty"`
	LastExit       time.Time `json:"last_exit,omitempty"`
	LastExitCode   int       `json:"last_exit_code,omitempty"`
}

// DaemonState represents the complete daemon state in daemon-agents.json
type DaemonState struct {
	PID       int                 `json:"pid"`
	StartedAt time.Time           `json:"started_at"`
	Agents    []DaemonAgentStatus `json:"agents"`
}

// Cobra command variables
var daemonDryRun bool

// daemonCmd is the parent command for daemon subcommands
var daemonCmd = &cobra.Command{
	Use:     "daemon",
	Short:   "Manage the agent supervisor daemon",
	GroupID: "agents",
	Long: `Start, stop, and monitor the loom agent supervisor daemon.

The daemon reads loom.yaml in the current project directory and supervises
the configured agents, automatically restarting them on failure with
exponential backoff.

Commands:
  loom daemon           Start the supervisor in the foreground
  loom daemon status    Show daemon and agent status
  loom daemon stop      Stop the running daemon

Configuration is read from loom.yaml in the current directory:
  daemon:
    pid_file: .loom/daemon.pid       # default
    log_dir: .loom/logs              # default
    max_agents: 20                   # default; 0 = unlimited
    restart_policy:
      max_retries: 3                 # default
      backoff_initial: 2             # seconds, default
      backoff_max: 300               # seconds, default

  agents:
    - worktree: falcon
      role: plan
      auto: true
    - worktree: nova
      role: task
      auto: true`,
	Run: runDaemon,
}

// daemonStatusCmd shows daemon and agent status
var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon and agent status",
	Run:   runDaemonStatus,
}

// daemonStopCmd stops the running daemon
var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running daemon",
	Run:   runDaemonStop,
}

func init() {
	daemonCmd.Flags().BoolVar(&daemonDryRun, "dry-run", false,
		"Validate config and print what would be started without actually starting")
	daemonCmd.AddCommand(daemonStatusCmd)
	daemonCmd.AddCommand(daemonStopCmd)
	rootCmd.AddCommand(daemonCmd)
}

func runDaemon(cmd *cobra.Command, args []string) {
	// 0. Create own process group so signals to the parent's group don't kill us.
	// This is critical when the daemon is launched via `loom daemon &` from a
	// script — without this, the daemon shares the script's PGID and any
	// process-group-wide signal can take down the daemon.
	if err := syscall.Setpgid(0, 0); err != nil {
		// Setpgid can fail if we're already a session leader or after exec in
		// some configurations. Fall back to setsid which creates a new session.
		if _, err2 := syscall.Setsid(); err2 != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not isolate process group (setpgid: %v, setsid: %v)\n", err, err2)
		}
	}

	// 1. Get project directory (current working directory)
	projectDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine working directory: %v\n", err)
		os.Exit(1)
	}

	// 2. Load and validate configuration
	config, err := LoadDaemonConfig(projectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: loading config: %v\n", err)
		os.Exit(1)
	}

	if len(config.Agents) == 0 {
		fmt.Fprintf(os.Stderr, "Error: no agents configured in loom.yaml\n")
		os.Exit(1)
	}

	// 3. Resolve paths (PID file, log dir, state file)
	pidFilePath := resolveDaemonPath(projectDir, config.Daemon.PIDFile)
	logDir := resolveDaemonPath(projectDir, config.Daemon.LogDir)
	stateFilePath := ResolveDaemonStatePath(projectDir)
	lockFilePath := filepath.Join(filepath.Dir(pidFilePath), "daemon.lock")

	// 3.5. Validate paths stay within expected boundaries
	validateDaemonPaths(projectDir, pidFilePath, logDir)

	// 4. Dry-run mode: print config and exit (before acquiring lock)
	if daemonDryRun {
		printDryRunInfo(config, pidFilePath, logDir, stateFilePath)
		return
	}

	// 5. Create directories (needed for lock file and PID file)
	if err := os.MkdirAll(filepath.Dir(pidFilePath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: creating PID directory: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(logDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "Error: creating log directory: %v\n", err)
		os.Exit(1)
	}

	// 6. Acquire exclusive lock to prevent concurrent daemon startup
	lockFile, err := os.OpenFile(lockFilePath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: opening lock file: %v\n", err)
		os.Exit(1)
	}
	if err := lockfile.TryLockExclusive(lockFile); err != nil {
		lockFile.Close()
		if err == lockfile.ErrLocked {
			fmt.Fprintf(os.Stderr, "Error: daemon already running (lock held on %s)\n", lockFilePath)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Error: acquiring daemon lock: %v\n", err)
		os.Exit(1)
	}
	// Lock is released when the file descriptor is closed (including on process crash)
	defer lockFile.Close()
	defer os.Remove(lockFilePath)

	// Write daemon info into the lock file
	lockInfo, _ := json.Marshal(lockfile.LockInfo{
		PID:       os.Getpid(),
		StartedAt: time.Now(),
	})
	_, _ = lockFile.Seek(0, 0)
	_ = lockFile.Truncate(0)
	_, _ = lockFile.Write(lockInfo)

	// 7. Clean up any stale PID file from a previous daemon (we hold the lock,
	// so no other daemon is running — any existing PID file is stale)
	os.Remove(pidFilePath)

	// 8. Write PID file
	if err := writePIDFile(pidFilePath, os.Getpid()); err != nil {
		fmt.Fprintf(os.Stderr, "Error: writing PID file: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(pidFilePath)
	defer os.Remove(stateFilePath)

	// 9. Setup signal handler
	shutdown := SetupSignalHandler()

	// 9.5. Initialize event bus for structured observability
	eventsDir := resolveDaemonPath(projectDir, config.Daemon.EventsDir)
	eventBus := events.NewBus(eventsDir)
	defer func() { _ = eventBus.Close() }()

	// 9.6. Initialize OTel exporter if enabled
	if otelExp := initOTelExporter(config, eventBus); otelExp != nil {
		defer stopOTelExporter(otelExp)
	}

	// 9.7. Start fleet-db backend if enabled (soft-failure, like OTel)
	var fleetDBSrv *FleetDBServer
	fleetCfg, fleetEnabled := resolveFleetDBConfig(&config.Daemon)
	if fleetEnabled {
		fleetCfg.DBPath = filepath.Join(projectDir, ".loom", "fleetdb.sqlite")
		fleetCfg.SocketPath = filepath.Join(projectDir, ".loom", "fleetdb.sock")
		var fleetErr error
		fleetDBSrv, fleetErr = NewFleetDBServer(fleetCfg, slog.Default())
		if fleetErr != nil {
			log.Printf("warning: failed to start fleet-db server: %v (continuing without fleet-db)", fleetErr)
			fleetDBSrv = nil
		} else {
			setDefaultTracker(fleetDBSrv.Backend())
			defer fleetDBSrv.Stop()
			log.Printf("fleet-db backend active (workspace: %s)", fleetCfg.Workspace)
		}
	}

	// 10. Create and start daemon (from daemon.go)
	daemon, err := NewDaemon(config, projectDir, eventBus)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: creating daemon: %v\n", err)
		os.Exit(1)
	}

	// 11. Print startup banner
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("Loom Agent Supervisor")
	fmt.Printf("PID: %d\n", os.Getpid())
	fmt.Printf("Config: %s/loom.yaml\n", projectDir)
	fmt.Printf("Agents: %d\n", len(config.Agents))
	for _, a := range config.Agents {
		fmt.Printf("  - %s (%s)\n", a.Worktree, a.Role)
	}
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println("═══════════════════════════════════════════════════════════════")

	// Extract configured max retries (default 3)
	maxRetries := 3
	if config.Daemon.RestartPolicy.MaxRetries != nil {
		maxRetries = *config.Daemon.RestartPolicy.MaxRetries
	}

	// Write initial state file
	startedAt := time.Now()
	if err := writeStateFile(stateFilePath, startedAt, daemon.Agents(), maxRetries); err != nil {
		fmt.Printf("Warning: failed to write initial state file: %v\n", err)
	}

	// 12. Start daemon
	if err := daemon.Start(); err != nil {
		// Clean up before exiting (os.Exit doesn't run defers)
		if fleetDBSrv != nil {
			fleetDBSrv.Stop()
		}
		os.Remove(pidFilePath)
		os.Remove(stateFilePath)
		lockFile.Close()
		os.Remove(lockFilePath)
		fmt.Fprintf(os.Stderr, "Error: starting daemon: %v\n", err)
		os.Exit(1)
	}

	// Start state file updater goroutine
	stateUpdateDone := startStateUpdater(shutdown, stateFilePath, startedAt, daemon, maxRetries)

	// 13. Wait for shutdown signal
	<-shutdown
	fmt.Println("\nShutting down...")
	daemon.Stop()
	<-stateUpdateDone
	fmt.Println("Daemon stopped.")
}

func runDaemonStatus(cmd *cobra.Command, args []string) {
	projectDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine working directory: %v\n", err)
		os.Exit(1)
	}

	// Use shared runtime detection (lockfile → state → PID fallback)
	rt := DetectDaemonRuntime(projectDir)
	if !rt.Running {
		fmt.Println("Daemon: not running")
		return
	}

	fmt.Printf("Daemon: running (PID %d)\n", rt.PID)

	// Read and display agent status
	stateFilePath := ResolveDaemonStatePath(projectDir)
	state, err := readStateFile(stateFilePath)
	if err != nil {
		fmt.Printf("  (no agent status available: %v)\n", err)
		return
	}

	fmt.Printf("Started: %s\n", state.StartedAt.Format(time.RFC3339))
	fmt.Printf("Agents: %d\n", len(state.Agents))
	fmt.Println("")

	// Format agent table
	for _, agent := range state.Agents {
		statusIcon := statusToIcon(agent.Status)
		fmt.Printf("  %s %s (%s)\n", statusIcon, agent.Worktree, agent.Role)
		if agent.PID > 0 {
			fmt.Printf("      PID: %d\n", agent.PID)
		}
		if agent.EpicID != "" {
			fmt.Printf("      Epic: %s\n", agent.EpicID)
		}
		if agent.TaskID != "" {
			fmt.Printf("      Task: %s\n", agent.TaskID)
		}
		if agent.RestartCount > 0 {
			fmt.Printf("      Restarts: %d\n", agent.RestartCount)
		}
	}
}

func statusToIcon(status string) string {
	switch status {
	case "running":
		return "●"
	case "starting":
		return "◐"
	case "stopped":
		return "○"
	case "failed":
		return "✗"
	default:
		return "?"
	}
}

func runDaemonStop(cmd *cobra.Command, args []string) {
	projectDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine working directory: %v\n", err)
		os.Exit(1)
	}

	// Use shared runtime detection (lockfile → state → PID fallback)
	rt := DetectDaemonRuntime(projectDir)
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

	fmt.Printf("Stopping daemon (PID %d)...\n", rt.PID)

	// Send SIGTERM for graceful shutdown
	if err := syscall.Kill(rt.PID, syscall.SIGTERM); err != nil {
		fmt.Fprintf(os.Stderr, "Error: sending SIGTERM: %v\n", err)
		os.Exit(1)
	}

	// Wait for daemon to exit (up to 30 seconds)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if !lockfile.IsProcessRunning(rt.PID) {
			fmt.Println("Daemon stopped.")
			return
		}
		time.Sleep(500 * time.Millisecond)
	}

	fmt.Fprintf(os.Stderr, "Warning: daemon did not stop within 30 seconds\n")
	fmt.Fprintf(os.Stderr, "You may need to kill it manually: kill -9 %d\n", rt.PID)
	os.Exit(1)
}
