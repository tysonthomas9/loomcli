package agent

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/sessionfinalize"
	"github.com/tysonthomas9/loomcli/internal/cli/workspace"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

var (
	planAutoMode    bool
	planDaemonMode  bool // Hidden: for internal tmux session use
	planInterval    int
	planMaxTasks    int
	planIdleTimeout int
	planParentID    string
)

var planCmd = &cobra.Command{
	Use:               "plan [worktree|workspace]",
	Short:             "Run a planning agent",
	GroupID:           "agents",
	ValidArgsFunction: cli.WorktreeCompletion,
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

Examples:
  loom plan falcon              # Run in falcon worktree/workspace (single task)
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
	planCmd.Flags().StringVar(&planParentID, "parent", "", "Filter tasks to descendants of this epic ID")
	cli.RegisterCommand(planCmd)
}

func runPlan(cmd *cobra.Command, args []string) {
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

	if planDaemonMode {
		runPlanDaemon(deps, worktreePath, agentName)
		return
	}

	routerCheck := cli.RouterTaskCheckFromEnv(planParentID)

	if planAutoMode && automode.IsTmuxAvailable() {
		shutdown := automode.SetupSignalHandler()
		automode.RunAutoModeTmux(automode.AutoModeOptions{
			Interval: planInterval, MaxTasks: planMaxTasks, IdleTimeout: planIdleTimeout,
			AgentType: "plan", AgentName: agentName, WorktreePath: worktreePath,
			ParentID: planParentID, CustomTaskCheck: routerCheck,
		}, shutdown)
		return
	}

	if err := cli.AcquireLock(worktreePath, "plan", agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		cli.ExitWithFlush(1)
	}
	defer func() { _ = cli.ReleaseLock(worktreePath) }()

	if planAutoMode {
		runPlanAutoFallback(deps, worktreePath, agentName, routerCheck)
		return
	}

	runPlanSingleTask(deps, worktreePath, agentName, routerCheck)
}

// runPlanDaemon handles daemon mode: acquire lock, invoke agent, finalize session.
func runPlanDaemon(deps *cli.Deps, worktreePath, agentName string) {
	if err := cli.AcquireLock(worktreePath, "plan", agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		cli.ExitWithFlush(1)
	}
	if err := cli.UpdateLockState(worktreePath, cli.StateActive); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update lock state: %v\n", err)
	}

	ws, _ := config.ResolveActiveWorkspace()
	prompt := GeneratePlanningPrompt(agentName, ws, planParentID)
	assignedTaskID := os.Getenv("LOOM_ASSIGNED_TASK_ID")
	if assignedTaskID != "" {
		prompt = GenerateFleetPlanningPrompt(agentName, assignedTaskID, ws)
	}
	sess := adoptOrCreateSession(agentName, planParentID, prompt, "planning")

	emitTaskClaimedFromEnv(agentName, assignedTaskID)

	beforeRef := automode.CaptureHEADRef(worktreePath)
	startedAt := time.Now()

	// Daemon-spawned subprocesses have no controlling TTY. InvokeInteractive
	// inherits the daemon's stdin/stdout, which makes backend TUIs render
	// nothing — the supervisor watchdog then times the silent run out at
	// 15 min. Use the wrapper-backed non-interactive path: PTY + stream-json
	// → log mtime advances per turn.
	shutdown := automode.SetupSignalHandler()
	collector := usage.NewCollector(cli.GetBackendName(), agentName)
	invokeErr := deps.Agent.InvokeNonInteractive(worktreePath, prompt, agentName, shutdown, collector)

	emitTaskLifecycleResult(agentName, worktreePath, startedAt, invokeErr)
	finalizeAgentSession(sess, worktreePath, beforeRef, invokeErr)

	if invokeErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", invokeErr)
		cli.ExitWithFlush(1)
	}
}

