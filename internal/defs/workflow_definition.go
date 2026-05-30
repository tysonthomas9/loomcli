package defs

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func applyWorkflowDefinitions(
	ctx context.Context,
	st store.Store,
	workspaceKey string,
	actor string,
	workflows []WorkflowModule,
	tools map[string]ToolModule,
) error {
	if err := validateWorkflowBindingCollisions(ctx, st, workspaceKey, workflows); err != nil {
		return err
	}
	for _, wf := range workflows {
		if err := applyWorkflowDefinition(ctx, st, workspaceKey, actor, wf, tools); err != nil {
			return err
		}
	}
	return nil
}

func applyWorkflowDefinition(
	ctx context.Context,
	st store.Store,
	workspaceKey string,
	actor string,
	wf WorkflowModule,
	tools map[string]ToolModule,
) error {
	if err := validateWorkflowRuntimeProfile(ctx, st, workspaceKey, wf); err != nil {
		return err
	}
	manifest := mustJSON(wf)
	capability := workflowCapabilityManifest(wf, tools)
	if _, err := st.DefinitionVersions().Apply(ctx, store.DefinitionVersionApply{
		WorkspaceKey:       workspaceKey,
		DefinitionType:     domain.DefinitionTypeWorkflow,
		DefinitionName:     wf.Name,
		Version:            wf.Version,
		SourceHash:         wf.SourceHash,
		BundleHash:         wf.SourceHash,
		Manifest:           manifest,
		CapabilityManifest: capability,
		CreatedBy:          actor,
		Status:             domain.DefinitionStatusActive,
	}); err != nil {
		return fmt.Errorf("apply workflow definition %s: %w", wf.Name, err)
	}
	if _, err := st.WorkflowDefinitions().Upsert(ctx, store.WorkflowDefinitionUpsert{
		WorkspaceKey:       workspaceKey,
		Name:               wf.Name,
		Version:            wf.Version,
		Description:        wf.Description,
		SingletonPolicy:    wf.SingletonPolicy,
		RuntimeProfileName: wf.RuntimeProfileName,
		SourceRef:          wf.SourcePath,
		BundleHash:         wf.SourceHash,
		Manifest:           manifest,
		CapabilityManifest: capability,
		Status:             domain.DefinitionStatusActive,
	}); err != nil {
		return fmt.Errorf("upsert workflow definition %s: %w", wf.Name, err)
	}
	if err := applyWorkflowRouteBinding(ctx, st, workspaceKey, wf); err != nil {
		return err
	}
	if err := applyWorkflowTriggerBinding(ctx, st, workspaceKey, wf); err != nil {
		return err
	}
	return nil
}

func validateWorkflowRuntimeProfile(ctx context.Context, st store.Store, workspaceKey string, wf WorkflowModule) error {
	if wf.RuntimeProfileName == "" {
		return nil
	}
	if st.RuntimeProfiles() == nil {
		return fmt.Errorf("%s: workflow %q declares runtime profile %q but runtime profile store is not configured", wf.SourcePath, wf.Name, wf.RuntimeProfileName)
	}
	if _, err := st.RuntimeProfiles().Get(ctx, workspaceKey, wf.RuntimeProfileName); err != nil {
		return fmt.Errorf("%s: workflow %q declares unknown runtime profile %q: %w", wf.SourcePath, wf.Name, wf.RuntimeProfileName, err)
	}
	return nil
}

func applyWorkflowRouteBinding(ctx context.Context, st store.Store, workspaceKey string, wf WorkflowModule) error {
	if wf.RoutePath == "" {
		return nil
	}
	if _, err := st.RouteBindings().Upsert(ctx, store.RouteBindingUpsert{
		WorkspaceKey:   workspaceKey,
		BindingID:      routeBindingID(wf.Name, "POST", wf.RoutePath),
		DefinitionName: wf.Name,
		DefinitionType: domain.DefinitionTypeWorkflow,
		Path:           wf.RoutePath,
		Method:         "POST",
		AuthPolicy:     wf.RouteAuth,
		Status:         domain.DefinitionStatusActive,
	}); err != nil {
		return fmt.Errorf("upsert route binding %s: %w", wf.Name, err)
	}
	return nil
}

func applyWorkflowTriggerBinding(ctx context.Context, st store.Store, workspaceKey string, wf WorkflowModule) error {
	if wf.TriggerEvent == "" {
		return nil
	}
	if _, err := st.TriggerBindings().Upsert(ctx, store.TriggerBindingUpsert{
		WorkspaceKey: workspaceKey,
		BindingID:    "workflow:" + wf.Name + ":" + wf.TriggerEvent,
		WorkflowName: wf.Name,
		EventType:    wf.TriggerEvent,
		Filter:       mustJSON(wf.TriggerFilter),
		Status:       domain.DefinitionStatusActive,
	}); err != nil {
		return fmt.Errorf("upsert trigger binding %s: %w", wf.Name, err)
	}
	return nil
}
