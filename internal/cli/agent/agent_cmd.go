package agent

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/workspace"
)

var (
	agentPromptFile  string
	agentTaskFilter  string
	agentAutoMode    bool
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
  -f, --task-filter   Task filter: needs_design, has_design, bug, or any
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
	agentCmd.Flags().StringVarP(&agentTaskFilter, "task-filter", "f", "any", "Task filter: needs_design, has_design, bug, or any")
	agentCmd.Flags().BoolVarP(&agentAutoMode, "auto", "a", false, "Enable continuous mode (process multiple tasks)")
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

	// Override with a server-provided router when this compatibility command is
	// launched with explicit role routing constraints.
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

// runAgentAutoMode handles continuous custom-agent execution in the current
// process. Custom agents require their prompt generator and filter in memory,
// so they cannot be reconstructed by a detached compatibility child.
func runAgentAutoMode(worktreePath, agentName string, promptGen func(string, *config.WorkspaceConfig) string, taskCheckFn func() (bool, error)) {
	opts := agentAutoOpts(worktreePath, agentName, promptGen, taskCheckFn)
	if err := cli.AcquireLock(worktreePath, "agent", agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		cli.ExitWithFlush(1)
	}
	defer func() { _ = cli.ReleaseLock(worktreePath) }()

	fmt.Println("[auto] using managed JSON streaming mode")
	shutdown := automode.SetupSignalHandler()
	automode.RunAutoModeLoop(opts, shutdown)
}

// runAgentSingleTask handles the single-task execution path.
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
