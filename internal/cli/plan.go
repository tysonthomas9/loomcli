package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	planAutoMode    bool
	planDaemonMode  bool // Hidden: for internal tmux session use
	planInterval    int
	planMaxTasks    int
	planIdleTimeout int
)

var planCmd = &cobra.Command{
	Use:               "plan [worktree]",
	Short:             "Run a Claude planning agent",
	GroupID:           "agents",
	ValidArgsFunction: worktreeCompletion,
	Long: `Run a Claude planning agent in the specified worktree.

The planning agent will:
  1. Pick the highest priority task (skipping [Need Review] tasks)
  2. Research the codebase and create a detailed plan
  3. Save the plan to the task's --design field
  4. Mark the task as [Need Review] for human approval
  5. Exit after completing ONE task (unless --auto is enabled)

Arguments:
  worktree    Worktree name (e.g., falcon) or path
              If omitted, runs in current directory

Flags:
  -a, --auto          Enable continuous mode (process multiple tasks)
  -i, --interval      Polling interval in seconds when no tasks (default: 30)
  -m, --max-tasks     Maximum tasks to process before exiting (0 = unlimited)
  -t, --idle-timeout  Exit after N minutes with no available tasks (0 = none)

Examples:
  loom plan falcon              # Run in falcon worktree (single task)
  loom plan                     # Run in current directory
  loom plan falcon --auto       # Continuous mode until Ctrl+C
  loom plan falcon -a -m 5      # Process up to 5 tasks
  loom plan falcon -a -t 30     # Exit after 30 min idle`,
	Args: cobra.MaximumNArgs(1),
	Run:  runPlan,
}

func init() {
	planCmd.Flags().BoolVarP(&planAutoMode, "auto", "a", false, "Enable continuous mode (process multiple tasks)")
	planCmd.Flags().BoolVar(&planDaemonMode, "daemon-mode", false, "Internal: single task mode for daemon")
	_ = planCmd.Flags().MarkHidden("daemon-mode")
	planCmd.Flags().IntVarP(&planInterval, "interval", "i", 30, "Polling interval in seconds when no tasks available")
	planCmd.Flags().IntVarP(&planMaxTasks, "max-tasks", "m", 0, "Maximum tasks to process (0 = unlimited)")
	planCmd.Flags().IntVarP(&planIdleTimeout, "idle-timeout", "t", 0, "Exit after N minutes with no tasks (0 = none)")
	rootCmd.AddCommand(planCmd)
}

func runPlan(cmd *cobra.Command, args []string) {
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
	if planDaemonMode {
		if err := AcquireLock(worktreePath, "plan", agentName); err != nil {
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
		prompt := GeneratePlanningPrompt(agentName, workspace)
		if err := InvokeClaude(worktreePath, prompt, agentName); err != nil { // Interactive mode, nice output
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		// Note: No StateIdle here - daemon exits immediately, lock left for parent to read
		return
	}

	// AUTO MODE with tmux - daemon manages lock, not parent
	if planAutoMode && IsTmuxAvailable() {
		shutdown := SetupSignalHandler()
		RunAutoModeTmux(AutoModeOptions{
			Interval:     planInterval,
			MaxTasks:     planMaxTasks,
			IdleTimeout:  planIdleTimeout,
			AgentType:    "plan",
			AgentName:    agentName,
			WorktreePath: worktreePath,
		}, shutdown)
		return
	}

	// AUTO MODE without tmux OR single task mode - parent manages lock
	if err := AcquireLock(worktreePath, "plan", agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = ReleaseLock(worktreePath) }()

	if planAutoMode {
		// Fallback to JSON streaming mode (no tmux)
		fmt.Println("[auto] tmux not found, using JSON streaming mode")
		shutdown := SetupSignalHandler()
		RunAutoModeLoop(AutoModeOptions{
			Interval:     planInterval,
			MaxTasks:     planMaxTasks,
			IdleTimeout:  planIdleTimeout,
			AgentType:    "plan",
			AgentName:    agentName,
			WorktreePath: worktreePath,
		}, shutdown)
		return
	}

	// SINGLE TASK MODE (original behavior)
	// Check if there are tasks available for planning
	available, err := HasAvailablePlanningTasks()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking tasks: %v\n", err)
		os.Exit(1)
	}
	if !available {
		fmt.Println("No tasks available for planning.")
		fmt.Println("Tasks must be: open status, no design, not [Need Review], not epics")
		return
	}

	fmt.Println("=========================================")
	fmt.Printf("Running PLANNING agent in: %s\n", worktreePath)
	fmt.Printf("Agent name: %s\n", agentName)
	fmt.Println("=========================================")
	fmt.Println("")

	// Generate and run the planning prompt
	workspace, _ := ResolveActiveWorkspace()
	prompt := GeneratePlanningPrompt(agentName, workspace)
	if err := InvokeClaude(worktreePath, prompt, agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error running Claude: %v\n", err)
		os.Exit(1)
	}
}
