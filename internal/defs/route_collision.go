package defs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type workflowRouteOwner struct {
	WorkflowName string
	BindingID    string
}

func validateWorkflowBindingCollisions(ctx context.Context, st store.Store, workspaceKey string, workflows []WorkflowModule) error {
	if err := validateWorkflowRouteCollisions(ctx, st, workspaceKey, workflows); err != nil {
		return err
	}
	return validateWorkflowTriggerCollisions(ctx, st, workspaceKey, workflows)
}

func ValidateWorkflowRouteBindingCollision(ctx context.Context, st store.Store, workspaceKey string, workflow WorkflowModule) error {
	return validateWorkflowRouteCollisions(ctx, st, workspaceKey, []WorkflowModule{workflow})
}

func ValidateWorkflowTriggerBindingCollision(ctx context.Context, st store.Store, workspaceKey string, workflow WorkflowModule) error {
	return validateWorkflowTriggerCollisions(ctx, st, workspaceKey, []WorkflowModule{workflow})
}

func validateWorkflowRouteCollisions(ctx context.Context, st store.Store, workspaceKey string, workflows []WorkflowModule) error {
	if st.RouteBindings() == nil {
		return nil
	}
	owners := map[string]workflowRouteOwner{}
	existing, err := st.RouteBindings().List(ctx, workspaceKey, store.RouteBindingFilter{Status: domain.DefinitionStatusActive, Limit: 10000})
	if err != nil {
		return fmt.Errorf("list active workflow route bindings: %w", err)
	}
	for _, route := range existing {
		if route == nil || route.DefinitionType != domain.DefinitionTypeWorkflow {
			continue
		}
		owners[workflowRouteCollisionKey(route.Method, route.Path)] = workflowRouteOwner{
			WorkflowName: route.DefinitionName,
			BindingID:    route.BindingID,
		}
	}
	for _, wf := range workflows {
		if strings.TrimSpace(wf.RoutePath) == "" {
			continue
		}
		key := workflowRouteCollisionKey("POST", wf.RoutePath)
		if owner, ok := owners[key]; ok && owner.WorkflowName != wf.Name {
			return fmt.Errorf("workflow route collision %s: workflow %q conflicts with %q (%s)", key, wf.Name, owner.WorkflowName, owner.BindingID)
		}
		owners[key] = workflowRouteOwner{WorkflowName: wf.Name, BindingID: routeBindingID(wf.Name, "POST", wf.RoutePath)}
	}
	return nil
}

func validateWorkflowTriggerCollisions(ctx context.Context, st store.Store, workspaceKey string, workflows []WorkflowModule) error {
	if st.TriggerBindings() == nil {
		return nil
	}
	owners := map[string]workflowRouteOwner{}
	existing, err := st.TriggerBindings().List(ctx, workspaceKey, store.TriggerBindingFilter{Status: domain.DefinitionStatusActive, Limit: 10000})
	if err != nil {
		return fmt.Errorf("list active workflow trigger bindings: %w", err)
	}
	for _, trigger := range existing {
		if trigger == nil {
			continue
		}
		owners[workflowTriggerCollisionKey(trigger.EventType, stringMapFromRaw(trigger.Filter))] = workflowRouteOwner{
			WorkflowName: trigger.WorkflowName,
			BindingID:    trigger.BindingID,
		}
	}
	for _, wf := range workflows {
		if strings.TrimSpace(wf.TriggerEvent) == "" {
			continue
		}
		key := workflowTriggerCollisionKey(wf.TriggerEvent, wf.TriggerFilter)
		if owner, ok := owners[key]; ok && owner.WorkflowName != wf.Name {
			return fmt.Errorf("workflow trigger collision %s: workflow %q conflicts with %q (%s)", key, wf.Name, owner.WorkflowName, owner.BindingID)
		}
		owners[key] = workflowRouteOwner{WorkflowName: wf.Name, BindingID: "workflow:" + wf.Name + ":" + wf.TriggerEvent}
	}
	return nil
}

func workflowRouteCollisionKey(method, routePath string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = "POST"
	}
	routePath = "/" + strings.Trim(strings.TrimSpace(routePath), "/")
	if routePath == "/" {
		return method + " /"
	}
	return method + " " + routePath
}

func workflowTriggerCollisionKey(event string, filter map[string]string) string {
	event = strings.TrimSpace(event)
	normalized := make(map[string]string, len(filter))
	for key, value := range filter {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		normalized[key] = value
	}
	data, _ := json.Marshal(normalized)
	return event + " " + string(data)
}
