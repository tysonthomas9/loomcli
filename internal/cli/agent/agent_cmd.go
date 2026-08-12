package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/agent/tsruntime"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/workspace"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

var (
	agentPromptFile  string
	agentTaskFilter  string
	agentAutoMode    bool
	agentDaemonMode  bool // Hidden: for internal tmux session use
	agentInterval    int
	agentMaxTasks    int
	agentIdleTimeout int
	agentParentID    string
)

var agentCmd = &cobra.Command{
	Use:               "agent <worktree> --prompt <path> [flags]",
	Short:             "Run a custom agent with a user-defined prompt",
	GroupID:           "agents",
	ValidArgsFunction: cli.WorktreeCompletion,
	Long: `Run a custom agent with a user-defined prompt template.

The agent command allows you to define custom agent roles by providing a prompt
template file. This is useful for specialized workflows like code review,
documentation generation, or domain-specific tasks.

Arguments:
  worktree    Worktree/workspace name (e.g., falcon) or path (required)

Required Flags:
  -p, --prompt    Path to prompt template file

Optional Flags:
  -f, --task-filter   Task filter: needs_design, has_design, or any (bug is supervisor-daemon only)
  -a, --auto          Enable continuous mode (process multiple tasks)
  -i, --interval      Polling interval in seconds when no tasks (default: 30)
  -m, --max-tasks     Maximum tasks to process before exiting (0 = unlimited)
  -t, --idle-timeout  Exit after N minutes with no available tasks (0 = none)

Template Variables (all optional — reference only what you want):
  {{.AgentName}}        Agent name (derived from worktree)
  {{.WorktreeName}}     Worktree name
  {{.Role}}             Real role name, or "custom" outside the daemon
  {{.TaskID}}           Pre-claimed task ID (daemon mode; empty otherwise)
  {{.EpicID}}           Epic this agent is scoped to (--parent), or empty

  {{.WorkspaceBlock}}   Multi-repo workspace section with the repo table
  {{.EpicScope}}        "Only select tasks from this epic" instruction
  {{.SafetyBlock}}      Shared multi-agent safety rules
  {{.CheckpointBlock}}  Previous-attempt context after a crash or preemption
  {{.TaskDetail}}       Full detail of {{.TaskID}} (title, status, labels,
                        description, design, acceptance criteria, notes,
                        dependencies). Fetching this costs an issue-backend
                        call, so it only happens when the template names it.

Examples:
  loom agent falcon --prompt ./prompts/reviewer.txt
  loom agent falcon --prompt ./prompts/reviewer.txt --task-filter needs_design
  loom agent falcon --prompt ./prompts/reviewer.txt --auto --idle-timeout 30`,
	Args: cobra.ExactArgs(1),
	Run:  runAgent,
}

func init() {
	agentCmd.Flags().StringVarP(&agentPromptFile, "prompt", "p", "", "Path to prompt template file")
	_ = agentCmd.MarkFlagRequired("prompt")
	agentCmd.Flags().StringVarP(&agentTaskFilter, "task-filter", "f", "any", "Task filter: needs_design, has_design, or any (bug requires supervisor daemon)")
	agentCmd.Flags().BoolVarP(&agentAutoMode, "auto", "a", false, "Enable continuous mode (process multiple tasks)")
	agentCmd.Flags().BoolVar(&agentDaemonMode, "daemon-mode", false, "Internal: single task mode for daemon")
	_ = agentCmd.Flags().MarkHidden("daemon-mode")
	agentCmd.Flags().IntVarP(&agentInterval, "interval", "i", 30, "Polling interval in seconds when no tasks available")
	agentCmd.Flags().IntVarP(&agentMaxTasks, "max-tasks", "m", 0, "Maximum tasks to process (0 = unlimited)")
	agentCmd.Flags().IntVarP(&agentIdleTimeout, "idle-timeout", "t", 0, "Exit after N minutes with no tasks (0 = none)")
	agentCmd.Flags().StringVar(&agentParentID, "parent", "", "Filter tasks to descendants of this epic ID")
	cli.RegisterCommand(agentCmd)
}

