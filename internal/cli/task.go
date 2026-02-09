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
	taskParentID    string
)

var taskCmd = &cobra.Command{
	Use:               "task [worktree|workspace]",
	Short:             "Run an implementation agent",
	GroupID:           "agents",
	ValidArgsFunction: worktreeCompletion,
	Long: `Run an implementation agent in the specified worktree or workspace.

The implementation agent will:
  1. Pick the highest priority ready task (skipping tasks needing revision)
  2. Follow the --design plan if present, otherwise create a local plan
  3. Implement, test, and review the code
  4. Commit and push changes
  5. Close the task and exit after completing ONE task (unless --auto is enabled)

Arguments:
  worktree    Worktree/workspace name (e.g., falcon) or path
              In workspace mode, workspace names take priority over repo names.
              If omitted, runs in current directory (or workspace root in workspace mode).

Flags:
  -a, --auto          Enable continuous mode (process multiple tasks)
  -i, --interval      Polling interval in seconds when no tasks (default: 30)
  -m, --max-tasks     Maximum tasks to process before exiting (0 = unlimited)
  -t, --idle-timeout  Exit after N minutes with no available tasks (0 = none)

Examples:
  loom task falcon              # Run in falcon worktree/workspace (single task)
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
	_ = taskCmd.Flags().MarkHidden("daemon-mode")
	taskCmd.Flags().IntVarP(&taskInterval, "interval", "i", 30, "Polling interval in seconds when no tasks available")
	taskCmd.Flags().IntVarP(&taskMaxTasks, "max-tasks", "m", 0, "Maximum tasks to process (0 = unlimited)")
	taskCmd.Flags().IntVarP(&taskIdleTimeout, "idle-timeout", "t", 0, "Exit after N minutes with no tasks (0 = none)")
	taskCmd.Flags().StringVar(&taskParentID, "parent", "", "Filter tasks to descendants of this epic ID")
	rootCmd.AddCommand(taskCmd)
}

func runTask(cmd *cobra.Command, args []string) {
	// Resolve worktree/workspace path
	var argName string
	if len(args) > 0 {
		argName = args[0]
	}

	target, err := ResolveAgentTarget(argName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	worktreePath := target.WorkDir
	agentName := target.AgentName

	// DAEMON MODE: Called by tmux session, run single task
	// Daemon manages its own lock (parent doesn't hold lock in tmux mode)
	if taskDaemonMode {
		if err := AcquireLock(worktreePath, "task", agentName); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		// Lock intentionally NOT released here. Parent (RunAutoModeTmux)
		// reads the lock after daemon exit to detect task claims, then
		// removes it before the next cycle.

		if err := UpdateLockState(worktreePath, StateActive); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not update lock state: %v\n", err)
		}
		workspace, _ := ResolveActiveWorkspace()
		prompt := GenerateTaskPrompt(agentName, workspace)
		if err := InvokeAgent(worktreePath, prompt, agentName); err != nil { // Interactive mode, nice output
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		// Note: No StateIdle here - daemon exits immediately, lock left for parent to read
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
			ParentID:     taskParentID,
		}, shutdown)
		return
	}

	// AUTO MODE without tmux OR single task mode - parent manages lock
	if err := AcquireLock(worktreePath, "task", agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = ReleaseLock(worktreePath) }()

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
			ParentID:     taskParentID,
		}, shutdown)
		return
	}

	// SINGLE TASK MODE (original behavior)
	// Check if there are tasks available for implementation
	available, err := HasAvailableImplementationTasks(taskParentID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking tasks: %v\n", err)
		os.Exit(1)
	}
	if !available {
		fmt.Println("No tasks available for implementation.")
		fmt.Println("Tasks must be: open status, has design, no needs-revision label, not epics")
		return
	}

	fmt.Println("=========================================")
	fmt.Printf("Running IMPLEMENTATION agent in: %s\n", worktreePath)
	fmt.Printf("Agent name: %s\n", agentName)
	fmt.Println("=========================================")
	fmt.Println("")

	if err := UpdateLockState(worktreePath, StateActive); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update lock state: %v\n", err)
	}

	// Generate and run the task prompt
	workspace, _ := ResolveActiveWorkspace()
	prompt := GenerateTaskPrompt(agentName, workspace)
	if err := InvokeAgent(worktreePath, prompt, agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error running agent: %v\n", err)
		os.Exit(1)
	}
}
