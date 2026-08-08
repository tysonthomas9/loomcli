package automationcomposition

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/systemeventing"
	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

var errUnregisteredAutomationRuntimeAction = errors.New("automation: unregistered runtime component action")

// ApprovalAuthorityProvider converts the identity already verified by web
// session middleware into one short-lived, approval-only authority. It is
// separate from the management-role resolver because approval eligibility is
// decided by the pending await's ActorAllow predicate, not workspace-admin
// membership.
type ApprovalAuthorityProvider = automation.ApprovalAuthorityProvider

type automationApprovalAuthorityProvider struct {
	issuer *authority.Issuer
	now    func() time.Time
}

var _ ApprovalAuthorityProvider = (*automationApprovalAuthorityProvider)(nil)

func newAutomationApprovalAuthorityProvider(issuer *authority.Issuer) ApprovalAuthorityProvider {
	if issuer == nil {
		return nil
	}
	return &automationApprovalAuthorityProvider{issuer: issuer, now: time.Now}
}

func (provider *automationApprovalAuthorityProvider) AuthorityForVerifiedSession(
	ctx context.Context,
	workspace,
	actorRef string,
) (authority.OperatorAuthority, error) {
	if provider == nil || provider.issuer == nil {
		return authority.OperatorAuthority{}, automation.ErrUnavailable
	}
	if ctx == nil {
		return authority.OperatorAuthority{}, fmt.Errorf("approval authority context is required: %w", authority.ErrInvalidScope)
	}
	if err := ctx.Err(); err != nil {
		return authority.OperatorAuthority{}, err
	}
	canonicalWorkspace, canonicalActor := strings.TrimSpace(workspace), strings.TrimSpace(actorRef)
	if canonicalWorkspace == "" || canonicalWorkspace != workspace ||
		canonicalActor == "" || canonicalActor != actorRef {
		return authority.OperatorAuthority{}, authority.ErrInvalidScope
	}
	now := time.Now
	if provider.now != nil {
		now = provider.now
	}
	principal, err := provider.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: canonicalActor, Class: authority.ClassOperator, Workspace: canonicalWorkspace,
		Actions: []authority.Action{automation.ActionJournalApproval}, ExpiresAt: now().Add(time.Minute),
	})
	if err != nil {
		return authority.OperatorAuthority{}, err
	}
	return provider.issuer.IssueOperator(principal, canonicalWorkspace, automation.ActionJournalApproval)
}

// automationOperationRules is the complete default-deny authority registry
// for the Phase 3 Automation public API. Queries require no authority value;
// every mutation is represented here by one exact capability-qualified action.
func automationOperationRules() []authority.OperationRule {
	return []authority.OperationRule{
		authority.OperatorOnly(automation.ActionCreateBinding),
		authority.OperatorOnly(automation.ActionUpdateBinding),
		authority.OperatorOnly(automation.ActionEnableBinding),
		authority.OperatorOnly(automation.ActionDisableBinding),
		authority.OperatorOnly(automation.ActionDeleteBinding),
		authority.OperatorOnly(automation.ActionCreateManagedBinding),
		authority.OperatorOnly(automation.ActionUpdateManagedBinding),
		authority.OperatorOnly(automation.ActionEnableManagedBinding),
		authority.OperatorOnly(automation.ActionDisableManagedBinding),
		authority.OperatorOnly(automation.ActionDeleteManagedBinding),
		authority.Allow(automation.ActionEnsureManagedBinding, authority.ClassSystem),
		authority.OperatorOnly(automation.ActionJournalApproval),
		authority.OperatorOnly(automation.ActionDispatchBinding),
		authority.Allow(automation.ActionAdmitEvent,
			authority.ClassWebhook, authority.ClassExecution, authority.ClassSystem),
		authority.Allow(automation.ActionSweepCron, authority.ClassSystem),
		authority.Allow(automation.ActionRetryDeliveries, authority.ClassSystem),
	}
}

type automationSystemAuthorityProvider struct {
	issuer *authority.Issuer
	now    func() time.Time
}

var _ systemeventing.AuthorityProvider = (*automationSystemAuthorityProvider)(nil)

func newAutomationSystemAuthorityProvider(issuer *authority.Issuer) systemeventing.AuthorityProvider {
	if issuer == nil {
		return nil
	}
	return &automationSystemAuthorityProvider{issuer: issuer, now: time.Now}
}