func runAgent(cmd *cobra.Command, args []string) {
	deps := cli.GetDeps(cmd)
	argName := args[0]
	validatePromptFile(agentPromptFile)

	if err := validateTaskFilterExecutionMode(agentTaskFilter, agentDaemonMode); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		cli.ExitWithFlush(1)
	}
	taskCheckFn, err := mapTaskFilter(agentTaskFilter, agentParentID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		cli.ExitWithFlush(1)
	}

	// Override with router-based check if daemon env vars provide routing constraints
	if routerCheck := cli.BuildRouterTaskCheck(cli.RoleConfigFromEnv(), cli.AgentEntryFromEnv(), agentParentID); routerCheck != nil {
		taskCheckFn = routerCheck
	}

	// Publish the mode before anything can reach a backend: cli.InvokeAgent
	// refuses the interactive path under it, which is what turns a TTY-less
	// daemon spawn into a loud failure instead of a silent exit-0 no-op.
	cli.SetDaemonMode(agentDaemonMode)

	target, err := workspace.ResolveAgentTarget(argName, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		cli.ExitWithFlush(1)
		return
	}

	worktreePath := target.WorkDir
	agentName := target.AgentName
	promptGen := makeCustomPromptGen(agentPromptFile)

	if agentDaemonMode {
		if err := runAgentDaemon(deps, worktreePath, agentName, promptGen); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			cli.ExitWithFlush(1)
		}
		return
	}

	if agentAutoMode {
		runAgentAutoMode(worktreePath, agentName, promptGen, taskCheckFn)
		return
	}

	runAgentSingleTask(worktreePath, agentName, promptGen, taskCheckFn)
}

// validatePromptFile ensures the prompt path exists and is a regular file.
func validatePromptFile(path string) {
	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot access prompt file %s: %v\n", path, err)
		cli.ExitWithFlush(1)
	}
	if info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: prompt path is a directory, not a file: %s\n", path)
		cli.ExitWithFlush(1)
	}
}

// runAgentDaemon handles daemon mode for a custom-role agent. It mirrors the
// built-in plan/task leaf contract: bind the supervisor-assigned task to the
// lock and prompt, invoke headlessly, emit task lifecycle events, and finalize
// the task-bound session.
func runAgentDaemon(deps *cli.Deps, worktreePath, agentName string, promptGen func(string, *config.WorkspaceConfig) string) error {
	if err := cli.AcquireLock(worktreePath, "agent", agentName); err != nil {
		return err
	}
	// Lock intentionally NOT released here. Parent (RunAutoModeTmux)
	// reads the lock after daemon exit to detect task claims, then
	// removes it before the next cycle.

	if err := cli.UpdateLockState(worktreePath, cli.StateActive); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update lock state: %v\n", err)
	}

	assignedTaskID := strings.TrimSpace(os.Getenv("LOOM_ASSIGNED_TASK_ID"))
	if err := prepareCustomDaemonAssignment(
		cmdstore.RootContext(),
		deps.IssueBackend,
		worktreePath,
		assignedTaskID,
		agentTaskFilter,
		os.Getenv("LOOM_READ_ONLY") == "1",
	); err != nil {
		return err
	}

	ws, _ := config.ResolveActiveWorkspace()
	prompt := bindCustomDaemonTask(promptGen(agentName, ws), assignedTaskID, agentTaskFilter)
	sess := adoptOrCreateSession(agentName, agentParentID, prompt, "implementation")
	defer backends.ClearActiveSessionEnv()

	emitTaskClaimedFromEnv(agentName, assignedTaskID)

	beforeRef := automode.CaptureHEADRef(worktreePath)
	startedAt := time.Now()
	shutdown := automode.SetupSignalHandler()
	collector := usage.NewCollector(cli.GetBackendName(), agentName)
	invokeErr := tsruntime.Invoker(deps.Agent).InvokeNonInteractive(worktreePath, prompt, agentName, shutdown, collector)
	if invokeErr == nil {
		invokeErr = validateBugDaemonReviewHandoff(
			cmdstore.RootContext(),
			deps.IssueBackend,
			assignedTaskID,
			agentTaskFilter,
		)
	}
	if invokeErr == nil {
		clearDaemonResumeOnSuccess(worktreePath)
	}

	emitTaskLifecycleResult(agentName, worktreePath, startedAt, invokeErr)
	finalizeAgentSession(sess, worktreePath, beforeRef, invokeErr, collector, startedAt, agentParentID)

	return invokeErr
}

// prepareCustomDaemonAssignment establishes the leaf's task identity. The
// resume decision MUST observe the carried lock before this run overwrites its
// task ID; otherwise a session from task A could be resumed while processing
// task B. Persisting before the remaining preflight checks keeps a rejected
// assignment attributable to the supervisor for recovery.
func prepareCustomDaemonAssignment(
	ctx context.Context,
	issueBackend backend.IssueBackend,
	worktreePath, taskID, taskFilter string,
	readOnly bool,
) error {
	maybeResumeDaemonSession(worktreePath, taskID)
	persistAssignedTaskToLock(worktreePath, taskID)
	return validateAssignedTaskFilter(ctx, issueBackend, taskID, taskFilter, readOnly)
}

