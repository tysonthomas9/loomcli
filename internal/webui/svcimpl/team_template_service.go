package svcimpl

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/teamtemplate"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

type teamTemplateService struct {
	store store.Store
}

func NewTeamTemplateService(st store.Store) *teamTemplateService {
	return &teamTemplateService{store: st}
}

func (s *teamTemplateService) CatalogTeamTemplates(context.Context) ([]teamtemplate.TeamTemplate, error) {
	return teamtemplate.All(), nil
}

func (s *teamTemplateService) ApplyTeamTemplate(
	ctx context.Context,
	workspaceKey string,
	teamTemplateID string,
	dryRun bool,
) (teamtemplate.ApplyReport, error) {
	if s.store == nil {
		return teamtemplate.ApplyReport{}, service.ErrUnavailable("Team Templates require a fleet-db store")
	}
	tpl, ok := teamtemplate.ByID(teamTemplateID)
	if !ok {
		return teamtemplate.ApplyReport{}, service.ErrNotFound(fmt.Sprintf("Team Template %q not found", teamTemplateID))
	}

	workspace, err := storeadapter.BuildWorkspaceDataForKey(ctx, s.store, workspaceKey)
	if err != nil {
		return teamtemplate.ApplyReport{}, classifyStoreError("load workspace", err)
	}
	runnableAgentCount, err := s.runnableAgentCount(ctx, workspaceKey)
	if err != nil {
		return teamtemplate.ApplyReport{}, err
	}
	maxAgents, err := s.maxAgents(ctx, workspaceKey)
	if err != nil {
		return teamtemplate.ApplyReport{}, err
	}

	deps := teamtemplate.ApplyDeps{
		Store:              s.store,
		DryRun:             dryRun,
		LocalPath:          workspace.Path,
		RunnableAgentCount: runnableAgentCount,
		MaxAgents:          maxAgents,
	}
	if workspace.Path != "" {
		materializer := newAgentWorktreeMaterializer(s.store)
		deps.LocalMaterializer = materializer.Materialize
	}

	report, err := teamtemplate.Apply(ctx, deps, workspaceKey, tpl)
	if err == nil {
		return report, nil
	}
	// Apply's landed API does not expose a typed preflight-refusal error. The
	// workspace view already loaded for ApplyDeps gives us the same condition
	// without parsing the error message or duplicating a store mutation.
	if workspace.Path != "" && len(workspace.Repos) == 0 {
		return report, service.ErrValidation(err.Error())
	}
	if errors.Is(err, domain.ErrNotFound) {
		return report, service.ErrNotFound(err.Error())
	}
	return report, service.ErrInternal("apply Team Template", err)
}

func (s *teamTemplateService) runnableAgentCount(ctx context.Context, workspaceKey string) (int, error) {
	agents, err := s.store.Agents().List(ctx, workspaceKey)
	if err != nil {
		return 0, classifyStoreError("list agents", err)
	}
	roles, err := s.store.Roles().List(ctx, workspaceKey)
	if err != nil {
		return 0, classifyStoreError("list agent roles", err)
	}
	rolesByName := make(map[string]*domain.Role, len(roles))
	for _, role := range roles {
		if role != nil {
			rolesByName[role.Name] = role
		}
	}
	count := 0
	for _, agent := range agents {
		if agent == nil || agent.DesiredState == domain.AgentDesiredStopped || agent.DesiredState == domain.AgentDesiredDraining {
			continue
		}
		if domain.ResolveRoleKind(rolesByName[agent.RoleName], agent.RoleName) == domain.RoleKindInteractive {
			continue
		}
		count++
	}
	return count, nil
}

func (s *teamTemplateService) maxAgents(ctx context.Context, workspaceKey string) (int, error) {
	profile, err := s.store.Daemon().Get(ctx, workspaceKey)
	if err != nil {
		return 0, classifyStoreError("load daemon profile", err)
	}
	if profile != nil && profile.MaxAgents != nil {
		return *profile.MaxAgents, nil
	}
	return teamtemplate.DefaultMaxAgents, nil
}
