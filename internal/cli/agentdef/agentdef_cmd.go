// Package agentdef registers the `loom agentdef` noun-verb commands for
// fleet-db-backed agent assignment CRUD within the active workspace.
//
// Distinct from `loom agent <worktree>`, which runs an actual agent process
// with a custom prompt.
package agentdef

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backendcheck"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var (
	agentAddRole         string
	agentAddAuto         bool
	agentAddBackend      string
	agentAddRepos        []string
	agentAddRepoGroups   []string
	agentAddCrossRepo    bool
	agentAddParent       string
	agentAddMode         string
	agentAddTaskFilter   string
	agentAddMaxConc      int
	agentAddBudget       string
	agentAddTask         string
	agentAddOrchestrator string

	agentListJSON  bool
	agentShowJSON  bool
	agentStopForce bool
)

// envOrchestratorSessionID is the env var lead injects so descendants are
// auto-attributed to the lead session that spawned them.
const envOrchestratorSessionID = "LOOM_ORCHESTRATOR_SESSION_ID"

var agentdefCmd = &cobra.Command{
	Use:     "agentdef",
	Short:   "Manage agent assignments within the active workspace",
	GroupID: "workspace",
	Long: `Define long-lived agent assignments stored in fleet-db.

Distinct from 'loom agent <worktree>' which runs an actual agent process.
Phase 6 will unify these surfaces.`,
}

var agentAddCmd = &cobra.Command{
	Use:   "add <NAME>",
	Short: "Register an agent assignment in the active workspace",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentAdd,
}

var agentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List agent assignments in the active workspace",
	Args:  cobra.NoArgs,
	RunE:  runAgentList,
}

var agentShowCmd = &cobra.Command{
	Use:   "show <NAME>",
	Short: "Show agent details",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentShow,
}

var agentRemoveCmd = &cobra.Command{
	Use:   "remove <NAME>",
	Short: "Delete an agent assignment from the active workspace",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentRemove,
}

var agentStartCmd = &cobra.Command{
	Use:   "start <NAME>",
	Short: "Request an agent assignment to start",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentStart,
}

var agentStopCmd = &cobra.Command{
	Use:   "stop <NAME>",
	Short: "Request an agent assignment to stop",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentStop,
}

func init() {
	agentAddCmd.Flags().StringVar(&agentAddRole, "role", "", "Role name (required)")
	_ = agentAddCmd.MarkFlagRequired("role")
	agentAddCmd.Flags().BoolVar(&agentAddAuto, "auto", false, "Auto-start on daemon up")
	agentAddCmd.Flags().StringVar(&agentAddBackend, "backend", "", "AI backend override")
	agentAddCmd.Flags().StringSliceVar(&agentAddRepos, "repos", nil, "Repo names (comma-separated or repeat flag)")
	agentAddCmd.Flags().StringSliceVar(&agentAddRepoGroups, "repo-groups", nil, "Repo groups (comma-separated or repeat flag)")
	agentAddCmd.Flags().BoolVar(&agentAddCrossRepo, "cross-repo", false, "Allow tasks spanning repos")
	agentAddCmd.Flags().StringVar(&agentAddParent, "parent", "", "Epic ID to scope this agent to")
	agentAddCmd.Flags().StringVar(&agentAddMode, "mode", "", "Agent mode: ephemeral or service")
	agentAddCmd.Flags().StringVar(&agentAddTaskFilter, "task-filter", "", "Task filter for task-driven agents: needs_design, has_design, or any")
	agentAddCmd.Flags().IntVar(&agentAddMaxConc, "max-concurrency", 0, "Maximum concurrent runs for orchestrator/service agents")
	agentAddCmd.Flags().StringVar(&agentAddBudget, "budget-policy", "", "Budget/retry policy name")
	agentAddCmd.Flags().StringVar(&agentAddTask, "task", "", "Pin this agent's first cycle to a specific task ID (claims that task instead of polling Ready)")
	agentAddCmd.Flags().StringVar(&agentAddOrchestrator, "orchestrator", "", "Parent lead/orchestrator session ID for the queued --task start command (overrides $LOOM_ORCHESTRATOR_SESSION_ID)")

	agentListCmd.Flags().BoolVar(&agentListJSON, "json", false, "JSON output")
	agentShowCmd.Flags().BoolVar(&agentShowJSON, "json", false, "JSON output")
	agentStopCmd.Flags().BoolVar(&agentStopForce, "force", false, "Stop without graceful yield when handled by a local daemon")

	registerHookFlags(agentAddCmd, &agentAddCommentReply, &agentAddWriteDesign, &agentAddLabels,
		&agentAddRemoveLabels, &agentAddSetStatus, &agentAddClose, &agentAddCycle)
	registerHookFlags(agentUpdateCmd, &agentUpdateCommentReply, &agentUpdateWriteDesign, &agentUpdateLabels,
		&agentUpdateRemoveLabels, &agentUpdateSetStatus, &agentUpdateClose, &agentUpdateCycle)
	agentUpdateCmd.Flags().BoolVar(&agentUpdateClear, "clear-on-complete", false, "Remove all on_complete hooks from this agent")

	agentdefCmd.AddCommand(agentAddCmd, agentListCmd, agentShowCmd, agentRemoveCmd, agentStartCmd, agentStopCmd, agentUpdateCmd)
	cli.RegisterCommand(agentdefCmd)
}

