package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	webuilog "github.com/tysonthomas9/loomcli/internal/webui/log"
)

var daemonLogsLines int
var daemonLogsFollow bool

var daemonLogsCmd = &cobra.Command{
	Use:   "logs [agent-name]",
	Short: "View agent logs",
	Long: `View agent logs without knowing the file path.

Examples:
  loom daemon logs              List available agents and their log paths
  loom daemon logs falcon       Show last 50 lines of falcon's log
  loom daemon logs falcon -n 100  Show last 100 lines
  loom daemon logs falcon -f    Follow/stream the log (like tail -f)`,
	Run: runDaemonLogs,
}

func init() {
	daemonLogsCmd.Flags().IntVarP(&daemonLogsLines, "lines", "n", 50, "Number of lines to show")
	daemonLogsCmd.Flags().BoolVarP(&daemonLogsFollow, "follow", "f", false, "Follow log output (like tail -f)")
}

func runDaemonLogs(cmd *cobra.Command, args []string) {
	projectDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine working directory: %v\n", err)
		exitProcess(1)
	}

	config := loadDaemonLogsConfig(projectDir)
	stateFilePath := cfgpkg.ResolveDaemonStatePath(projectDir)
	state, stateErr := ReadStateFile(stateFilePath)

	if len(args) == 0 {
		listAgentLogs(projectDir, config, state, stateErr)
		return
	}

	agent := findAgent(args[0], state, stateErr)
	logPath := resolveAgentLogPath(projectDir, config, agent.Role, agent.Worktree)
	showAgentLog(logPath)
}

// loadDaemonLogsConfig loads daemon config with a fallback to defaults.
func loadDaemonLogsConfig(projectDir string) *cfgpkg.DaemonConfig {
	config, err := cfgpkg.LoadDaemonConfig(projectDir)
	if err != nil {
		return &cfgpkg.DaemonConfig{
			Daemon: cfgpkg.DaemonSettings{
				PIDFile: ".loom/daemon.pid",
				LogDir:  ".loom/logs",
			},
		}
	}
	return config
}

// listAgentLogs prints all available agents and their log paths.
func listAgentLogs(projectDir string, config *cfgpkg.DaemonConfig, state *DaemonState, stateErr error) {
	if stateErr != nil || state == nil || len(state.Agents) == 0 {
		fmt.Fprintf(os.Stderr, "No agent state found. Is the daemon running or has it been run before?\n")
		exitProcess(1)
	}
	fmt.Println("Available agents:")
	for _, agent := range state.Agents {
		logPath := resolveAgentLogPath(projectDir, config, agent.Role, agent.Worktree)
		exists := "missing"
		if _, err := os.Stat(logPath); err == nil {
			exists = "exists"
		}
		fmt.Printf("  %-15s %s (%s)\n", agent.Worktree, logPath, exists)
	}
	fmt.Println("\nUse 'loom daemon logs <agent-name>' to view a specific agent's log.")
}

// findAgent looks up an agent by worktree name in the state file, exiting on failure.
func findAgent(name string, state *DaemonState, stateErr error) *DaemonAgentStatus {
	if stateErr == nil && state != nil {
		for i := range state.Agents {
			if state.Agents[i].Worktree == name {
				return &state.Agents[i]
			}
		}
	}

	fmt.Fprintf(os.Stderr, "Unknown agent: %s\n", name)
	if stateErr == nil && state != nil && len(state.Agents) > 0 {
		fmt.Fprintf(os.Stderr, "Available agents:")
		for _, agent := range state.Agents {
			fmt.Fprintf(os.Stderr, " %s", agent.Worktree)
		}
		fmt.Fprintf(os.Stderr, "\n")
	} else {
		fmt.Fprintf(os.Stderr, "No agent state found. Is the daemon running or has it been run before?\n")
	}
	exitProcess(1)
	return nil // unreachable
}

// showAgentLog reads and displays the agent's log file, optionally following it.
func showAgentLog(logPath string) {
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Log file not found: %s\n", logPath)
		fmt.Fprintf(os.Stderr, "The agent may not have been started yet.\n")
		exitProcess(1)
	}

	lines, _, err := webuilog.ReadLastNLines(logPath, daemonLogsLines)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading log: %v\n", err)
		exitProcess(1)
	}

	if len(lines) == 0 {
		fmt.Println("(empty log file)")
		if !daemonLogsFollow {
			return
		}
	}

	for _, line := range lines {
		fmt.Println(line)
	}

	if daemonLogsFollow {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if err := followLogFile(ctx, logPath); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "Error following log: %v\n", err)
			exitProcess(1)
		}
	}
}

// resolveAgentLogPath reconstructs the log file path using the same convention
// as daemon_spawn.go:spawnAgent.
func resolveAgentLogPath(projectDir string, config *cfgpkg.DaemonConfig, role, worktree string) string {
	logDir := config.Daemon.LogDir
	if logDir == "" {
		logDir = ".loom/logs"
	}
	if !filepath.IsAbs(logDir) {
		logDir = filepath.Join(projectDir, logDir)
	}

	// Namespace by workspace ID (same as daemon_spawn.go)
	ws, err := cfgpkg.ResolveActiveWorkspace()
	if err == nil && ws != nil && ws.ID != "" {
		logDir = filepath.Join(logDir, ws.ID)
	}

	safeWorktree := filepath.Base(worktree)
	safeRole := filepath.Base(role)
	if safeRole != role {
		slog.Warn("role name sanitized for log path lookup", "raw", role, "safe", safeRole)
	}
	return filepath.Join(logDir, fmt.Sprintf("%s-%s.log", safeRole, safeWorktree))
}

// followLogFile watches a log file for changes and writes new bytes to stdout.
// It blocks until ctx is cancelled.
func followLogFile(ctx context.Context, path string) error {
	f, err := os.Open(path) //nolint:gosec // path constructed from known daemon paths
	if err != nil {
		return err
	}
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()

	offset, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	dir := filepath.Dir(path)
	if err := watcher.Add(dir); err != nil {
		return fmt.Errorf("watching directory: %w", err)
	}

	buf := make([]byte, 32*1024)
	baseName := filepath.Base(path)

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			f, offset = handleLogEvent(f, event, path, baseName, buf, offset)
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			return fmt.Errorf("watcher error: %w", err)
		}
	}
}

// handleLogEvent processes a single fsnotify event, reopening the file on Create
// and reading new bytes on Write. Returns the updated file handle and offset.
func handleLogEvent(f *os.File, event fsnotify.Event, path, baseName string, buf []byte, offset int64) (*os.File, int64) {
	if filepath.Base(event.Name) != baseName {
		return f, offset
	}
	if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
		return f, offset
	}

	// If file was recreated, reopen it
	if event.Op&fsnotify.Create != 0 {
		if f != nil {
			_ = f.Close()
		}
		newF, err := os.Open(path) //nolint:gosec // path constructed from known daemon paths
		if err != nil {
			return nil, 0
		}
		f = newF
		offset = 0
	}

	if f == nil {
		return f, offset
	}

	// Read new bytes from the current offset
	for {
		n, readErr := f.ReadAt(buf, offset)
		if n > 0 {
			_, _ = os.Stdout.Write(buf[:n])
			offset += int64(n)
		}
		if readErr != nil {
			break
		}
	}
	return f, offset
}
