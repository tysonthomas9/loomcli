package agents

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

const (
	DesiredStateReconciliationComponentID platformruntime.ComponentID = "serve-agents-desired-state-reconciliation"
	DesiredStateReconciliationCadence                                 = 30 * time.Second
	DesiredStateReconciliationTimeout                                 = 30 * time.Second
)

type RuntimeAuthorityProvider interface {
	AuthorityForAgentsRuntime(context.Context, platformruntime.ComponentID, string, authority.Action) (authority.SystemAuthority, error)
}

type RuntimeWorkspaceLister interface {
	ListWorkspaceKeys(context.Context) ([]string, error)
}

type RuntimeConfig struct {
	WorkspaceKey    string
	WorkspaceLister RuntimeWorkspaceLister
}

type desiredStateReconciliationComponent struct {
	agents     IdentityQueries
	commands   DesiredStateReconciliationCommands
	authority  RuntimeAuthorityProvider
	workspaces agentsRuntimeWorkspaceScope
}

var _ platformruntime.Component = (*desiredStateReconciliationComponent)(nil)

func (*desiredStateReconciliationComponent) ID() platformruntime.ComponentID {
	return DesiredStateReconciliationComponentID
}

func (component *desiredStateReconciliationComponent) RunOnce(ctx context.Context, _ time.Time) error {
	if component == nil || component.agents == nil || component.commands == nil || component.authority == nil {
		return ErrUnavailable
	}
	workspaces, err := component.workspaces.list(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, workspace := range workspaces {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		auth, err := component.authority.AuthorityForAgentsRuntime(
			ctx, component.ID(), workspace, ActionReconcileDesiredState,
		)
		if err != nil {
			errs = append(errs, fmt.Errorf("derive Agents reconciliation authority for %q: %w", workspace, err))
			continue
		}
		values, err := component.agents.ListAgents(ctx, workspace, AgentFilter{})
		if err != nil {
			errs = append(errs, fmt.Errorf("list Agents in %q: %w", workspace, err))
			continue
		}
		for _, agent := range values {
			if agent == nil || agent.DeletedAt != nil {
				continue
			}
			if _, err := component.commands.ReconcileDesiredState(ctx, auth, ReconcileDesiredStateCommand{
				WorkspaceKey: workspace, AgentID: agent.AgentID,
				ExpectedUpdatedAt: agent.UpdatedAt, GenerationID: agent.GenerationID,
			}); err != nil {
				errs = append(errs, fmt.Errorf("reconcile Agent %q in %q: %w", agent.AgentID, workspace, err))
			}
		}
	}
	return errors.Join(errs...)
}

func RuntimeRegistration(
	agents IdentityQueries,
	commands DesiredStateReconciliationCommands,
	authorityProvider RuntimeAuthorityProvider,
	config RuntimeConfig,
) (platformruntime.Registration, error) {
	if agents == nil || commands == nil || authorityProvider == nil {
		return platformruntime.Registration{}, fmt.Errorf("agents reconciliation API and authority provider are required: %w", ErrUnavailable)
	}
	workspaces, err := newAgentsRuntimeWorkspaceScope(config.WorkspaceKey, config.WorkspaceLister)
	if err != nil {
		return platformruntime.Registration{}, err
	}
	return platformruntime.Registration{
		Component: &desiredStateReconciliationComponent{
			agents: agents, commands: commands, authority: authorityProvider, workspaces: workspaces,
		},
		Policy: platformruntime.Policy{
			Cadence: DesiredStateReconciliationCadence, Immediate: true, Timeout: DesiredStateReconciliationTimeout,
			FailureBackoff: platformruntime.Backoff{Initial: time.Second, Max: time.Minute, Multiplier: 2},
		},
	}, nil
}

type agentsRuntimeWorkspaceScope struct {
	fixed  string
	lister RuntimeWorkspaceLister
}

func newAgentsRuntimeWorkspaceScope(workspace string, lister RuntimeWorkspaceLister) (agentsRuntimeWorkspaceScope, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" && lister == nil {
		return agentsRuntimeWorkspaceScope{}, fmt.Errorf("workspace lister is required for unscoped Agents runtime: %w", ErrUnavailable)
	}
	return agentsRuntimeWorkspaceScope{fixed: workspace, lister: lister}, nil
}

func (scope agentsRuntimeWorkspaceScope) list(ctx context.Context) ([]string, error) {
	if scope.fixed != "" {
		return []string{scope.fixed}, nil
	}
	workspaces, err := scope.lister.ListWorkspaceKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Agents runtime workspaces: %w", err)
	}
	seen := make(map[string]struct{}, len(workspaces))
	for _, workspace := range workspaces {
		if workspace == "" || workspace != strings.TrimSpace(workspace) {
			return nil, fmt.Errorf("workspace list contains an invalid key: %w", ErrInvalidPersistedState)
		}
		if _, duplicate := seen[workspace]; duplicate {
			return nil, fmt.Errorf("workspace list contains duplicate %q: %w", workspace, ErrInvalidPersistedState)
		}
		seen[workspace] = struct{}{}
	}
	out := append([]string(nil), workspaces...)
	sort.Strings(out)
	return out, nil
}