// validateAssignedTaskFilter is the daemon leaf's final fail-closed guard.
// Supervisor routing is authoritative for selection, but the leaf re-reads a
// bug-filtered assignment before emitting task-claimed or invoking an AI
// backend so stale/malformed assignment data cannot spend on non-bug work.
func validateAssignedTaskFilter(
	ctx context.Context,
	issueBackend backend.IssueBackend,
	taskID, taskFilter string,
	readOnly bool,
) error {
	if strings.TrimSpace(taskFilter) != "bug" {
		return nil
	}
	if !readOnly {
		return fmt.Errorf("bug task filter requires read-only execution")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("bug task filter requires a supervisor-assigned task")
	}
	if issueBackend == nil {
		return fmt.Errorf("bug task filter cannot verify assigned task %s: issue backend is unavailable", taskID)
	}
	issue, err := issueBackend.Get(ctx, taskID)
	if err != nil {
		return fmt.Errorf("bug task filter cannot verify assigned task %s: %w", taskID, err)
	}
	if issue == nil {
		return fmt.Errorf("bug task filter cannot verify assigned task %s: issue was not returned", taskID)
	}
	if !strings.EqualFold(strings.TrimSpace(issue.IssueType), "bug") {
		return fmt.Errorf(
			"bug task filter rejected assigned task %s with issue type %q",
			taskID, strings.TrimSpace(issue.IssueType),
		)
	}
	return nil
}

// validateBugDaemonReviewHandoff makes the bug-triage terminal state a
// host-verified contract rather than trusting a clean model exit. A bug worker
// that exits without moving its assigned card to Review is failed so the
// supervisor can recover/retry it, and its carried resume state is retained.
func validateBugDaemonReviewHandoff(
	ctx context.Context,
	issueBackend backend.IssueBackend,
	taskID, taskFilter string,
) error {
	if strings.TrimSpace(taskFilter) != "bug" {
		return nil
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("bug task filter cannot verify Review handoff without an assigned task")
	}
	if issueBackend == nil {
		return fmt.Errorf("bug task filter cannot verify Review handoff for %s: issue backend is unavailable", taskID)
	}
	issue, err := issueBackend.Get(ctx, taskID)
	if err != nil {
		return fmt.Errorf("bug task filter cannot verify Review handoff for %s: %w", taskID, err)
	}
	if issue == nil {
		return fmt.Errorf("bug task filter cannot verify Review handoff for %s: issue was not returned", taskID)
	}
	if status := issue.Status; status != "review" {
		return fmt.Errorf(
			"bug task filter run for %s exited successfully with task status %q; expected %q",
			taskID, status, "review",
		)
	}
	return nil
}

// validateTaskFilterExecutionMode keeps the strict issue-type filter out of
// legacy unbound execution. Only the daemon leaf receives a supervisor-selected
// task ID that it can verify before model invocation.
func validateTaskFilterExecutionMode(taskFilter string, daemonMode bool) error {
	if strings.TrimSpace(taskFilter) == "bug" && !daemonMode {
		return fmt.Errorf("bug task filter requires a supervisor-assigned daemon run")
	}
	return nil
}

// bindCustomDaemonTask makes the supervisor's claim authoritative for custom
// prompts. Custom role bodies can describe how to process work, but they must
// not run their own ready/claim loop after the supervisor has fenced one task.
func bindCustomDaemonTask(prompt, taskID, taskFilter string) string {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return prompt
	}
	handoff := `When the role's handoff is complete, run 'loom complete' and exit.`
	if strings.TrimSpace(taskFilter) == "bug" {
		handoff = `Follow the custom role's status handoff exactly. Leave the bug in
Review and do NOT run 'loom complete' because that would close it. Exit after
the Review handoff.`
	}
	return fmt.Sprintf(`## Supervisor-assigned task

Your one task for this run is %s. The supervisor has already claimed it for
this agent. Load it with 'loom data show %s --output json' and perform the
custom role below only for that task. Do NOT claim or select another task, and
do NOT run 'loom data ready'. %s

%s`, taskID, taskID, handoff, prompt)
}

