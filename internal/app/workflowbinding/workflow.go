package workflowbinding

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

var (
	ErrInvalidRequest = errors.New("workflow binding: invalid request")
	ErrUnavailable    = errors.New("workflow binding: unavailable")
)

// CreateRequest keeps workflow-name preparation separate from Automation's
// binding definition. An explicit Definition.DriverID is authoritative and
// bypasses preparation, preserving the existing API's driver-first behavior.
type CreateRequest struct {
	WorkspaceKey string
	Workflow     string
	Definition   automation.BindingDefinition
}

// Workflow is the named application workflow for workflow-addressed binding
// creation.
type Workflow struct {
	preparer WorkflowTargetPreparer
	creator  BindingCreator
}

// New constructs the workflow. Both ports are required because a missing
// preparer must never degrade workflow-name requests into unresolved driver
// identifiers.
func New(preparer WorkflowTargetPreparer, creator BindingCreator) (*Workflow, error) {
	switch {
	case preparer == nil:
		return nil, fmt.Errorf("%w: workflow target preparer is required", ErrUnavailable)
	case creator == nil:
		return nil, fmt.Errorf("%w: Automation binding creator is required", ErrUnavailable)
	default:
		return &Workflow{preparer: preparer, creator: creator}, nil
	}
}

// ResolveTarget prepares a workflow-name target without creating a binding.
// Callers use this to verify that an idempotent ensure still addresses the
// same immutable execution target as an existing binding.
func (workflow *Workflow) ResolveTarget(
	ctx context.Context,
	workspaceKey, workflowName string,
) (WorkflowTarget, error) {
	if ctx == nil {
		return WorkflowTarget{}, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if workflow == nil || workflow.preparer == nil {
		return WorkflowTarget{}, ErrUnavailable
	}
	workspaceKey = strings.TrimSpace(workspaceKey)
	workflowName = strings.TrimSpace(workflowName)
	if workspaceKey == "" {
		return WorkflowTarget{}, fmt.Errorf("%w: workspace is required", ErrInvalidRequest)
	}
	if workflowName == "" {
		return WorkflowTarget{}, fmt.Errorf("%w: workflow is required", ErrInvalidRequest)
	}
	target, err := workflow.preparer.PrepareWorkflowTarget(ctx, workspaceKey, workflowName)
	if err != nil {
		return WorkflowTarget{}, fmt.Errorf("prepare workflow target %q: %w", workflowName, err)
	}
	target.DriverID = strings.TrimSpace(target.DriverID)
	target.DriverVersionID = strings.TrimSpace(target.DriverVersionID)
	if target.DriverID == "" || target.DriverVersionID == "" {
		return WorkflowTarget{}, fmt.Errorf("prepared workflow target must include driver id and version id: %w", automation.ErrInvalidPersistedState)
	}
	return target, nil
}

// Create prepares workflow-name targets and delegates the sole durable write
// to Automation. Explicit driver targets never call the preparer.
func (workflow *Workflow) Create(
	ctx context.Context,
	auth authority.OperatorAuthority,
	request CreateRequest,
) (*automation.Binding, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if workflow == nil || workflow.preparer == nil || workflow.creator == nil {
		return nil, ErrUnavailable
	}

	request.WorkspaceKey = strings.TrimSpace(request.WorkspaceKey)
	request.Workflow = strings.TrimSpace(request.Workflow)
	request.Definition.DriverID = strings.TrimSpace(request.Definition.DriverID)
	request.Definition.DriverVersionID = strings.TrimSpace(request.Definition.DriverVersionID)
	if request.WorkspaceKey == "" {
		return nil, fmt.Errorf("%w: workspace is required", ErrInvalidRequest)
	}

	if request.Definition.DriverID == "" {
		if request.Workflow == "" {
			return nil, fmt.Errorf("%w: one of workflow or driver id is required", ErrInvalidRequest)
		}
		target, err := workflow.ResolveTarget(ctx, request.WorkspaceKey, request.Workflow)
		if err != nil {
			return nil, err
		}
		request.Definition.DriverID = target.DriverID
		// A caller-supplied version remains a requested-version guard for
		// Automation to compare with the activated version. Only an omitted
		// version is filled from preparation; never silently retarget a request.
		if request.Definition.DriverVersionID == "" {
			request.Definition.DriverVersionID = target.DriverVersionID
		}
	}

	binding, err := workflow.creator.CreateBinding(ctx, auth, automation.CreateBindingCommand{
		WorkspaceKey: request.WorkspaceKey,
		Definition:   request.Definition,
	})
	if err != nil {
		return binding, fmt.Errorf("create Automation binding: %w", err)
	}
	return binding, nil
}