func runAgentAdd(cmd *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		mode := domain.AgentMode(agentAddMode)
		// Attribution: explicit --orchestrator flag wins; otherwise inherit from
		// the env var that `loom lead` injects. Empty = unattached.
		orchestratorID := agentAddOrchestrator
		if orchestratorID == "" {
			orchestratorID = os.Getenv(envOrchestratorSessionID)
		}
		create, err := agentCreateFromFlags(ws, args[0], mode)
		if err != nil {
			return err
		}
		a, err := h.Store.Agents().Create(ctx, create)
		if err != nil {
			return fmt.Errorf("create agent: %w", err)
		}
		if err := ensureAgentDefinitionLocalWorktrees(ctx, h.Store, *a); err != nil {
			_ = h.Store.Agents().Delete(ctx, a.WorkspaceKey, a.Name)
			return err
		}
		fmt.Printf("Created agent %s/%s (role=%s)\n", a.WorkspaceKey, a.Name, a.RoleName)

		if err := enqueueAgentAddTaskStart(ctx, h.Store, ws, a.Name, orchestratorID); err != nil {
			return err
		}
		warnIfBackendMissing(cmd, a.Name, agentAddBackend)
		return nil
	})
}

func agentCreateFromFlags(workspace, name string, mode domain.AgentMode) (store.AgentCreate, error) {
	desiredState := domain.AgentDesiredState("")
	if agentAddTask != "" {
		desiredState = domain.AgentDesiredStopped
	}
	// Store the canonical spelling. Unvalidated, a filter the router does not
	// recognize is accepted here and then silently treated as "has_design" at
	// dispatch time, so a planner created with the documented "needs_design"
	// runs as a second worker.
	taskFilter, err := cli.ValidateTaskFilter(agentAddTaskFilter)
	if err != nil {
		return store.AgentCreate{}, err
	}
	hooks, err := hooksFromFlags(agentAddCommentReply, agentAddWriteDesign, agentAddLabels,
		agentAddRemoveLabels, agentAddSetStatus, agentAddClose, agentAddCycle)
	if err != nil {
		return store.AgentCreate{}, err
	}
	return store.AgentCreate{
		WorkspaceKey:   workspace,
		Name:           name,
		RoleName:       agentAddRole,
		Auto:           agentAddAuto,
		Backend:        agentAddBackend,
		Repos:          agentAddRepos,
		RepoGroups:     agentAddRepoGroups,
		CrossRepo:      agentAddCrossRepo,
		Parent:         agentAddParent,
		Mode:           mode,
		TaskFilter:     taskFilter,
		MaxConcurrency: agentAddMaxConc,
		BudgetPolicy:   agentAddBudget,
		DesiredState:   desiredState,
		Hooks:          hooks,
	}, nil
}

