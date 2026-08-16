package operationalview

import (
	"context"
	"fmt"
	"sort"

	agentsowner "github.com/tysonthomas9/loomcli/internal/modules/agents"
)

// WorkspaceAgentRecords is the read-only Agents owner seam needed to project
// the live Agent roster into a Workspace view.
type WorkspaceAgentRecords interface {
	ListAgents(context.Context, string, agentsowner.AgentFilter) ([]*agentsowner.Agent, error)
	ListRoles(context.Context, string) ([]*agentsowner.Role, error)
}

// WorkspaceRosterQuery projects the current Agents-owned roster into an
// immutable Workspace view. It deliberately remains separate from the cached
// Workspace topology query so create/archive changes are visible on the next
// read without a cross-owner cache invalidation channel.
type WorkspaceRosterQuery interface {
	Project(context.Context, *Workspace) error
}

// NewWorkspaceRosterQuery constructs the named cross-owner roster projection.
// A nil directory is a valid unavailable projection and produces an empty
// roster rather than stale or inferred Agents.
func NewWorkspaceRosterQuery(directory WorkspaceAgentRecords) WorkspaceRosterQuery {
	return workspaceRosterQuery{directory: directory}
}

type workspaceRosterQuery struct {
	directory WorkspaceAgentRecords
}

func (query workspaceRosterQuery) Project(ctx context.Context, view *Workspace) error {
	if view == nil {
		return fmt.Errorf("workspace roster: workspace view is required")
	}
	view.Agents = []Agent{}
	if query.directory == nil {
		return nil
	}
	agents, err := query.directory.ListAgents(ctx, view.ID, agentsowner.AgentFilter{})
	if err != nil {
		return fmt.Errorf("workspace roster: list Agents: %w", err)
	}
	roles, err := query.directory.ListRoles(ctx, view.ID)
	if err != nil {
		return fmt.Errorf("workspace roster: list Roles: %w", err)
	}
	rolesByName := make(map[string]*agentsowner.Role, len(roles))
	for _, role := range roles {
		if role == nil || role.WorkspaceKey != view.ID || role.Name == "" {
			return fmt.Errorf("workspace roster: invalid persisted Role: %w", agentsowner.ErrInvalidPersistedState)
		}
		rolesByName[role.Name] = role
	}
	for _, agent := range agents {
		if agent == nil || agent.WorkspaceKey != view.ID || agent.AgentID == "" {
			return fmt.Errorf("workspace roster: invalid persisted Agent: %w", agentsowner.ErrInvalidPersistedState)
		}
		if agent.Behavior.RoleName == "" {
			continue
		}
		role := rolesByName[agent.Behavior.RoleName]
		if role == nil {
			return fmt.Errorf(
				"workspace roster: Agent %q references missing Role %q: %w",
				agent.AgentID, agent.Behavior.RoleName, agentsowner.ErrInvalidPersistedState,
			)
		}
		runtime, parseErr := agentsowner.ParseRuntimeMetadata(agent.Metadata)
		if parseErr != nil {
			return fmt.Errorf("workspace roster: Agent %q runtime metadata: %w", agent.AgentID, parseErr)
		}
		roleKind := runtime.RoleKind
		if roleKind == "" {
			roleKind = role.Kind
		}
		backend := runtime.Backend
		if backend == "" {
			backend = role.Backend
		}
		view.Agents = append(view.Agents, Agent{
			Name: agent.AgentID, Kind: roleKind, RoleName: agent.Behavior.RoleName,
			Backend: backend, Repos: append([]string(nil), runtime.Repos...),
			RepoGroups: append([]string(nil), runtime.RepoGroups...), CrossRepo: runtime.CrossRepo,
		})
	}
	sort.Slice(view.Agents, func(i, j int) bool { return view.Agents[i].Name < view.Agents[j].Name })
	return nil
}
