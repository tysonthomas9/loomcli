package agent

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/workspace"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

var (
	taskAutoMode    bool
	taskDaemonMode  bool // Hidden: for internal tmux session use
	taskInterval    int
	taskMaxTasks    int
	taskIdleTimeout int
	taskParentID    string
	taskOutput      string
)

var taskCmd = &cobra.Command{
	Use:     "task",
	Short:   "Manage task workflow and run implementation agents",
	GroupID: "agents",
	Long: `Manage task workflow state and run implementation agents.

Workflow commands express user intent:
  loom task claim <id>
  loom task submit <id>
  loom task block <id> --reason <text>
  loom task release <id>
  loom task close <id>

The implementation-agent runner is explicit:
  loom task run [worktree|workspace]`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var taskRunCmd = &cobra.Command{
	Use:               "run [worktree|workspace]",
	Short:             "Run an implementation agent",
	ValidArgsFunction: cli.WorktreeCompletion,
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
  loom task run falcon              # Run in falcon worktree/workspace (single task)
  loom task run                     # Run in current directory
  loom task run falcon --auto       # Continuous mode until Ctrl+C
  loom task run falcon -a -m 5      # Process up to 5 tasks
  loom task run falcon -a -t 30     # Exit after 30 min idle`,
	Args: cobra.MaximumNArgs(1),
	Run:  runTask,
}

func init() {
	taskCmd.PersistentFlags().StringVarP(&taskOutput, "output", "o", "text", "Output format for workflow commands: text|json")

	taskRunCmd.Flags().BoolVarP(&taskAutoMode, "auto", "a", false, "Enable continuous mode (process multiple tasks)")
	taskRunCmd.Flags().BoolVar(&taskDaemonMode, "daemon-mode", false, "Internal: single task mode for daemon")
	_ = taskRunCmd.Flags().MarkHidden("daemon-mode")
	taskRunCmd.Flags().IntVarP(&taskInterval, "interval", "i", 30, "Polling interval in seconds when no tasks available")
	taskRunCmd.Flags().IntVarP(&taskMaxTasks, "max-tasks", "m", 0, "Maximum tasks to process (0 = unlimited)")
	taskRunCmd.Flags().IntVarP(&taskIdleTimeout, "idle-timeout", "t", 0, "Exit after N minutes with no tasks (0 = none)")
	taskRunCmd.Flags().StringVar(&taskParentID, "parent", "", "Filter tasks to descendants of this epic ID")

	taskCmd.AddCommand(
		taskRunCmd,
		taskClaimCmd,
		taskSubmitCmd,
		taskBlockCmd,
		taskReleaseCmd,
		taskCloseCmd,
		taskReopenCmd,
		taskDeferCmd,
		taskUndeferCmd,
		taskShowCmd,
		taskListCmd,
	)
	cli.RegisterCommand(taskCmd)
}

func runTask(cmd *cobra.Command, args []string) {
	deps := cli.GetDeps(cmd)

	var argName string
	if len(args) > 0 {
		argName = args[0]
	}

	target, err := workspace.ResolveAgentTarget(argName, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		cli.ExitWithFlush(1)
	}

	worktreePath := target.WorkDir
	agentName := target.AgentName

	if taskDaemonMode {
		runTaskDaemon(deps, worktreePath, agentName)
		return
	}

	routerCheck := cli.RouterTaskCheckFromEnv(taskParentID)

	if taskAutoMode && automode.IsTmuxAvailable() {
		shutdown := automode.SetupSignalHandler()
		automode.RunAutoModeTmux(automode.AutoModeOptions{
			Interval: taskInterval, MaxTasks: taskMaxTasks, IdleTimeout: taskIdleTimeout,
			AgentType: "task", AgentName: agentName, WorktreePath: worktreePath,
			ParentID: taskParentID, CustomTaskCheck: routerCheck,
		}, shutdown)
		return
	}

	if err := cli.AcquireLock(worktreePath, "task", agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		cli.ExitWithFlush(1)
	}
	defer func() { _ = cli.ReleaseLock(worktreePath) }()

	if taskAutoMode {
		runTaskAutoFallback(deps, worktreePath, agentName, routerCheck)
		return
	}

	runTaskSingleTask(deps, worktreePath, agentName, routerCheck)
}

// runTaskDaemon handles daemon mode for the task agent.
func runTaskDaemon(deps *cli.Deps, worktreePath, agentName string) {
	if err := cli.AcquireLock(worktreePath, "task", agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		cli.ExitWithFlush(1)
	}
	if err := cli.UpdateLockState(worktreePath, cli.StateActive); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update lock state: %v\n", err)
	}

	ws, _ := config.ResolveActiveWorkspace()
	prompt := GenerateTaskPrompt(agentName, ws, taskParentID, cli.GetBackendName())
	assignedTaskID := os.Getenv("LOOM_ASSIGNED_TASK_ID")
	if assignedTaskID != "" {
		prompt = GenerateFleetTaskPrompt(agentName, assignedTaskID, ws, cli.GetBackendName())
	}
	sess := adoptOrCreateSession(agentName, taskParentID, prompt, "implementation")

	emitTaskClaimedFromEnv(agentName, assignedTaskID)

	beforeRef := automode.CaptureHEADRef(worktreePath)
	startedAt := time.Now()

	// Daemon-spawned subprocesses have no controlling TTY. InvokeInteractive
	// inherits the daemon's stdin/stdout, which makes backend TUIs render
	// nothing — the supervisor watchdog then times the silent run out at
	// 15 min (see runTaskDaemon/runPlanDaemon notes). Use the wrapper-backed
	// non-interactive path: PTY + stream-json → log mtime advances per turn.
	shutdown := automode.SetupSignalHandler()
	collector := usage.NewCollector(cli.GetBackendName(), agentName)
	invokeErr := deps.Agent.InvokeNonInteractive(worktreePath, prompt, agentName, shutdown, collector)

	emitTaskLifecycleResult(agentName, worktreePath, startedAt, invokeErr)
	finalizeAgentSession(sess, worktreePath, beforeRef, invokeErr, collector, startedAt, taskParentID)

	if invokeErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", invokeErr)
		cli.ExitWithFlush(1)
	}
}

