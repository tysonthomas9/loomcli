package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// DaemonAgentStatus represents the status of a single supervised agent
type DaemonAgentStatus struct {
	Worktree     string    `json:"worktree"`
	Role         string    `json:"role"`
	PID          int       `json:"pid"`
	Status       string    `json:"status"` // "running", "starting", "stopped", "failed"
	TaskID       string    `json:"task_id,omitempty"`
	EpicID       string    `json:"epic_id,omitempty"`
	RestartCount int       `json:"restart_count"`
	LastStart    time.Time `json:"last_start,omitempty"`
	LastExit     time.Time `json:"last_exit,omitempty"`
	LastExitCode int       `json:"last_exit_code,omitempty"`
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

	// 3. Resolve paths (PID file, log dir)
	pidFilePath := resolveDaemonPath(projectDir, config.Daemon.PIDFile)
	logDir := resolveDaemonPath(projectDir, config.Daemon.LogDir)
	stateFilePath := filepath.Join(filepath.Dir(pidFilePath), "daemon-agents.json")
	lockFilePath := filepath.Join(filepath.Dir(pidFilePath), "daemon.lock")

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

	// 10. Create and start daemon (from daemon.go)
	daemon, err := NewDaemon(config, projectDir)
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

	// Write initial state file
	startedAt := time.Now()
	if err := writeStateFile(stateFilePath, startedAt, daemon.Agents()); err != nil {
		fmt.Printf("Warning: failed to write initial state file: %v\n", err)
	}

	// 12. Start daemon
	if err := daemon.Start(); err != nil {
		// Clean up files before exiting (os.Exit doesn't run defers)
		os.Remove(pidFilePath)
		os.Remove(stateFilePath)
		lockFile.Close()
		os.Remove(lockFilePath)
		fmt.Fprintf(os.Stderr, "Error: starting daemon: %v\n", err)
		os.Exit(1)
	}

	// Start state file updater goroutine
	stateUpdateDone := make(chan struct{})
	go func() {
		defer close(stateUpdateDone)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-shutdown:
				return
			case <-ticker.C:
				if err := writeStateFile(stateFilePath, startedAt, daemon.Agents()); err != nil {
					fmt.Printf("Warning: failed to update state file: %v\n", err)
				}
			}
		}
	}()

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
	config, err := LoadDaemonConfig(projectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load config: %v\n", err)
		// Continue anyway to check PID file with defaults
		config = &DaemonConfig{
			Daemon: DaemonSettings{
				PIDFile: ".loom/daemon.pid",
			},
		}
	}

	pidFilePath := resolveDaemonPath(projectDir, config.Daemon.PIDFile)
	stateFilePath := filepath.Join(filepath.Dir(pidFilePath), "daemon-agents.json")

	// Check if daemon is running
	pid, running := isLoomDaemonRunning(pidFilePath)
	if !running {
		fmt.Println("Daemon: not running")
		return
	}

	fmt.Printf("Daemon: running (PID %d)\n", pid)

	// Read and display agent status
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
	config, _ := LoadDaemonConfig(projectDir)
	if config == nil {
		config = &DaemonConfig{
			Daemon: DaemonSettings{
				PIDFile: ".loom/daemon.pid",
			},
		}
	}

	pidFilePath := resolveDaemonPath(projectDir, config.Daemon.PIDFile)

	pid, running := isLoomDaemonRunning(pidFilePath)
	if !running {
		fmt.Println("Daemon is not running.")
		return
	}

	fmt.Printf("Stopping daemon (PID %d)...\n", pid)

	// Send SIGTERM for graceful shutdown
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		fmt.Fprintf(os.Stderr, "Error: sending SIGTERM: %v\n", err)
		os.Exit(1)
	}

	// Wait for daemon to exit (up to 30 seconds)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if !IsProcessRunning(pid) {
			fmt.Println("Daemon stopped.")
			return
		}
		time.Sleep(500 * time.Millisecond)
	}

	fmt.Fprintf(os.Stderr, "Warning: daemon did not stop within 30 seconds\n")
	fmt.Fprintf(os.Stderr, "You may need to kill it manually: kill -9 %d\n", pid)
	os.Exit(1)
}

// resolveDaemonPath resolves a path relative to projectDir, or returns as-is if absolute
func resolveDaemonPath(projectDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(projectDir, path)
}

