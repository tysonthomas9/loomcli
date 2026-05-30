package defs

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type workflowRouteOwner struct {
	WorkflowName string
	BindingID    string
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
