// Package agent registers the worker-agent cobra commands — `loom plan`,
// `loom task`, `loom agent`, `loom claim`, `loom complete`, `loom list`,
// `loom recover` — and owns worker prompt generation from workspace/role
// config plus per-worktree crash recovery (stale lock and process-group
// cleanup). Blank-imported by cmd/loom for command registration; the daemon
// supervisor calls its exported RecoverWorktree, ResumeTTL, and archive-log
// helpers directly.
package agent

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/workspace"
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
  -f, --task-filter   Task filter: needs_design, has_design, or any (default: any)
  -a, --auto          Enable continuous mode (process multiple tasks)
  -i, --interval      Polling interval in seconds when no tasks (default: 30)
  -m, --max-tasks     Maximum tasks to process before exiting (0 = unlimited)
  -t, --idle-timeout  Exit after N minutes with no available tasks (0 = none)

Template Variables:
  {{.AgentName}}      Agent name (derived from worktree)
  {{.WorktreeName}}   Worktree name
  {{.Role}}           Role name (always "custom" for this command)

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
	agentCmd.Flags().StringVarP(&agentTaskFilter, "task-filter", "f", "any", "Task filter: needs_design, has_design, or any")
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
	argName := args[0]
	validatePromptFile(agentPromptFile)

	taskCheckFn, err := mapTaskFilter(agentTaskFilter, agentParentID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		cli.ExitWithFlush(1)
	}

	// Override with router-based check if daemon env vars provide routing constraints
	if routerCheck := cli.BuildRouterTaskCheck(cli.RoleConfigFromEnv(), cli.AgentEntryFromEnv(), agentParentID); routerCheck != nil {
		taskCheckFn = routerCheck
	}

	target, err := workspace.ResolveAgentTarget(argName, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		cli.ExitWithFlush(1)
	}

	worktreePath := target.WorkDir
	agentName := target.AgentName
	promptGen := makeCustomPromptGen(agentPromptFile)

	if agentDaemonMode {
		runAgentDaemon(worktreePath, agentName, promptGen)
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

// runAgentDaemon handles daemon mode: single task execution inside a tmux session.
// The daemon manages its own lock; the parent reads the lock after exit.
func runAgentDaemon(worktreePath, agentName string, promptGen func(string, *config.WorkspaceConfig) string) {
	if err := cli.AcquireLock(worktreePath, "agent", agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		cli.ExitWithFlush(1)
	}
	// Lock intentionally NOT released here. Parent (RunAutoModeTmux)
	// reads the lock after daemon exit to detect task claims, then
	// removes it before the next cycle.

	if err := cli.UpdateLockState(worktreePath, cli.StateActive); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update lock state: %v\n", err)
	}

	// Adopt parent-created session env vars for transcript tracking
	if inheritedSID := os.Getenv("LOOM_SESSION_ID"); inheritedSID != "" {
		inheritedRuntimeDir := os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR")
		if inheritedRuntimeDir == "" {
			inheritedRuntimeDir = cli.GetWorkspaceRuntimeDir()
		}
		backends.SetActiveSessionRuntimeEnv(inheritedRuntimeDir, inheritedSID)
		defer backends.ClearActiveSessionEnv()
	}

	ws, _ := config.ResolveActiveWorkspace()
	prompt := promptGen(agentName, ws)
	if err := cli.InvokeAgent(worktreePath, prompt, agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		cli.ExitWithFlush(1)
	}
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
	case "any", "":
		return func() (bool, error) { return automode.HasAnyAvailableTasks(parentID, repoLabel) }, nil
	default:
		return nil, fmt.Errorf("invalid task filter: %s (must be needs_design, has_design, or any)", filter)
	}
}

// makeCustomPromptGen creates a prompt generator closure that loads and templates
// the specified prompt file.
func makeCustomPromptGen(promptFile string) func(string, *config.WorkspaceConfig) string {
	return func(agentName string, workspace *config.WorkspaceConfig) string {
		// In workspace mode, agent name may differ from worktree name,
		// but we use the same value for consistency with plan/task prompts.
		worktreeName := agentName

		data := PromptData{
			AgentName:    agentName,
			WorktreeName: worktreeName,
			Role:         "custom",
		}

		prompt, err := LoadPromptTemplate(promptFile, data)
		if err != nil {
			// Log warning, try reading raw file
			fmt.Fprintf(os.Stderr, "Warning: template parsing failed: %v\n", err)
			content, readErr := os.ReadFile(promptFile)
			if readErr != nil {
				return fmt.Sprintf("Error: could not load prompt file %s: %v", promptFile, err)
			}
			return string(content)
		}
		return prompt
	}
}