// agentAutoOpts builds the common AutoModeOptions for auto mode invocations.
func agentAutoOpts(worktreePath, agentName string, promptGen func(string, *config.WorkspaceConfig) string, taskCheckFn func() (bool, error)) automode.AutoModeOptions {
	return automode.AutoModeOptions{
		Interval:        agentInterval,
		MaxTasks:        agentMaxTasks,
		IdleTimeout:     agentIdleTimeout,
		AgentType:       "agent",
		AgentName:       agentName,
		WorktreePath:    worktreePath,
		ParentID:        agentParentID,
		CustomPromptGen: promptGen,
		CustomTaskCheck: taskCheckFn,
	}
}

// runAgentAutoMode handles continuous mode with tmux (preferred) or JSON streaming fallback.
func runAgentAutoMode(worktreePath, agentName string, promptGen func(string, *config.WorkspaceConfig) string, taskCheckFn func() (bool, error)) {
	opts := agentAutoOpts(worktreePath, agentName, promptGen, taskCheckFn)

	if automode.IsTmuxAvailable() {
		shutdown := automode.SetupSignalHandler()
		automode.RunAutoModeTmux(opts, shutdown)
		return
	}

	// Fallback to JSON streaming mode (no tmux)
	if err := cli.AcquireLock(worktreePath, "agent", agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		cli.ExitWithFlush(1)
	}
	defer func() { _ = cli.ReleaseLock(worktreePath) }()

	fmt.Println("[auto] tmux not found, using JSON streaming mode")
	shutdown := automode.SetupSignalHandler()
	automode.RunAutoModeLoop(opts, shutdown)
}

// runAgentSingleTask handles the single-task (non-auto, non-daemon) execution path.
func runAgentSingleTask(worktreePath, agentName string, promptGen func(string, *config.WorkspaceConfig) string, taskCheckFn func() (bool, error)) {
	if err := cli.AcquireLock(worktreePath, "agent", agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		cli.ExitWithFlush(1)
	}
	defer func() { _ = cli.ReleaseLock(worktreePath) }()

	available, err := taskCheckFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking tasks: %v\n", err)
		cli.ExitWithFlush(1)
	}
	if !available {
		fmt.Println("No tasks available matching the specified filter.")
		fmt.Printf("Filter: %s\n", agentTaskFilter)
		return
	}

	fmt.Println("=========================================")
	fmt.Printf("Running CUSTOM agent in: %s\n", worktreePath)
	fmt.Printf("Agent name: %s\n", agentName)
	fmt.Printf("Prompt file: %s\n", agentPromptFile)
	fmt.Printf("Task filter: %s\n", agentTaskFilter)
	fmt.Println("=========================================")
	fmt.Println("")

	if err := cli.UpdateLockState(worktreePath, cli.StateActive); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update lock state: %v\n", err)
	}

	ws, _ := config.ResolveActiveWorkspace()
	prompt := promptGen(agentName, ws)
	if err := cli.InvokeAgent(worktreePath, prompt, agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error running agent: %v\n", err)
		cli.ExitWithFlush(1)
	}
}

// mapTaskFilter converts a filter string to the corresponding HasAvailable* function.
// The parentID is captured in the returned closure to scope task discovery.
func mapTaskFilter(filter, parentID string) (func() (bool, error), error) {
	repoLabel := os.Getenv("LOOM_AGENT_REPO")
	switch filter {
	case "needs_design":
		return func() (bool, error) { return automode.HasAvailablePlanningTasks(parentID, repoLabel) }, nil
	case "has_design":
		return func() (bool, error) { return automode.HasAvailableImplementationTasks(parentID, repoLabel) }, nil
	case "bug":
		return cli.BuildRouterTaskCheck(
			config.RoleConfig{TaskFilter: "bug"},
			config.AgentEntry{Repo: repoLabel},
			parentID,
		), nil
	case "any", "":
		return func() (bool, error) { return automode.HasAnyAvailableTasks(parentID, repoLabel) }, nil
	default:
		return nil, fmt.Errorf("invalid task filter: %s (must be needs_design, has_design, bug, or any)", filter)
	}
}

