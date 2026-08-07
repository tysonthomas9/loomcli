package app

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/tysonthomas9/loomcli/internal/app/systemeventing"
	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui"
)

// agentRouteAutomationCapability gives app-level route fixtures the same
// narrow Automation query seam production composition supplies. These tests
// exercise supervised agents only, so mutation methods intentionally remain
// unavailable through the embedded interface.
type agentRouteAutomationCapability struct {
	bindings automation.BindingOperations
}

var _ webui.AutomationCapability = (*agentRouteAutomationCapability)(nil)

func newAgentRouteAutomationCapability(st store.Store) webui.AutomationCapability {
	return &agentRouteAutomationCapability{bindings: &agentRouteBindingQueries{st: st}}
}

func (capability *agentRouteAutomationCapability) BindingOperations() automation.BindingOperations {
	return capability.bindings
}

func (*agentRouteAutomationCapability) AuditQueries() automation.AuditQueries { return nil }
func (*agentRouteAutomationCapability) WebhookWorkflow() *webhookingestion.Workflow {
	return nil
}
func (*agentRouteAutomationCapability) WorkflowBinding() *workflowbinding.Workflow   { return nil }
func (*agentRouteAutomationCapability) WorkflowEventing() *workfloweventing.Workflow { return nil }
func (*agentRouteAutomationCapability) ApprovalJournal() automation.ApprovalJournal  { return nil }
func (*agentRouteAutomationCapability) ApprovalAuthorityProvider() automation.ApprovalAuthorityProvider {
	return nil
}
func (*agentRouteAutomationCapability) IssueJournalEmitter() systemeventing.IssueJournalEmitter {
	return nil
}
func (*agentRouteAutomationCapability) RunOutcomePublisher() driver.RunOutcomePublisher {
	return nil
}
func (*agentRouteAutomationCapability) OperatorAuthorityResolver() workflowcataloghttp.OperatorAuthorityResolver {
	return nil
}
func (*agentRouteAutomationCapability) RuntimeRegistrations() []platformruntime.Registration {
	return nil
}

type agentRouteBindingQueries struct {
	automation.BindingOperations
	st store.Store
}

func (queries *agentRouteBindingQueries) GetBinding(ctx context.Context, workspace, bindingID string) (*automation.Binding, error) {
	binding, err := queries.st.TriggerBindings().Get(ctx, workspace, bindingID)
	if err != nil {
		return nil, mapAgentRouteBindingError(err)
	}
	return projectAgentRouteBinding(binding)
}

func (queries *agentRouteBindingQueries) ListBindings(ctx context.Context, workspace string, filter automation.BindingFilter) ([]*automation.Binding, error) {
	bindings, err := queries.st.TriggerBindings().List(ctx, workspace, store.TriggerBindingFilter{
		SourceKind: filter.SourceKind, RouteKey: filter.RouteKey, DriverID: filter.DriverID,
		TargetAgentServiceID: filter.TargetAgentServiceID, Enabled: filter.Enabled, Limit: filter.Limit,
	})
	if err != nil {
		return nil, mapAgentRouteBindingError(err)
	}
	projected := make([]*automation.Binding, 0, len(bindings))
	for _, binding := range bindings {
		item, err := projectAgentRouteBinding(binding)
		if err != nil {
			return nil, err
		}
		projected = append(projected, item)
	}
	return projected, nil
}

func projectAgentRouteBinding(binding *automation.Binding) (*automation.Binding, error) {
	if binding == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(binding)
	if err != nil {
		return nil, err
	}
	var projected automation.Binding
	if err := json.Unmarshal(encoded, &projected); err != nil {
		return nil, err
	}
	return &projected, nil
}

func mapAgentRouteBindingError(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return errors.Join(automation.ErrNotFound, err)
	}
	return err
}
