package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	planAutoMode    bool
	planDaemonMode  bool // Hidden: for internal tmux session use
	planSandboxMode bool
	planInterval    int
	planMaxTasks    int
	planIdleTimeout int
	planParentID    string
)

var planCmd = &cobra.Command{
	Use:               "plan [worktree|workspace]",
	Short:             "Run a planning agent",
	GroupID:           "agents",
	ValidArgsFunction: worktreeCompletion,
	Long: `Run a planning agent in the specified worktree or workspace.

The planning agent will:
  1. Pick the highest priority task (or one with needs-revision label)
  2. Research the codebase and create a detailed plan
  3. Save the plan to the task's --design field
  4. Set status to 'review' for human approval
  5. Exit after completing ONE task (unless --auto is enabled)

Arguments:
  worktree    Worktree/workspace name (e.g., falcon) or path
              In workspace mode, workspace names take priority over repo names.
              If omitted, runs in current directory (or workspace root in workspace mode).

Flags:
  -a, --auto          Enable continuous mode (process multiple tasks)
  -i, --interval      Polling interval in seconds when no tasks (default: 30)
  -m, --max-tasks     Maximum tasks to process before exiting (0 = unlimited)
  -t, --idle-timeout  Exit after N minutes with no available tasks (0 = none)
      --sandbox       Run the agent inside an OpenShell sandbox

Examples:
  loom plan falcon              # Run in falcon worktree/workspace (single task)
  loom plan                     # Run in current directory
  loom plan falcon --auto       # Continuous mode until Ctrl+C
  loom plan falcon -a -m 5      # Process up to 5 tasks
  loom plan falcon -a -t 30     # Exit after 30 min idle
  loom plan falcon --sandbox    # Run in an OpenShell sandbox`,
	Args: cobra.MaximumNArgs(1),
	Run:  runPlan,
}

func init() {
	planCmd.Flags().BoolVarP(&planAutoMode, "auto", "a", false, "Enable continuous mode (process multiple tasks)")
	planCmd.Flags().BoolVar(&planDaemonMode, "daemon-mode", false, "Internal: single task mode for daemon")
	_ = planCmd.Flags().MarkHidden("daemon-mode")
	planCmd.Flags().BoolVar(&planSandboxMode, "sandbox", false, "Run the agent inside an OpenShell sandbox")
	planCmd.Flags().IntVarP(&planInterval, "interval", "i", 30, "Polling interval in seconds when no tasks available")
	planCmd.Flags().IntVarP(&planMaxTasks, "max-tasks", "m", 0, "Maximum tasks to process (0 = unlimited)")
	planCmd.Flags().IntVarP(&planIdleTimeout, "idle-timeout", "t", 0, "Exit after N minutes with no tasks (0 = none)")
	planCmd.Flags().StringVar(&planParentID, "parent", "", "Filter tasks to descendants of this epic ID")
	rootCmd.AddCommand(planCmd)
}

func runPlan(cmd *cobra.Command, args []string) {
	var argName string
	if len(args) > 0 {
		argName = args[0]
	}
	target, err := ResolveAgentTarget(argName, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	worktreePath, agentName := target.WorkDir, target.AgentName

	if planSandboxMode {
		handleSandboxMode("plan", agentName, worktreePath, planParentID, planAutoMode)
		return
	}

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
		prompt := GeneratePlanningPrompt(agentName, workspace, planParentID)
		if err := InvokeAgent(worktreePath, prompt, agentName); err != nil { // Interactive mode, nice output
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		// Note: No StateIdle here - daemon exits immediately, lock left for parent to read
		return
	}

	// Build router-based task check from daemon env vars (nil when no routing env vars set)
	routerCheck := RouterTaskCheckFromEnv(planParentID)

	// AUTO MODE with tmux - daemon manages lock, not parent
	if planAutoMode && IsTmuxAvailable() {
		shutdown := SetupSignalHandler()
		RunAutoModeTmux(AutoModeOptions{
			Interval:        planInterval,
			MaxTasks:        planMaxTasks,
			IdleTimeout:     planIdleTimeout,
			AgentType:       "plan",
			AgentName:       agentName,
			WorktreePath:    worktreePath,
			ParentID:        planParentID,
			CustomTaskCheck: routerCheck,
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
			Interval:        planInterval,
			MaxTasks:        planMaxTasks,
			IdleTimeout:     planIdleTimeout,
			AgentType:       "plan",
			AgentName:       agentName,
			WorktreePath:    worktreePath,
			ParentID:        planParentID,
			CustomTaskCheck: routerCheck,
		}, shutdown)
		return
	}

	// SINGLE TASK MODE - check if there are tasks available for planning
	available, err := checkTaskAvailability(routerCheck, func() (bool, error) { return HasAvailablePlanningTasks(planParentID, os.Getenv("LOOM_AGENT_REPO")) })
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking tasks: %v\n", err)
		os.Exit(1)
	}
	if !available {
		fmt.Println("No tasks available for planning.")
		fmt.Println("Tasks must be: open status, no design (or has needs-revision label), not epics")
		return
	}

	fmt.Println("=========================================")
	fmt.Printf("Running PLANNING agent in: %s\n", worktreePath)
	fmt.Printf("Agent name: %s\n", agentName)
	fmt.Println("=========================================")
	fmt.Println("")

	if err := UpdateLockState(worktreePath, StateActive); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update lock state: %v\n", err)
	}

	// Generate and run the planning prompt
	workspace, _ := ResolveActiveWorkspace()
	prompt := GeneratePlanningPrompt(agentName, workspace, planParentID)
	if err := InvokeAgent(worktreePath, prompt, agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error running agent: %v\n", err)
		os.Exit(1)
	}
}