// AuthorityForVerifiedSource admits only registered internal producers. The
// durable journal's actor becomes the authority subject after the component
// and workspace envelope has been verified; callers cannot put an actor in the
// event-content request itself.
func (provider *automationSystemAuthorityProvider) AuthorityForVerifiedSource(
	ctx context.Context,
	source systemeventing.VerifiedSource,
) (authority.SystemAuthority, error) {
	if provider == nil || provider.issuer == nil {
		return authority.SystemAuthority{}, automation.ErrUnavailable
	}
	if ctx == nil {
		return authority.SystemAuthority{}, fmt.Errorf("system event authority context is required: %w", authority.ErrInvalidScope)
	}
	if err := ctx.Err(); err != nil {
		return authority.SystemAuthority{}, err
	}
	workspace := strings.TrimSpace(source.WorkspaceKey)
	if workspace == "" || workspace != source.WorkspaceKey {
		return authority.SystemAuthority{}, fmt.Errorf("unregistered system event source %q: %w", source.ComponentID, authority.ErrInvalidScope)
	}
	var actor string
	switch source.ComponentID {
	case systemeventing.IssueJournalBridgeComponentID:
		actor = strings.TrimSpace(source.ActorRef)
		if actor == "" {
			actor = "system:issue-journal"
		}
	case systemeventing.DriverRunOutcomeComponentID:
		if source.ActorRef != driver.RunFinishedActor {
			return authority.SystemAuthority{}, fmt.Errorf("unregistered driver outcome actor %q: %w", source.ActorRef, authority.ErrInvalidScope)
		}
		actor = driver.RunFinishedActor
	default:
		return authority.SystemAuthority{}, fmt.Errorf("unregistered system event source %q: %w", source.ComponentID, authority.ErrInvalidScope)
	}
	now := time.Now
	if provider.now != nil {
		now = provider.now
	}
	principal, err := provider.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: actor, Class: authority.ClassSystem, Workspace: workspace,
		Actions: []authority.Action{automation.ActionAdmitEvent}, ExpiresAt: now().Add(time.Minute),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	reason := "registered system event source " + source.ComponentID + " admission"
	return provider.issuer.IssueSystem(principal, workspace, automation.ActionAdmitEvent, reason)
}

// automationOperatorActions is the exact subset delegated through the external
// operator resolver or the loopback-only local open-mode resolver. Ingestion
// and runtime actions are never delegated through either operator path.
func automationOperatorActions() []authority.Action {
	return []authority.Action{
		automation.ActionCreateBinding,
		automation.ActionUpdateBinding,
		automation.ActionEnableBinding,
		automation.ActionDisableBinding,
		automation.ActionDeleteBinding,
		automation.ActionCreateManagedBinding,
		automation.ActionUpdateManagedBinding,
		automation.ActionEnableManagedBinding,
		automation.ActionDisableManagedBinding,
		automation.ActionDeleteManagedBinding,
		automation.ActionDispatchBinding,
	}
}

type automationExecutionAuthorityProvider struct {
	issuer *authority.Issuer
	now    func() time.Time
}

var _ workfloweventing.ExecutionAuthorityProvider = (*automationExecutionAuthorityProvider)(nil)

func newAutomationExecutionAuthorityProvider(issuer *authority.Issuer) workfloweventing.ExecutionAuthorityProvider {
	if issuer == nil {
		return nil
	}
	return &automationExecutionAuthorityProvider{issuer: issuer, now: time.Now}
}

// AuthorityForVerifiedRun narrows an already verified running DriverRun to the
// single Automation admission action. The provider accepts no action selector
// and rejects incomplete, unfenced execution envelopes even if a caller inside
// the process constructs one directly.
func (provider *automationExecutionAuthorityProvider) AuthorityForVerifiedRun(
	ctx context.Context,
	parent workfloweventing.VerifiedRun,
) (authority.ExecutionAuthority, error) {
	if provider == nil || provider.issuer == nil {
		return authority.ExecutionAuthority{}, automation.ErrUnavailable
	}
	if ctx == nil {
		return authority.ExecutionAuthority{}, fmt.Errorf("execution authority context is required: %w", authority.ErrInvalidScope)
	}
	if err := ctx.Err(); err != nil {
		return authority.ExecutionAuthority{}, err
	}
	workspace := strings.TrimSpace(parent.WorkspaceKey)
	runID := strings.TrimSpace(parent.RunID)
	if workspace == "" || workspace != parent.WorkspaceKey || runID == "" || runID != parent.RunID ||
		parent.Status != "running" || strings.TrimSpace(parent.NodeID) == "" || parent.NodeID != strings.TrimSpace(parent.NodeID) ||
		strings.TrimSpace(parent.LeaseID) == "" || parent.LeaseID != strings.TrimSpace(parent.LeaseID) || parent.FencingToken <= 0 {
		return authority.ExecutionAuthority{}, fmt.Errorf("verified running execution envelope is invalid: %w", authority.ErrInvalidScope)
	}
	now := time.Now
	if provider.now != nil {
		now = provider.now
	}
	principal, err := provider.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: runID, Class: authority.ClassExecution, Workspace: workspace,
		Actions:   []authority.Action{automation.ActionAdmitEvent},
		ExpiresAt: now().Add(time.Minute),
	})
	if err != nil {
		return authority.ExecutionAuthority{}, err
	}
	return provider.issuer.IssueExecutionForOwner(principal, workspace, automation.ActionAdmitEvent, authority.ExecutionOwner{
		ResourceKind: authority.ExecutionResourceDriverRun, ResourceID: runID,
		NodeID: parent.NodeID, LeaseID: parent.LeaseID, FencingToken: parent.FencingToken,
	})
}