func enqueueAgentAddTaskStart(ctx context.Context, st store.Store, workspace, agentName, orchestratorID string) error {
	if agentAddTask == "" || st.AgentCommands() == nil {
		return nil
	}
	payload := map[string]string{"task_id": agentAddTask}
	if orchestratorID != "" {
		payload["parent_session_id"] = orchestratorID
	}
	if _, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  workspace,
		TargetAgentID: agentName,
		Type:          "start",
		Payload:       payload,
	}); err != nil {
		return fmt.Errorf("enqueue start command for task %q: %w", agentAddTask, err)
	}
	fmt.Printf("  pinned to task: %s\n", agentAddTask)
	return nil
}

// warnIfBackendMissing writes a stderr WARN if the resolved backend
// for a freshly-created agent is not on PATH. The agent is still
// created (the user may intentionally pre-create one before installing
// the binary), but the daemon will refuse to spawn it until the binary
// appears.
func warnIfBackendMissing(cmd *cobra.Command, agentName, agentBackend string) {
	effective := agentBackend
	if effective == "" {
		effective = cli.ResolveBackendName()
	}
	info, err := backendcheck.CheckBackend(effective)
	if err != nil || info.Installed {
		return
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "WARN: %s\n", info.InstallHint)
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
		"      Agent %q was recorded, but the daemon will not spawn it until %q is installed on PATH.\n",
		agentName, effective)
}

func ensureAgentDefinitionLocalWorktrees(ctx context.Context, st store.Store, agent domain.Agent) error {
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		return fmt.Errorf("load local workspace state: %w", err)
	}
	local := sc.Workspaces[agent.WorkspaceKey]
	if local.Path == "" {
		return nil
	}
	repos, err := st.Repos().List(ctx, agent.WorkspaceKey)
	if err != nil {
		return fmt.Errorf("list workspace repos: %w", err)
	}
	localRepos := make([]localworkspace.Repo, 0, len(repos))
	for _, repo := range repos {
		if repo == nil {
			continue
		}
		localRepos = append(localRepos, localworkspace.Repo{
			Name:   repo.Name,
			Path:   localworkspace.RepoPath(local, repo.Name),
			Groups: append([]string(nil), repo.Groups...),
		})
	}
	selected, err := localworkspace.SelectAgentRepos(localRepos, agent)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		return fmt.Errorf("workspace %s has no repos for agent %q", agent.WorkspaceKey, agent.Name)
	}

	created := make(map[string]string, len(selected))
	for _, repo := range selected {
		target := localworkspace.AgentWorktreePath(local.Path, repo.Name, agent.Name)
		if err := localworkspace.EnsureGitWorktree(repo.Path, target, agent.Name); err != nil {
			return fmt.Errorf("create worktree for repo %q: %w", repo.Name, err)
		}
		created[repo.Name] = target
	}
	return localworkspace.RememberAgentWorktree(agent.WorkspaceKey, agent.Name, localworkspace.FirstWorktreePath(created))
}

func runAgentList(_ *cobra.Command, _ []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		agents, err := h.Store.Agents().List(ctx, ws)
		if err != nil {
			return fmt.Errorf("list agents: %w", err)
		}
		if agentListJSON {
			return cmdstore.WriteJSON(agents)
		}
		if len(agents) == 0 {
			fmt.Printf("No agents in workspace %s\n", ws)
			return nil
		}
		for _, a := range agents {
			auto := ""
			if a.Auto {
				auto = " auto"
			}
			mode := ""
			if a.Mode != "" {
				mode = " mode=" + string(a.Mode)
			}
			fmt.Printf("%-20s role=%-10s state=%s%s%s\n", a.Name, a.RoleName, a.State, mode, auto)
		}
		return nil
	})
}

