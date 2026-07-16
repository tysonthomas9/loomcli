// Package workflowbinding coordinates trigger-binding creation when a caller
// addresses the target by workflow name. HTTP decoding, builtin materialization,
// and Automation persistence remain outside the workflow.
package workflowbinding

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// WorkflowTarget is the minimum prepared catalog identity needed to create a
// binding. Preparation never exposes a legacy Driver record to this workflow.
type WorkflowTarget struct {
	DriverID        string
	DriverVersionID string
}

// WorkflowTargetPreparer resolves a workflow name to an activated target.
// Implementations may materialize a missing builtin before returning, but the
// workflow depends only on this consumer-owned port rather than that legacy
// mechanism or its Store dependency.
type WorkflowTargetPreparer interface {
	PrepareWorkflowTarget(context.Context, string, string) (WorkflowTarget, error)
}

// BindingCreator is the exact Automation command this application workflow
// delegates to after target preparation. It intentionally omits every other
// binding, event, delivery, and runtime operation.
type BindingCreator interface {
	CreateBinding(context.Context, authority.OperatorAuthority, automation.CreateBindingCommand) (*automation.Binding, error)
}