// makeCustomPromptGen creates a prompt generator closure that loads and templates
// the specified prompt file.
//
// The template context is built lazily through loadPromptTemplateWith: the file
// is parsed first, and only the PromptData fields it actually names are
// computed. Nothing is prepended to the rendered result except the read-only
// preamble (which predates this and is applied exactly once) — a custom role
// gets total prompt control, and the template variables are how it opts pieces
// of the built-in context back in.
// buildCustomPromptData assembles the template context for a custom-role prompt.
//
// Identity is unconditional — it is arguments and environment reads. The
// context blocks are filled in only when refs says the template names them,
// and that gate is not micro-optimization: TaskDetail is an issue-backend Get,
// which under the fleet backend is a network round trip paid on every single
// agent spawn, and CheckpointBlock stats and reads the worktree lock
// directory. A read-only critic whose prompt wants nothing but {{.TaskID}}
// must not be billed for either.
// customRoleName resolves what {{.Role}} renders as. LOOM_ROLE carries the
// REAL role name the daemon spawned this agent under ("reviewer", "critic",
// ...); the literal "custom" this used to hardcode told the prompt nothing it
// did not already know and made role-conditional templates impossible. Outside
// the daemon (a hand-run `loom agent`) there is no role record, so "custom"
// stays as the fallback.
func customRoleName() string {
	if role := strings.TrimSpace(os.Getenv("LOOM_ROLE")); role != "" {
		return role
	}
	return "custom"
}

// customTaskDetailText renders the assigned task as prompt text. Called only
// when the template references {{.TaskDetail}} — this is the one field that
// costs an issue-backend round trip.
//
// Returns "" when there is no pre-claimed task (one-shot/auto mode) or the
// fetch fails. Failing the whole render would take the agent down over context
// it merely asked for, and the warning on stderr is enough for the operator to
// see why the section came out empty.
func customTaskDetailText(taskID string) string {
	if taskID == "" {
		return ""
	}
	ctx, cancel := cmdstore.SignalContext()
	defer cancel()
	detail, err := cli.DefaultIssueBackend().Get(ctx, taskID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load task %s for {{.TaskDetail}}: %v\n", taskID, err)
		return ""
	}
	if detail == nil {
		fmt.Fprintf(os.Stderr, "Warning: task %s not found; {{.TaskDetail}} will be empty\n", taskID)
		return ""
	}
	return FormatIssueText(detail)
}

func buildCustomPromptData(agentName string, workspace *config.WorkspaceConfig, refs promptFieldRefs) PromptData {
	// A task IS already claimed before this prompt is rendered in daemon mode:
	// the supervisor's pre-flight claims for any role with a task_filter, not
	// just the built-ins, and exports LOOM_ASSIGNED_TASK_ID role-agnostically.
	// This is the same source runTaskDaemon and runPlanDaemon read; custom
	// roles simply never read it, which is why {{.TaskID}} was always blank.
	//
	// It is legitimately empty in one-shot and auto mode. There is no
	// pre-claim on those paths — the agent selects and claims its own task
	// mid-turn — so no ID exists yet at render time. Do not invent one.
	taskID := strings.TrimSpace(os.Getenv("LOOM_ASSIGNED_TASK_ID"))

	data := PromptData{
		AgentName: agentName,
		// In workspace mode, agent name may differ from worktree name,
		// but we use the same value for consistency with plan/task prompts.
		WorktreeName: agentName,
		Role:         customRoleName(),
		TaskID:       taskID,
		EpicID:       agentParentID,
	}

	if refs.has("WorkspaceBlock") {
		data.WorkspaceBlock = buildWorkspaceContextBlock(workspace)
	}
	if refs.has("EpicScope") {
		data.EpicScope = buildEpicScopeBlock(agentParentID)
	}
	if refs.has("SafetyBlock") {
		data.SafetyBlock = buildSafetyGuardrailsBlock()
	}
	if refs.has("CheckpointBlock") {
		data.CheckpointBlock = checkpointBlockIfNotResuming()
	}
	if refs.has("TaskDetail") {
		data.TaskDetail = customTaskDetailText(taskID)
	}
	return data
}

func makeCustomPromptGen(promptFile string) func(string, *config.WorkspaceConfig) string {
	return func(agentName string, workspace *config.WorkspaceConfig) string {
		prompt, err := loadPromptTemplateWith(promptFile, func(refs promptFieldRefs) PromptData {
			return buildCustomPromptData(agentName, workspace, refs)
		})
		if err != nil {
			// Log warning, try reading raw file
			fmt.Fprintf(os.Stderr, "Warning: template parsing failed: %v\n", err)
			content, readErr := os.ReadFile(promptFile)
			if readErr != nil {
				return fmt.Sprintf("Error: could not load prompt file %s: %v", promptFile, err)
			}
			return prependReadOnlyPolicy(string(content))
		}
		return prependReadOnlyPolicy(prompt)
	}
}

func prependReadOnlyPolicy(prompt string) string {
	preamble := ReadOnlyPreamble()
	if preamble == "" {
		return prompt
	}
	return preamble + "\n\n" + prompt
}