// runTaskAutoFallback handles auto mode without tmux for the task agent.
func runTaskAutoFallback(deps *cli.Deps, worktreePath, agentName string, routerCheck func() (bool, error)) {
	fmt.Println("[auto] tmux not found, using JSON streaming mode")
	shutdown := automode.SetupSignalHandler()
	backendName := cli.GetBackendName()
	promptGen := func(name string, ws *config.WorkspaceConfig) string {
		return GenerateTaskPrompt(name, ws, taskParentID, backendName)
	}
	automode.RunAutoModeLoop(automode.AutoModeOptions{
		Interval: taskInterval, MaxTasks: taskMaxTasks, IdleTimeout: taskIdleTimeout,
		AgentType: "task", AgentName: agentName, WorktreePath: worktreePath,
		ParentID: taskParentID, CustomTaskCheck: routerCheck,
		CustomPromptGen: promptGen, Deps: deps,
	}, shutdown)
}

// runTaskSingleTask runs a single implementation task.
func runTaskSingleTask(deps *cli.Deps, worktreePath, agentName string, routerCheck func() (bool, error)) {
	available, err := cli.CheckTaskAvailability(routerCheck, func() (bool, error) {
		return automode.HasAvailableImplementationTasks(taskParentID, os.Getenv("LOOM_AGENT_REPO"))
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking tasks: %v\n", err)
		cli.ExitWithFlush(1)
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

	if err := cli.UpdateLockState(worktreePath, cli.StateActive); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update lock state: %v\n", err)
	}

	ws, _ := config.ResolveActiveWorkspace()
	prompt := GenerateTaskPrompt(agentName, ws, taskParentID, cli.GetBackendName())
	sess := createAgentSession(agentName, taskParentID, prompt, "implementation")

	// Single-task mode: the task is self-claimed by the agent during the
	// run, so the ID is unknown here. Emit with TaskID="" to start the
	// loom.task span; emitTaskLifecycleResult reads the lock file after
	// invoke to recover the resolved ID for the close-out event.
	emitTaskClaimedFromEnv(agentName, "")

	beforeRef := automode.CaptureHEADRef(worktreePath)
	startedAt := time.Now()
	invokeErr := deps.Agent.InvokeInteractive(worktreePath, prompt, agentName)
	emitTaskLifecycleResult(agentName, worktreePath, startedAt, invokeErr)
	finalizeAgentSession(sess, worktreePath, beforeRef, invokeErr, nil, startedAt, taskParentID)

	if invokeErr != nil {
		fmt.Fprintf(os.Stderr, "Error running agent: %v\n", invokeErr)
		cli.ExitWithFlush(1)
	}
}
