// Package agentdef registers the `loom agentdef` noun-verb commands for
// fleet-db-backed agent assignment CRUD within the active workspace.
//
// Distinct from `loom agent <worktree>`, which runs an actual agent process
// with a custom prompt.
package agentdef

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	workspacecmd "github.com/tysonthomas9/loomcli/internal/cli/workspace"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var (
	agentAddRole       string
	agentAddAuto       bool
	agentAddBackend    string
	agentAddRepos      []string
	agentAddRepoGroups []string
	agentAddCrossRepo  bool
	agentAddParent     string
	agentAddMode       string
	agentAddTaskFilter string
	agentAddMaxConc    int
	agentAddBudget     string

	agentListJSON  bool
	agentShowJSON  bool
	agentStopForce bool
)

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
	agentAddCmd.Flags().StringVar(&agentAddTaskFilter, "task-filter", "", "Task filter for task-driven agents")
	agentAddCmd.Flags().IntVar(&agentAddMaxConc, "max-concurrency", 0, "Maximum concurrent runs for orchestrator/service agents")
	agentAddCmd.Flags().StringVar(&agentAddBudget, "budget-policy", "", "Budget/retry policy name")

	agentListCmd.Flags().BoolVar(&agentListJSON, "json", false, "JSON output")
	agentShowCmd.Flags().BoolVar(&agentShowJSON, "json", false, "JSON output")
	agentStopCmd.Flags().BoolVar(&agentStopForce, "force", false, "Stop without graceful yield when handled by a local daemon")

	agentdefCmd.AddCommand(agentAddCmd, agentListCmd, agentShowCmd, agentRemoveCmd, agentStartCmd, agentStopCmd)
	cli.RegisterCommand(agentdefCmd)
}

func runAgentAdd(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		mode := domain.AgentMode(agentAddMode)
		a, err := h.Store.Agents().Create(ctx, store.AgentCreate{
			WorkspaceKey:   ws,
			Name:           args[0],
			RoleName:       agentAddRole,
			Auto:           agentAddAuto,
			Backend:        agentAddBackend,
			Repos:          agentAddRepos,
			RepoGroups:     agentAddRepoGroups,
			CrossRepo:      agentAddCrossRepo,
			Parent:         agentAddParent,
			Mode:           mode,
			TaskFilter:     agentAddTaskFilter,
			MaxConcurrency: agentAddMaxConc,
			BudgetPolicy:   agentAddBudget,
		})
		if err != nil {
			return fmt.Errorf("create agent: %w", err)
		}
		if err := ensureAgentDefinitionLocalWorktrees(ctx, h.Store, *a); err != nil {
			_ = h.Store.Agents().Delete(ctx, a.WorkspaceKey, a.Name)
			return err
		}
		fmt.Printf("Created agent %s/%s (role=%s)\n", a.WorkspaceKey, a.Name, a.RoleName)
		return nil
	})
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
	selected, err := selectAgentDefinitionRepos(repos, agent)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		return fmt.Errorf("workspace %s has no repos for agent %q", agent.WorkspaceKey, agent.Name)
	}

	created := make(map[string]string, len(selected))
	for _, repo := range selected {
		repoPath := local.Repos[repo.Name]
		if repoPath == "" {
			repoPath = filepath.Join(local.Path, repo.Name)
		}
		target := filepath.Join(local.Path, "worktrees", repo.Name, agent.Name)
		if err := workspacecmd.EnsureRepoWorktree(repoPath, target, agent.Name); err != nil {
			return fmt.Errorf("create worktree for repo %q: %w", repo.Name, err)
		}
		created[repo.Name] = target
	}
	return rememberAgentDefinitionWorktree(agent.WorkspaceKey, agent.Name, firstAgentDefinitionWorktree(created))
}

func selectAgentDefinitionRepos(repos []*domain.Repo, agent domain.Agent) ([]*domain.Repo, error) {
	if len(repos) == 0 {
		return nil, nil
	}
	if agent.CrossRepo {
		return repos, nil
	}
	allowed := make(map[string]struct{})
	for _, name := range agent.Repos {
		allowed[name] = struct{}{}
	}
	for _, group := range agent.RepoGroups {
		for _, repo := range repos {
			if repo == nil {
				continue
			}
			for _, repoGroup := range repo.Groups {
				if repoGroup == group {
					allowed[repo.Name] = struct{}{}
				}
			}
		}
	}
	if len(allowed) == 0 {
		return []*domain.Repo{repos[0]}, nil
	}
	selected := make([]*domain.Repo, 0, len(allowed))
	for _, repo := range repos {
		if repo == nil {
			continue
		}
		if _, ok := allowed[repo.Name]; ok {
			selected = append(selected, repo)
		}
	}
	if len(selected) == 0 {
		names := make([]string, 0, len(repos))
		for _, repo := range repos {
			if repo != nil {
				names = append(names, repo.Name)
			}
		}
		sort.Strings(names)
		return nil, fmt.Errorf("agent repo affinity does not match any workspace repo; available repos: %s", strings.Join(names, ", "))
	}
	return selected, nil
}

func rememberAgentDefinitionWorktree(wsKey, agentName, worktreePath string) error {
	return bootstrap.WithStateLock(func() error {
		sc, err := bootstrap.LoadStateCache()
		if err != nil {
			return err
		}
		if sc.Workspaces == nil {
			sc.Workspaces = make(map[string]bootstrap.WorkspaceLocalState)
		}
		local := sc.Workspaces[wsKey]
		if local.Agents == nil {
			local.Agents = make(map[string]bootstrap.AgentLocalState)
		}
		local.Agents[agentName] = bootstrap.AgentLocalState{Worktree: worktreePath}
		sc.Workspaces[wsKey] = local
		return bootstrap.SaveStateCache(sc)
	})
}

func firstAgentDefinitionWorktree(paths map[string]string) string {
	if len(paths) == 0 {
		return ""
	}
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)
	return paths[names[0]]
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
