package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
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
	ValidArgsFunction: worktreeCompletion,
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
	rootCmd.AddCommand(agentCmd)
}

func runAgent(cmd *cobra.Command, args []string) {
	// Worktree is required (enforced by cobra.ExactArgs(1))
	argName := args[0]

	// Validate prompt file exists and is a regular file
	info, err := os.Stat(agentPromptFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot access prompt file %s: %v\n", agentPromptFile, err)
		os.Exit(1)
	}
	if info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: prompt path is a directory, not a file: %s\n", agentPromptFile)
		os.Exit(1)
	}

	// Validate and map task filter
	taskCheckFn, err := mapTaskFilter(agentTaskFilter, agentParentID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Override with router-based check if daemon env vars provide routing constraints
	if routerCheck := BuildRouterTaskCheck(RoleConfigFromEnv(), AgentEntryFromEnv(), agentParentID); routerCheck != nil {
		taskCheckFn = routerCheck
	}

	// Resolve worktree/workspace path
	target, err := ResolveAgentTarget(argName, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	worktreePath := target.WorkDir
	agentName := target.AgentName

	// Create custom prompt generator
	promptGen := makeCustomPromptGen(agentPromptFile)

	// DAEMON MODE: Called by tmux session, run single task
	// Daemon manages its own lock (parent doesn't hold lock in tmux mode)
	if agentDaemonMode {
		if err := AcquireLock(worktreePath, "agent", agentName); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		// Lock intentionally NOT released here. Parent (RunAutoModeTmux)
		// reads the lock after daemon exit to detect task claims, then
		// removes it before the next cycle.

		if err := UpdateLockState(worktreePath, StateActive); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not update lock state: %v\n", err)
		}

		// Adopt parent-created session env vars for transcript tracking
		if inheritedSID := os.Getenv("LOOM_SESSION_ID"); inheritedSID != "" {
			inheritedBeads := os.Getenv("LOOM_BEADS_DIR")
			if inheritedBeads == "" {
				inheritedBeads = GetBeadsDir()
			}
			SetActiveSessionEnv(inheritedBeads, inheritedSID)
			defer ClearActiveSessionEnv()
		}

		workspace, _ := ResolveActiveWorkspace()
		prompt := promptGen(agentName, workspace)
		if err := InvokeAgent(worktreePath, prompt, agentName); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		// Note: No StateIdle here - daemon exits immediately, lock left for parent to read
		return
	}

	// AUTO MODE with tmux - daemon manages lock, not parent
	if agentAutoMode && IsTmuxAvailable() {
		shutdown := SetupSignalHandler()
		RunAutoModeTmux(AutoModeOptions{
			Interval:        agentInterval,
			MaxTasks:        agentMaxTasks,
			IdleTimeout:     agentIdleTimeout,
			AgentType:       "agent",
			AgentName:       agentName,
			WorktreePath:    worktreePath,
			ParentID:        agentParentID,
			CustomPromptGen: promptGen,
			CustomTaskCheck: taskCheckFn,
		}, shutdown)
		return
	}

	// AUTO MODE without tmux OR single task mode - parent manages lock
	if err := AcquireLock(worktreePath, "agent", agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = ReleaseLock(worktreePath) }()

	if agentAutoMode {
		// Fallback to JSON streaming mode (no tmux)
		fmt.Println("[auto] tmux not found, using JSON streaming mode")
		shutdown := SetupSignalHandler()
		RunAutoModeLoop(AutoModeOptions{
			Interval:        agentInterval,
			MaxTasks:        agentMaxTasks,
			IdleTimeout:     agentIdleTimeout,
			AgentType:       "agent",
			AgentName:       agentName,
			WorktreePath:    worktreePath,
			ParentID:        agentParentID,
			CustomPromptGen: promptGen,
			CustomTaskCheck: taskCheckFn,
		}, shutdown)
		return
	}

	// SINGLE TASK MODE
	// Check if there are tasks available based on the filter
	available, err := taskCheckFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking tasks: %v\n", err)
		os.Exit(1)
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

	if err := UpdateLockState(worktreePath, StateActive); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update lock state: %v\n", err)
	}

	// Generate and run the custom prompt
	workspace, _ := ResolveActiveWorkspace()
	prompt := promptGen(agentName, workspace)
	if err := InvokeAgent(worktreePath, prompt, agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error running agent: %v\n", err)
		os.Exit(1)
	}
}

// mapTaskFilter converts a filter string to the corresponding HasAvailable* function.
// The parentID is captured in the returned closure to scope task discovery.
func mapTaskFilter(filter, parentID string) (func() (bool, error), error) {
	repoLabel := os.Getenv("LOOM_AGENT_REPO")
	switch filter {
	case "needs_design":
		return func() (bool, error) { return HasAvailablePlanningTasks(parentID, repoLabel) }, nil
	case "has_design":
		return func() (bool, error) { return HasAvailableImplementationTasks(parentID, repoLabel) }, nil
	case "any", "":
		return func() (bool, error) { return HasAnyAvailableTasks(parentID, repoLabel) }, nil
	default:
		return nil, fmt.Errorf("invalid task filter: %s (must be needs_design, has_design, or any)", filter)
	}
}

// makeCustomPromptGen creates a prompt generator closure that loads and templates
// the specified prompt file.
func makeCustomPromptGen(promptFile string) func(string, *WorkspaceConfig) string {
	return func(agentName string, workspace *WorkspaceConfig) string {
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