func runAgentShow(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		a, err := h.Store.Agents().Get(ctx, ws, args[0])
		if err != nil {
			return fmt.Errorf("get agent: %w", err)
		}
		if agentShowJSON {
			return cmdstore.WriteJSON(a)
		}
		fmt.Printf("Workspace:    %s\n", a.WorkspaceKey)
		fmt.Printf("Name:         %s\n", a.Name)
		fmt.Printf("Role:         %s\n", a.RoleName)
		fmt.Printf("State:        %s\n", a.State)
		fmt.Printf("Auto-start:   %t\n", a.Auto)
		if a.Backend != "" {
			fmt.Printf("Backend:      %s\n", a.Backend)
		}
		if len(a.Repos) > 0 {
			fmt.Printf("Repos:        %s\n", strings.Join(a.Repos, ", "))
		}
		if len(a.RepoGroups) > 0 {
			fmt.Printf("Repo groups:  %s\n", strings.Join(a.RepoGroups, ", "))
		}
		if a.CrossRepo {
			fmt.Printf("Cross-repo:   true\n")
		}
		if a.Parent != "" {
			fmt.Printf("Parent epic:  %s\n", a.Parent)
		}
		// AgentSession is the single source of truth for orchestrator
		// attribution; the denormalized Agent.OrchestratorSessionID
		// cache was dropped on FleetDB writes in 9aef2ae5.
		if orchID, err := store.OrchestrationSessionIDFor(ctx, h.Store, ws, a.Name); err == nil && orchID != "" {
			fmt.Printf("Orchestrator: %s\n", orchID)
		}
		if a.Mode != "" {
			fmt.Printf("Mode:         %s\n", a.Mode)
		}
		if a.TaskFilter != "" {
			fmt.Printf("Task filter:  %s\n", a.TaskFilter)
		}
		if a.MaxConcurrency > 0 {
			fmt.Printf("Max conc:     %d\n", a.MaxConcurrency)
		}
		if a.BudgetPolicy != "" {
			fmt.Printf("Budget:       %s\n", a.BudgetPolicy)
		}
		printHookPipeline(a.Hooks)
		return nil
	})
}

func runAgentRemove(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if err := h.Store.Agents().Delete(ctx, ws, args[0]); err != nil {
			return fmt.Errorf("remove agent: %w", err)
		}
		fmt.Printf("Removed agent %s/%s\n", ws, args[0])
		return nil
	})
}

func runAgentStart(_ *cobra.Command, args []string) error {
	return updateAgentDesiredState(args[0], domain.AgentDesiredRunning, domain.AgentStateActive, "start", nil)
}

func runAgentStop(_ *cobra.Command, args []string) error {
	payload := map[string]string{}
	if agentStopForce {
		payload["force"] = "true"
	}
	return updateAgentDesiredState(args[0], domain.AgentDesiredStopped, domain.AgentStateStopped, "stop", payload)
}

func updateAgentDesiredState(name string, desired domain.AgentDesiredState, state domain.AgentState, commandType string, payload map[string]string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if _, err := h.Store.Agents().Update(ctx, ws, name, store.AgentUpdate{
			DesiredState: &desired,
			State:        &state,
		}); err != nil {
			return fmt.Errorf("update agent desired state: %w", err)
		}
		if h.Store.AgentCommands() != nil {
			if _, err := h.Store.AgentCommands().Create(ctx, store.AgentCommandCreate{
				WorkspaceKey:  ws,
				TargetAgentID: name,
				Type:          commandType,
				Payload:       payload,
			}); err != nil {
				return fmt.Errorf("create agent command: %w", err)
			}
		}
		fmt.Printf("Requested agent %s/%s %s\n", ws, name, commandType)
		return nil
	})
}
