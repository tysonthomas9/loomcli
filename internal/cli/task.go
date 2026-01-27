package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	taskAutoMode    bool
	taskDaemonMode  bool // Hidden: for internal tmux session use
	taskInterval    int
	taskMaxTasks    int
	taskIdleTimeout int
)

var taskCmd = &cobra.Command{
	Use:               "task [worktree]",
	Short:             "Run a Claude implementation agent",
	GroupID:           "agents",
	ValidArgsFunction: worktreeCompletion,
	Long: `Run a Claude implementation agent in the specified worktree.

The implementation agent will:
  1. Pick the highest priority ready task (skipping [Need Review] tasks)
  2. Follow the --design plan if present, otherwise create a local plan
  3. Implement, test, and review the code
  4. Commit and push changes
  5. Close the task and exit after completing ONE task (unless --auto is enabled)

Arguments:
  worktree    Worktree name (e.g., falcon) or path
              If omitted, runs in current directory

Flags:
  -a, --auto          Enable continuous mode (process multiple tasks)
  -i, --interval      Polling interval in seconds when no tasks (default: 30)
  -m, --max-tasks     Maximum tasks to process before exiting (0 = unlimited)
  -t, --idle-timeout  Exit after N minutes with no available tasks (0 = none)

Examples:
  loom task falcon              # Run in falcon worktree (single task)
  loom task                     # Run in current directory
  loom task falcon --auto       # Continuous mode until Ctrl+C
  loom task falcon -a -m 5      # Process up to 5 tasks
  loom task falcon -a -t 30     # Exit after 30 min idle`,
	Args: cobra.MaximumNArgs(1),
	Run:  runTask,
}

func init() {
	taskCmd.Flags().BoolVarP(&taskAutoMode, "auto", "a", false, "Enable continuous mode (process multiple tasks)")
	taskCmd.Flags().BoolVar(&taskDaemonMode, "daemon-mode", false, "Internal: single task mode for daemon")
	taskCmd.Flags().MarkHidden("daemon-mode")
	taskCmd.Flags().IntVarP(&taskInterval, "interval", "i", 30, "Polling interval in seconds when no tasks available")
	taskCmd.Flags().IntVarP(&taskMaxTasks, "max-tasks", "m", 0, "Maximum tasks to process (0 = unlimited)")
	taskCmd.Flags().IntVarP(&taskIdleTimeout, "idle-timeout", "t", 0, "Exit after N minutes with no tasks (0 = none)")
	rootCmd.AddCommand(taskCmd)
}

func runTask(cmd *cobra.Command, args []string) {
	// Resolve worktree path
	var worktreeName string
	if len(args) > 0 {
		worktreeName = args[0]
	}

	worktreePath, err := ResolveWorktreePath(worktreeName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	agentName := GetWorktreeName(worktreePath)

	// DAEMON MODE: Called by tmux session, run single task
	// Daemon manages its own lock (parent doesn't hold lock in tmux mode)
	if taskDaemonMode {
		if err := AcquireLock(worktreePath, "task", agentName); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer ReleaseLock(worktreePath)

		UpdateLockState(worktreePath, StateActive)
		prompt := GenerateTaskPrompt(agentName)
		InvokeClaude(worktreePath, prompt) // Interactive mode, nice output
		// Note: No StateIdle here - daemon exits immediately, lock released by defer
		return
	}

	// AUTO MODE with tmux - daemon manages lock, not parent
	if taskAutoMode && IsTmuxAvailable() {
		shutdown := SetupSignalHandler()
		RunAutoModeTmux(AutoModeOptions{
			Interval:     taskInterval,
			MaxTasks:     taskMaxTasks,
			IdleTimeout:  taskIdleTimeout,
			AgentType:    "task",
			AgentName:    agentName,
			WorktreePath: worktreePath,
		}, shutdown)
		return
	}

	// AUTO MODE without tmux OR single task mode - parent manages lock
	if err := AcquireLock(worktreePath, "task", agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer ReleaseLock(worktreePath)

	if taskAutoMode {
		// Fallback to JSON streaming mode (no tmux)
		fmt.Println("[auto] tmux not found, using JSON streaming mode")
		shutdown := SetupSignalHandler()
		RunAutoModeLoop(AutoModeOptions{
			Interval:     taskInterval,
			MaxTasks:     taskMaxTasks,
			IdleTimeout:  taskIdleTimeout,
			AgentType:    "task",
			AgentName:    agentName,
			WorktreePath: worktreePath,
		}, shutdown)
		return
	}

	// SINGLE TASK MODE (original behavior)
	fmt.Println("=========================================")
	fmt.Printf("Running IMPLEMENTATION agent in: %s\n", worktreePath)
	fmt.Printf("Agent name: %s\n", agentName)
	fmt.Println("=========================================")
	fmt.Println("")

	// Generate and run the task prompt
	prompt := GenerateTaskPrompt(agentName)
	if err := InvokeClaude(worktreePath, prompt); err != nil {
		fmt.Fprintf(os.Stderr, "Error running Claude: %v\n", err)
		os.Exit(1)
	}
}