// runPlanAutoFallback handles auto mode without tmux.
func runPlanAutoFallback(deps *cli.Deps, worktreePath, agentName string, routerCheck func() (bool, error)) {
	fmt.Println("[auto] tmux not found, using JSON streaming mode")
	shutdown := automode.SetupSignalHandler()
	promptGen := func(name string, ws *config.WorkspaceConfig) string {
		return GeneratePlanningPrompt(name, ws, planParentID)
	}
	automode.RunAutoModeLoop(automode.AutoModeOptions{
		Interval: planInterval, MaxTasks: planMaxTasks, IdleTimeout: planIdleTimeout,
		AgentType: "plan", AgentName: agentName, WorktreePath: worktreePath,
		ParentID: planParentID, CustomTaskCheck: routerCheck,
		CustomPromptGen: promptGen, Deps: deps,
	}, shutdown)
}

// runPlanSingleTask runs a single planning task.
func runPlanSingleTask(deps *cli.Deps, worktreePath, agentName string, routerCheck func() (bool, error)) {
	available, err := cli.CheckTaskAvailability(routerCheck, func() (bool, error) {
		return automode.HasAvailablePlanningTasks(planParentID, os.Getenv("LOOM_AGENT_REPO"))
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking tasks: %v\n", err)
		cli.ExitWithFlush(1)
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

	if err := cli.UpdateLockState(worktreePath, cli.StateActive); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update lock state: %v\n", err)
	}

	ws, _ := config.ResolveActiveWorkspace()
	prompt := GeneratePlanningPrompt(agentName, ws, planParentID)
	sess := createAgentSession(agentName, planParentID, prompt, "planning")

	// Single-task mode: the task is self-claimed by the agent during the
	// run, so the ID is unknown here. Emit with TaskID="" to start the
	// loom.task span; emitTaskLifecycleResult reads the lock file after
	// invoke to recover the resolved ID for the close-out event.
	emitTaskClaimedFromEnv(agentName, "")

	beforeRef := automode.CaptureHEADRef(worktreePath)
	startedAt := time.Now()
	invokeErr := deps.Agent.InvokeInteractive(worktreePath, prompt, agentName)
	emitTaskLifecycleResult(agentName, worktreePath, startedAt, invokeErr)
	finalizeAgentSession(sess, worktreePath, beforeRef, invokeErr)

	if invokeErr != nil {
		fmt.Fprintf(os.Stderr, "Error running agent: %v\n", invokeErr)
		cli.ExitWithFlush(1)
	}
}

// adoptOrCreateSession either inherits a parent session or creates a new one.
func adoptOrCreateSession(agentName, parentID, prompt, phase string) *sessions.Session {
	if inheritedSID := os.Getenv("LOOM_SESSION_ID"); inheritedSID != "" {
		inheritedRuntimeDir := os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR")
		if inheritedRuntimeDir == "" {
			inheritedRuntimeDir = cli.GetWorkspaceRuntimeDir()
		}
		if prompt != "" {
			if sessStore, err := sessions.NewStore(inheritedRuntimeDir); err == nil {
				_ = sessStore.UpdatePrompt(inheritedSID, prompt)
			}
		}
		backends.SetActiveSessionRuntimeEnv(inheritedRuntimeDir, inheritedSID)
		return nil
	}
	return createAgentSession(agentName, parentID, prompt, phase)
}

// createAgentSession creates a new session for tracking.
func createAgentSession(agentName, parentID, prompt, phase string) *sessions.Session {
	sessStore, sessErr := sessions.NewStore(cli.GetWorkspaceRuntimeDir())
	if sessErr != nil {
		log.Printf("[agent] Warning: session store unavailable: %v", sessErr)
		return nil
	}
	sess, _ := sessStore.CreateSession(sessions.CreateOptions{
		AgentName: agentName, Backend: cli.ResolveBackendName(),
		EpicID: parentID, Prompt: prompt, Phase: phase,
	})
	if sess != nil {
		backends.SetActiveSessionRuntimeEnv(cli.GetWorkspaceRuntimeDir(), sess.SessionID())
	}
	return sess
}

// finalizeAgentSession finalizes a session after agent invocation.
func finalizeAgentSession(sess *sessions.Session, worktreePath, beforeRef string, invokeErr error) {
	if sess == nil {
		return
	}
	exitCode := 0
	if invokeErr != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(invokeErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	taskID := ""
	if info, lockErr := cli.ReadLockFile(worktreePath); lockErr == nil {
		taskID = info.TaskID
	}
	_, _ = sessionfinalize.WithWorktree(sess, sessionfinalize.WithWorktreeOptions{
		WorktreePath: worktreePath,
		BeforeRef:    beforeRef,
		TaskID:       taskID,
		ExitCode:     exitCode,
	})
	backends.ClearActiveSessionEnv()
}

// emitTaskClaimedFromEnv emits a TaskClaimed event before InvokeInteractive
// so a loom.task span starts under the active trace. Used by single-task and
// daemon-mode plan/task paths that bypass the auto-mode loop.
//
// In daemon mode the assigned task ID comes from LOOM_ASSIGNED_TASK_ID. In
// single-task mode the agent self-claims the task during the run, so the ID
// is unknown at this point and we emit with TaskID="" — otelexport accepts
// the empty string and the followup TaskCompleted/TaskFailed will carry the
// resolved ID read from the lock file.
//
// Best-effort: if AgentEventBus is unavailable (mkdir failure on first use)
// or NewEvent fails we skip silently. Per the trace contract §6 the prompt
// content is NOT placed on the event — only IDs and titles.
func emitTaskClaimedFromEnv(agentName, taskID string) {
	bus := cli.AgentEventBus()
	if bus == nil {
		return
	}
	evt, err := events.NewEvent(events.TaskClaimed, agentName, "", "", events.TaskClaimedData{TaskID: taskID})
	if err != nil {
		return
	}
	if emitErr := bus.Emit(evt); emitErr != nil {
		log.Printf("[agent] Failed to emit task_claimed event: %v", emitErr)
	}
}

// emitTaskLifecycleResult emits TaskCompleted on success or TaskFailed on
// error. Pairs with emitTaskClaimedFromEnv to close out the loom.task span
// regardless of how InvokeInteractive returned.
//
// taskID is read from the lock file at finalize time so single-task mode (no
// LOOM_ASSIGNED_TASK_ID) still records the ID the agent self-claimed during
// the run.
//
// Per the trace contract §6 invokeErr.Error() is included in TaskFailedData
// only because the otelexport classifier needs the message text to bucket
// the error; this is the same pattern the auto-mode loop uses (see
// internal/cli/automode/automode_task.go::emitTaskFailedEvent). Prompt
// content is never carried.
func emitTaskLifecycleResult(agentName, worktreePath string, startedAt time.Time, invokeErr error) {
	bus := cli.AgentEventBus()
	if bus == nil {
		return
	}
	taskID := ""
	if info, lockErr := cli.ReadLockFile(worktreePath); lockErr == nil && info != nil {
		taskID = info.TaskID
	}
	if invokeErr == nil {
		duration := events.Duration{Duration: time.Since(startedAt)}
		evt, err := events.NewEvent(events.TaskCompleted, agentName, "", "", events.TaskCompletedData{
			TaskID:   taskID,
			Duration: duration,
		})
		if err != nil {
			return
		}
		if emitErr := bus.Emit(evt); emitErr != nil {
			log.Printf("[agent] Failed to emit task_completed event: %v", emitErr)
		}
		return
	}
	// Failure path: classify the error so otelexport gets a stable
	// loom.error_type bucket. Reuses agenterr.ClassifyFromOutput, the same
	// helper auto-mode uses, so single-task and auto-mode classify
	// identically. Backend resolves to whatever ResolveBackendName picked
	// for this run; pattern lookup falls back to the exit-code table when
	// backend-specific patterns don't match.
	exitCode := 1
	var exitErr *exec.ExitError
	if errors.As(invokeErr, &exitErr) {
		exitCode = exitErr.ExitCode()
	}
	ae := agenterr.ClassifyFromOutput(invokeErr.Error(), exitCode, cli.ResolveBackendName())
	evtData := events.TaskFailedData{
		TaskID:     taskID,
		Error:      invokeErr.Error(),
		ErrorClass: ae.Class.String(),
	}
	if ae.RetryAfter > 0 {
		evtData.RetryAfter = ae.RetryAfter.String()
	}
	evt, err := events.NewEvent(events.TaskFailed, agentName, "", "", evtData)
	if err != nil {
		return
	}
	if emitErr := bus.Emit(evt); emitErr != nil {
		log.Printf("[agent] Failed to emit task_failed event: %v", emitErr)
	}
}