type automationRuntimeAuthorityProvider struct {
	issuer *authority.Issuer
	now    func() time.Time
}

var _ automation.RuntimeAuthorityProvider = (*automationRuntimeAuthorityProvider)(nil)

func newAutomationRuntimeAuthorityProvider(issuer *authority.Issuer) automation.RuntimeAuthorityProvider {
	if issuer == nil {
		return nil
	}
	return &automationRuntimeAuthorityProvider{issuer: issuer, now: time.Now}
}

func (provider *automationRuntimeAuthorityProvider) AuthorityForAutomationRuntime(
	ctx context.Context,
	componentID platformruntime.ComponentID,
	workspace string,
	action authority.Action,
) (authority.SystemAuthority, error) {
	if provider == nil || provider.issuer == nil {
		return authority.SystemAuthority{}, automation.ErrUnavailable
	}
	if ctx == nil {
		return authority.SystemAuthority{}, fmt.Errorf("runtime authority context is required: %w", authority.ErrInvalidScope)
	}
	if err := ctx.Err(); err != nil {
		return authority.SystemAuthority{}, err
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return authority.SystemAuthority{}, fmt.Errorf("runtime authority workspace is required: %w", authority.ErrInvalidScope)
	}
	subject, ok := automationRuntimeSubject(componentID, action)
	if !ok {
		return authority.SystemAuthority{}, fmt.Errorf("%w: component=%q action=%q", errUnregisteredAutomationRuntimeAction, componentID, action)
	}
	now := time.Now
	if provider.now != nil {
		now = provider.now
	}
	principal, err := provider.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: subject, Class: authority.ClassSystem, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: now().Add(time.Minute),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	reason := fmt.Sprintf("registered automation runtime component %s pass", componentID)
	return provider.issuer.IssueSystem(principal, workspace, action, reason)
}

func automationRuntimeSubject(componentID platformruntime.ComponentID, action authority.Action) (string, bool) {
	switch {
	case componentID == automation.CronSchedulerComponentID && action == automation.ActionSweepCron:
		// Retain the existing event actor on durable cron ticks.
		return "system:cron", true
	case componentID == automation.DeliverySweeperComponentID && action == automation.ActionRetryDeliveries:
		return "system:delivery-retry", true
	default:
		return "", false
	}
}

type automationWebhookAuthorityProvider struct {
	issuer *authority.Issuer
	now    func() time.Time
}

var _ webhookingestion.AuthorityProvider = (*automationWebhookAuthorityProvider)(nil)

func newAutomationWebhookAuthorityProvider(issuer *authority.Issuer) webhookingestion.AuthorityProvider {
	if issuer == nil {
		return nil
	}
	return &automationWebhookAuthorityProvider{issuer: issuer, now: time.Now}
}

func (provider *automationWebhookAuthorityProvider) AuthorityForVerifiedWebhook(
	ctx context.Context,
	request webhookingestion.AuthorityRequest,
) (authority.WebhookAuthority, error) {
	if provider == nil || provider.issuer == nil {
		return authority.WebhookAuthority{}, automation.ErrUnavailable
	}
	if ctx == nil {
		return authority.WebhookAuthority{}, fmt.Errorf("webhook authority context is required: %w", authority.ErrInvalidScope)
	}
	if err := ctx.Err(); err != nil {
		return authority.WebhookAuthority{}, err
	}
	workspace := strings.TrimSpace(request.WorkspaceKey)
	sourceKind := strings.ToLower(strings.TrimSpace(request.SourceKind))
	routeKey := strings.TrimSpace(request.RouteKey)
	if workspace == "" || sourceKind == "" || routeKey == "" ||
		sourceKind == automation.SourceKindCron || sourceKind == automation.SourceKindInternal {
		return authority.WebhookAuthority{}, fmt.Errorf("verified webhook authority scope is invalid: %w", authority.ErrInvalidScope)
	}
	sourceRef := strings.TrimSpace(request.SourceRef)
	if sourceRef == "" {
		sourceRef = routeKey
	}
	now := time.Now
	if provider.now != nil {
		now = provider.now
	}
	principal, err := provider.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "webhook:" + sourceKind + ":" + sourceRef,
		Class:   authority.ClassWebhook, Workspace: workspace,
		Actions:   []authority.Action{automation.ActionAdmitEvent},
		ExpiresAt: now().Add(time.Minute),
	})
	if err != nil {
		return authority.WebhookAuthority{}, err
	}
	return provider.issuer.IssueWebhook(principal, workspace, automation.ActionAdmitEvent)
}
