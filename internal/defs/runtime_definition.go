package defs

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func applyRuntimeDefinitions(ctx context.Context, st store.Store, workspaceKey, actor string, runtimes []RuntimeModule) error {
	for _, rt := range runtimes {
		if err := applyRuntimeDefinition(ctx, st, workspaceKey, actor, rt); err != nil {
			return err
		}
	}
	return nil
}

func applyRuntimeDefinition(ctx context.Context, st store.Store, workspaceKey, actor string, rt RuntimeModule) error {
	manifest := mustJSON(rt)
	if _, err := st.DefinitionVersions().Apply(ctx, store.DefinitionVersionApply{
		WorkspaceKey:   workspaceKey,
		DefinitionType: domain.DefinitionTypeRuntime,
		DefinitionName: rt.Name,
		Version:        rt.Version,
		SourceHash:     rt.SourceHash,
		BundleHash:     rt.SourceHash,
		Manifest:       manifest,
		CreatedBy:      actor,
		Status:         domain.DefinitionStatusActive,
	}); err != nil {
		return fmt.Errorf("apply runtime definition %s: %w", rt.Name, err)
	}
	if _, err := st.RuntimeProfiles().Upsert(ctx, store.RuntimeProfileUpsert{
		WorkspaceKey: workspaceKey,
		Name:         rt.Name,
		Version:      rt.Version,
		Provider:     rt.Provider,
		Image:        rt.Image,
		Repos:        rt.Repos,
		Env:          rt.Env,
		CPU:          rt.CPU,
		Memory:       rt.Memory,
		Manifest:     manifest,
		Status:       domain.DefinitionStatusActive,
	}); err != nil {
		return fmt.Errorf("upsert runtime profile %s: %w", rt.Name, err)
	}
	return nil
}