// isLoomDaemonRunning checks if a loom daemon is running by reading PID file and checking process
func isLoomDaemonRunning(pidFilePath string) (int, bool) {
	data, err := os.ReadFile(pidFilePath)
	if err != nil {
		return 0, false
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}

	// Validate PID is positive (negative PIDs are invalid)
	if pid <= 0 {
		return 0, false
	}

	return pid, IsProcessRunning(pid)
}

// writePIDFile atomically writes the PID file
func writePIDFile(path string, pid int) error {
	// Write to temp file first, then rename for atomicity
	// Include PID in temp filename to avoid race conditions with concurrent daemons
	tempFile := fmt.Sprintf("%s.%d.tmp", path, pid)
	if err := os.WriteFile(tempFile, []byte(strconv.Itoa(pid)+"\n"), 0600); err != nil {
		return err
	}
	if err := os.Rename(tempFile, path); err != nil {
		os.Remove(tempFile) // Clean up temp file on rename failure
		return err
	}
	return nil
}

// readStateFile reads the daemon-agents.json state file
func readStateFile(path string) (*DaemonState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var state DaemonState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// writeStateFile writes the daemon-agents.json state file
func writeStateFile(path string, startedAt time.Time, agents []SupervisedAgentStatus) error {
	state := DaemonState{
		PID:       os.Getpid(),
		StartedAt: startedAt,
		Agents:    make([]DaemonAgentStatus, len(agents)),
	}

	for i, ap := range agents {
		state.Agents[i] = DaemonAgentStatus{
			Worktree:     ap.Worktree,
			Role:         ap.Role,
			PID:          ap.PID,
			Status:       computeAgentStatus(ap),
			EpicID:       ap.AssignedEpicID,
			RestartCount: ap.RestartCount,
			LastStart:    ap.LastStart,
			LastExit:     ap.LastExit,
			LastExitCode: ap.LastExitCode,
		}
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	// Atomic write via temp file (include PID to avoid race conditions)
	tempFile := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tempFile, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tempFile, path); err != nil {
		os.Remove(tempFile) // Clean up temp file on rename failure
		return err
	}
	return nil
}

// computeAgentStatus determines the status string based on agent state
func computeAgentStatus(ap SupervisedAgentStatus) string {
	if ap.PID > 0 && IsProcessRunning(ap.PID) {
		return "running"
	}
	// Not running - check if it failed
	if ap.RestartCount > 3 { // default max retries
		return "failed"
	}
	return "stopped"
}

// printDryRunInfo displays what would happen in dry-run mode
func printDryRunInfo(config *DaemonConfig, pidFile, logDir, stateFile string) {
	fmt.Println("DRY RUN - No daemon will be started")
	fmt.Println("")
	fmt.Println("Configuration:")
	fmt.Printf("  PID file: %s\n", pidFile)
	fmt.Printf("  State file: %s\n", stateFile)
	fmt.Printf("  Log directory: %s\n", logDir)
	if config.Daemon.RestartPolicy.MaxRetries != nil {
		fmt.Printf("  Max retries: %d\n", *config.Daemon.RestartPolicy.MaxRetries)
	} else {
		fmt.Printf("  Max retries: 3 (default)\n")
	}
	if config.Daemon.RestartPolicy.BackoffInitial != nil {
		fmt.Printf("  Backoff initial: %ds\n", *config.Daemon.RestartPolicy.BackoffInitial)
	} else {
		fmt.Printf("  Backoff initial: 2s (default)\n")
	}
	if config.Daemon.RestartPolicy.BackoffMax != nil {
		fmt.Printf("  Backoff max: %ds\n", *config.Daemon.RestartPolicy.BackoffMax)
	} else {
		fmt.Printf("  Backoff max: 300s (default)\n")
	}
	if config.Daemon.MaxAgents != nil {
		fmt.Printf("  Max agents: %d\n", *config.Daemon.MaxAgents)
	} else {
		fmt.Printf("  Max agents: 20 (default)\n")
	}
	fmt.Println("")
	fmt.Println("Agents to supervise:")
	for _, a := range config.Agents {
		fmt.Printf("  - %s (role: %s, auto: %v)\n", a.Worktree, a.Role, a.Auto)
	}
	fmt.Println("")
	fmt.Println("Recommended systemd resource controls:")
	fmt.Println("  LimitNOFILE=65536      # file descriptor limit")
	fmt.Println("  MemoryMax=4G           # memory ceiling")
	fmt.Println("  CPUQuota=200%          # CPU limit (200% = 2 cores)")
	fmt.Println("  TasksMax=256           # max tasks (processes+threads)")
}
