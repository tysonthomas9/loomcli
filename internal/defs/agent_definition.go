package defs

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func applyAgentDefinitions(
	ctx context.Context,
	st store.Store,
	workspaceKey string,
	actor string,
	agents []AgentModule,
	skills map[string]SkillModule,
	tools map[string]ToolModule,
) error {
	for _, agent := range agents {
		if err := applyAgentDefinition(ctx, st, workspaceKey, actor, agent, skills, tools); err != nil {
			return err
		}
	}
	return nil
}

func applyAgentDefinition(
	ctx context.Context,
	st store.Store,
	workspaceKey string,
	actor string,
	agent AgentModule,
	skills map[string]SkillModule,
	tools map[string]ToolModule,
) error {
	manifest := mustJSON(agent)
	capability := agentCapabilityManifest(agent, skills, tools)
	if _, err := st.DefinitionVersions().Apply(ctx, store.DefinitionVersionApply{
		WorkspaceKey:       workspaceKey,
		DefinitionType:     domain.DefinitionTypeAgent,
		DefinitionName:     agent.Name,
		Version:            agent.Version,
		SourceHash:         agent.SourceHash,
		BundleHash:         agent.SourceHash,
		Manifest:           manifest,
		CapabilityManifest: capability,
		CreatedBy:          actor,
		Status:             domain.DefinitionStatusActive,
	}); err != nil {
		return fmt.Errorf("apply agent definition %s: %w", agent.Name, err)
	}
	if err := upsertRole(ctx, st, workspaceKey, agent); err != nil {
		return err
	}
	return nil
}
