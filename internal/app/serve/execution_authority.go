package serve

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// executionTaskRunAuthorityResolver binds one exact handler-selected action
// to the request-derived TaskRun owner tuple. The secret is deliberately not
// embedded in authority; the immediately following outbound fenced command
// validates LeaseToken against FleetDB (or the parity memstore adapter).
type executionTaskRunAuthorityResolver struct {
	issuer *authority.Issuer
	now    func() time.Time
}

func (resolver *executionTaskRunAuthorityResolver) ResolveTaskRunAuthority(
	ctx context.Context,
	workspace string,
	action authority.Action,
	owner execution.Owner,
) (authority.ExecutionAuthority, error) {
	if resolver == nil || resolver.issuer == nil || resolver.now == nil {
		return authority.ExecutionAuthority{}, execution.ErrUnavailable
	}
	if ctx == nil {
		return authority.ExecutionAuthority{}, fmt.Errorf("execution authority context is required: %w", authority.ErrInvalidScope)
	}
	if err := ctx.Err(); err != nil {
		return authority.ExecutionAuthority{}, err
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" || !validTaskRunOwnerEnvelope(owner) || !taskRunExecutionAction(action) {
		return authority.ExecutionAuthority{}, fmt.Errorf("invalid TaskRun authority envelope: %w", authority.ErrInvalidScope)
	}
	principal, err := resolver.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject:   "task-run:" + owner.ResourceID,
		Class:     authority.ClassExecution,
		Workspace: workspace,
		Actions:   []authority.Action{action},
		ExpiresAt: resolver.now().Add(externalOperatorAuthorityTTL),
	})
	if err != nil {
		return authority.ExecutionAuthority{}, err
	}
	return resolver.issuer.IssueExecutionForOwner(principal, workspace, action, authority.ExecutionOwner{
		ResourceKind: authority.ExecutionResourceTaskRun,
		ResourceID:   owner.ResourceID,
		NodeID:       owner.NodeID,
		LeaseID:      owner.LeaseID,
		FencingToken: owner.FencingToken,
	})
}

func validTaskRunOwnerEnvelope(owner execution.Owner) bool {
	return owner.ResourceKind == execution.ResourceTaskRun &&
		strings.TrimSpace(owner.ResourceID) != "" && owner.ResourceID == strings.TrimSpace(owner.ResourceID) &&
		strings.TrimSpace(owner.NodeID) != "" && owner.NodeID == strings.TrimSpace(owner.NodeID) &&
		strings.TrimSpace(owner.LeaseID) != "" && owner.LeaseID == strings.TrimSpace(owner.LeaseID) &&
		strings.TrimSpace(owner.LeaseToken) != "" && owner.FencingToken > 0
}

func taskRunExecutionAction(action authority.Action) bool {
	switch action {
	case execution.ActionHeartbeat, execution.ActionAppendLog, execution.ActionFinalize,
		execution.ActionUpdateTaskRunWorkItemDesign,
		execution.ActionRequeueTaskRun, execution.ActionExhaustTaskRunRetries,
		artifacts.ActionDeclare, artifacts.ActionUpload, artifacts.ActionFinalize,
		artifacts.ActionReference, artifacts.ActionGet, artifacts.ActionList:
		return true
	default:
		return false
	}
}

type executionDriverRunAuthorityResolver struct {
	issuer *authority.Issuer
	now    func() time.Time
}

func (resolver *executionDriverRunAuthorityResolver) ResolveDriverRunAuthority(
	ctx context.Context,
	workspace string,
	action authority.Action,
	owner execution.Owner,
) (authority.ExecutionAuthority, error) {
	if resolver == nil || resolver.issuer == nil || resolver.now == nil {
		return authority.ExecutionAuthority{}, execution.ErrUnavailable
	}
	if ctx == nil {
		return authority.ExecutionAuthority{}, fmt.Errorf("execution authority context is required: %w", authority.ErrInvalidScope)
	}
	if err := ctx.Err(); err != nil {
		return authority.ExecutionAuthority{}, err
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" || !validDriverRunOwnerEnvelope(owner) || !driverRunExecutionAction(action) {
		return authority.ExecutionAuthority{}, fmt.Errorf("invalid DriverRun authority envelope: %w", authority.ErrInvalidScope)
	}
	principal, err := resolver.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "driver-run:" + owner.ResourceID, Class: authority.ClassExecution, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: resolver.now().Add(externalOperatorAuthorityTTL),
	})
	if err != nil {
		return authority.ExecutionAuthority{}, err
	}
	return resolver.issuer.IssueExecutionForOwner(principal, workspace, action, authority.ExecutionOwner{
		ResourceKind: authority.ExecutionResourceDriverRun, ResourceID: owner.ResourceID,
		NodeID: owner.NodeID, LeaseID: owner.LeaseID, FencingToken: owner.FencingToken,
	})
}

func validDriverRunOwnerEnvelope(owner execution.Owner) bool {
	return owner.ResourceKind == execution.ResourceDriverRun &&
		strings.TrimSpace(owner.ResourceID) != "" && owner.ResourceID == strings.TrimSpace(owner.ResourceID) &&
		strings.TrimSpace(owner.NodeID) != "" && owner.NodeID == strings.TrimSpace(owner.NodeID) &&
		strings.TrimSpace(owner.LeaseID) != "" && owner.LeaseID == strings.TrimSpace(owner.LeaseID) &&
		strings.TrimSpace(owner.LeaseToken) != "" && owner.FencingToken > 0
}

func driverRunExecutionAction(action authority.Action) bool {
	switch action {
	case execution.ActionHeartbeatDriverRun, execution.ActionFinalizeDriverRun, execution.ActionAwaitDriverRun,
		execution.ActionStartChildDriverRun, execution.ActionCascadeChildDriverRuns,
		execution.ActionClaimDriverRunWorkItem, execution.ActionReleaseDriverRunWorkItem,
		execution.ActionHandoffDriverRunReviewWorkItem,
		execution.ActionBindWorkerProfileParent, execution.ActionEnqueueLeadAssignment,
		execution.ActionRequestTaskRun, execution.ActionRecoverStaleChildTaskRuns:
		return true
	default:
		return false
	}
}

type executionSystemAuthorityResolver struct {
	issuer *authority.Issuer
	now    func() time.Time
}

func (resolver *executionSystemAuthorityResolver) ResolveExecutionSystemAuthority(
	ctx context.Context,
	workspace string,
	action authority.Action,
	componentID string,
) (authority.SystemAuthority, error) {
	if resolver == nil || resolver.issuer == nil || resolver.now == nil {
		return authority.SystemAuthority{}, execution.ErrUnavailable
	}
	if ctx == nil {
		return authority.SystemAuthority{}, fmt.Errorf("execution runtime authority context is required: %w", authority.ErrInvalidScope)
	}
	if err := ctx.Err(); err != nil {
		return authority.SystemAuthority{}, err
	}
	workspace = strings.TrimSpace(workspace)
	componentID = strings.TrimSpace(componentID)
	if workspace == "" || !registeredExecutionSystemComponent(componentID, action) {
		return authority.SystemAuthority{}, fmt.Errorf("unregistered Execution runtime component %q: %w", componentID, authority.ErrInvalidScope)
	}
	principal, err := resolver.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: componentID, Class: authority.ClassSystem, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: resolver.now().Add(externalOperatorAuthorityTTL),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	return resolver.issuer.IssueSystem(principal, workspace, action, "registered Execution runtime component "+componentID)
}

func registeredExecutionSystemComponent(componentID string, action authority.Action) bool {
	switch componentID {
	case string(execution.DriverExecutorComponentID):
		return driverExecutorSystemAction(action)
	case string(execution.AwaitTimeoutComponentID):
		return action == execution.ActionResolveDriverAwait
	case string(AwaitEventNotificationComponentID):
		return awaitEventNotificationSystemAction(action)
	case driver.RunOutcomeAwaitComponentID:
		return driverRunOutcomeSystemAction(action)
	case string(execution.TaskRunConvergenceComponentID):
		return taskRunConvergenceSystemAction(action)
	case string(execution.OutboxDeliveryComponentID):
		return outboxDeliverySystemAction(action)
	default:
		return registeredTaskRunWorkerComponent(componentID) && taskRunWorkerSystemAction(action)
	}
}

func outboxDeliverySystemAction(action authority.Action) bool {
	return action == execution.ActionListDueOutboxDeliveries ||
		action == execution.ActionRecordOutboxDeliveryResult
}

func driverExecutorSystemAction(action authority.Action) bool {
	switch action {
	case execution.ActionClaimDriverRun, execution.ActionRecoverDriverRuns,
		execution.ActionRegisterWorkerNode, execution.ActionHeartbeatWorkerNode,
		execution.ActionSetWorkerNodeDrain:
		return true
	default:
		return false
	}
}

func awaitEventNotificationSystemAction(action authority.Action) bool {
	switch action {
	case execution.ActionResolveDriverAwait, execution.ActionClaimAwaitEventNotifications,
		execution.ActionCompleteAwaitEventNotification, execution.ActionRetryAwaitEventNotification:
		return true
	default:
		return false
	}
}

func driverRunOutcomeSystemAction(action authority.Action) bool {
	switch action {
	case execution.ActionResolveDriverAwait, execution.ActionRecoverChildDriverRunCascade,
		execution.ActionRecoverTerminalDriverRunWork,
		execution.ActionClaimDriverRunOutcomes, execution.ActionCompleteDriverRunOutcome,
		execution.ActionRetryDriverRunOutcome,
		execution.ActionClaimTerminalDriverRunWorkRecoveries,
		execution.ActionCompleteTerminalDriverRunWorkRecovery,
		execution.ActionRetryTerminalDriverRunWorkRecovery:
		return true
	default:
		return false
	}
}

func taskRunConvergenceSystemAction(action authority.Action) bool {
	return action == execution.ActionConvergeTaskRun || action == execution.ActionRepairTerminalDriverStep
}

func taskRunWorkerSystemAction(action authority.Action) bool {
	switch action {
	case execution.ActionClaimTaskRun, execution.ActionRegisterWorkerNode,
		execution.ActionHeartbeatWorkerNode, execution.ActionSetWorkerNodeDrain,
		execution.ActionConvergeTaskRun, execution.ActionRepairTerminalDriverStep:
		return true
	default:
		return false
	}
}

func registeredTaskRunWorkerComponent(componentID string) bool {
	const prefix = "execution-task-run-worker-"
	if !strings.HasPrefix(componentID, prefix) {
		return false
	}
	index, err := strconv.Atoi(strings.TrimPrefix(componentID, prefix))
	return err == nil && index > 0
}
