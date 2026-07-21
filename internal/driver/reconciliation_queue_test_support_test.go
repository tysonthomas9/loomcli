package driver

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// legacyReconciliationQueuePort is test-only glue for compact memstore
// fixtures. Production composition owns the equivalent Store translation in
// app/serve and publishes only Execution's typed API.
type legacyReconciliationQueuePort struct {
	awaitEvents store.AwaitEventNotificationStore
	runOutcomes store.DriverRunOutcomeStore
}

func (port *legacyReconciliationQueuePort) ClaimAwaitEventNotifications(
	ctx context.Context,
	lease execution.AwaitEventNotificationLease,
) ([]execution.AwaitEventNotification, error) {
	values, err := port.awaitEvents.ClaimAwaitEventNotifications(ctx, store.AwaitEventNotificationClaim{
		WorkspaceKey: lease.WorkspaceKey, ClaimID: lease.ClaimID, Before: lease.Before,
		ClaimUntil: lease.ClaimUntil, Limit: lease.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]execution.AwaitEventNotification, 0, len(values))
	for _, value := range values {
		out = append(out, execution.AwaitEventNotification{
			Event: execution.AwaitEvent{
				WorkspaceKey: value.Event.WorkspaceKey, EventID: value.Event.EventID,
				SourceEventID: value.Event.SourceEventID, EventType: value.Event.EventType,
				SubjectRef: value.Event.SubjectRef, SourceKind: value.Event.SourceKind,
				Origin: string(value.Event.Origin), ActorRef: value.Event.ActorRef,
				Payload: append([]byte(nil), value.Event.Payload...),
			},
			Attempt: value.Attempt, DurableEventID: value.DurableEventID,
			CanonicalEventID: value.CanonicalEventID, PayloadOversized: value.PayloadOversized,
			PayloadSize: value.PayloadSize,
		})
	}
	return out, nil
}

func (port *legacyReconciliationQueuePort) CompleteAwaitEventNotification(
	ctx context.Context,
	completion execution.AwaitEventNotificationCompletion,
) error {
	return port.awaitEvents.CompleteAwaitEventNotification(ctx, store.AwaitEventNotificationCompletion{
		WorkspaceKey: completion.WorkspaceKey, EventID: completion.EventID,
		ClaimID: completion.ClaimID, CompletedAt: completion.CompletedAt,
	})
}

func (port *legacyReconciliationQueuePort) RetryAwaitEventNotification(
	ctx context.Context,
	retry execution.AwaitEventNotificationRetry,
) error {
	return port.awaitEvents.RetryAwaitEventNotification(ctx, store.AwaitEventNotificationRetry{
		WorkspaceKey: retry.WorkspaceKey, EventID: retry.EventID, ClaimID: retry.ClaimID,
		AvailableAt: retry.AvailableAt, Error: retry.Error,
	})
}

func (port *legacyReconciliationQueuePort) ClaimDriverRunOutcomes(
	ctx context.Context,
	lease execution.DriverRunOutcomeLease,
) ([]execution.DriverRunOutcome, error) {
	values, err := port.runOutcomes.ClaimDriverRunOutcomes(ctx, store.DriverRunOutcomeClaim{
		WorkspaceKey: lease.WorkspaceKey, ClaimID: lease.ClaimID, Before: lease.Before,
		ClaimUntil: lease.ClaimUntil, Limit: lease.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]execution.DriverRunOutcome, 0, len(values))
	for _, value := range values {
		out = append(out, execution.DriverRunOutcome{
			WorkspaceKey: value.WorkspaceKey, RunID: value.RunID,
			Status: execution.DriverRunStatus(value.Status), Summary: value.Summary,
			ErrorClass: value.ErrorClass, ParentRunID: value.ParentRunID,
			ParentEventID: value.ParentEventID, EpicID: value.EpicID,
			OccurredAt: value.OccurredAt, Attempt: value.Attempt,
		})
	}
	return out, nil
}

func (port *legacyReconciliationQueuePort) CompleteDriverRunOutcome(
	ctx context.Context,
	completion execution.DriverRunOutcomeCompletion,
) error {
	return port.runOutcomes.CompleteDriverRunOutcome(ctx, store.DriverRunOutcomeCompletion{
		WorkspaceKey: completion.WorkspaceKey, RunID: completion.RunID,
		ClaimID: completion.ClaimID, CompletedAt: completion.CompletedAt,
	})
}

func (port *legacyReconciliationQueuePort) RetryDriverRunOutcome(
	ctx context.Context,
	retry execution.DriverRunOutcomeRetry,
) error {
	return port.runOutcomes.RetryDriverRunOutcome(ctx, store.DriverRunOutcomeRetry{
		WorkspaceKey: retry.WorkspaceKey, RunID: retry.RunID, ClaimID: retry.ClaimID,
		AvailableAt: retry.AvailableAt, Error: retry.Error,
	})
}

type legacyReconciliationAuthorities struct {
	issuer *authority.Issuer
}

func (resolver *legacyReconciliationAuthorities) ResolveExecutionSystemAuthority(
	_ context.Context,
	workspace string,
	action authority.Action,
	componentID string,
) (authority.SystemAuthority, error) {
	principal, err := resolver.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: componentID, Class: authority.ClassSystem, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	return resolver.issuer.IssueSystem(principal, workspace, action, "test reconciliation component")
}

func newLegacyReconciliationQueues(
	awaitEvents store.AwaitEventNotificationStore,
	runOutcomes store.DriverRunOutcomeStore,
) (*execution.Service, *legacyReconciliationAuthorities, error) {
	issuer := authority.NewIssuer()
	rules := append(execution.OperationRules(), execution.DriverRunOperationRules()...)
	admission, err := issuer.NewAdmission(rules...)
	if err != nil {
		return nil, nil, err
	}
	port := &legacyReconciliationQueuePort{awaitEvents: awaitEvents, runOutcomes: runOutcomes}
	service, err := execution.New(execution.Dependencies{AwaitEvents: port, RunOutcomes: port}, admission)
	if err != nil {
		return nil, nil, err
	}
	return service, &legacyReconciliationAuthorities{issuer: issuer}, nil
}

func NewAwaitEventReconciler(
	outbox store.AwaitEventNotificationStore,
	dispatcher awaitEventDispatcher,
	workspace string,
	workspaces RunOutcomeWorkspaceLister,
) (*AwaitEventReconciler, error) {
	queue, authorities, err := newLegacyReconciliationQueues(outbox, nil)
	if err != nil {
		return nil, err
	}
	return NewAwaitEventReconcilerWithExecution(
		queue, authorities, dispatcher, workspace, workspaces,
		string(execution.AwaitTimeoutComponentID)+"-test",
	)
}

type noOpRunOutcomeCascade struct {
	execution.DriverRunAPI
}

type noOpTerminalDriverRunWorkRecoveryQueue struct{}

func (noOpTerminalDriverRunWorkRecoveryQueue) ClaimTerminalDriverRunWorkRecoveries(
	context.Context,
	authority.SystemAuthority,
	execution.ClaimTerminalDriverRunWorkRecoveriesCommand,
) ([]execution.DriverRunOutcome, error) {
	return []execution.DriverRunOutcome{}, nil
}

func (noOpTerminalDriverRunWorkRecoveryQueue) CompleteTerminalDriverRunWorkRecovery(
	context.Context,
	authority.SystemAuthority,
	execution.CompleteTerminalDriverRunWorkRecoveryCommand,
) error {
	return nil
}

func (noOpTerminalDriverRunWorkRecoveryQueue) RetryTerminalDriverRunWorkRecovery(
	context.Context,
	authority.SystemAuthority,
	execution.RetryTerminalDriverRunWorkRecoveryCommand,
) error {
	return nil
}

func (noOpRunOutcomeCascade) RecoverTerminalDriverRunWork(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.RecoverTerminalDriverRunWorkCommand,
) (execution.RecoverTerminalDriverRunWorkResult, error) {
	return execution.RecoverTerminalDriverRunWorkResult{
		ActionID: command.RequestID,
		Committed: &execution.RecoverTerminalDriverRunWorkCommit{
			WorkspaceKey: command.WorkspaceKey, DriverRunID: command.DriverRunID,
			ParentStatus: command.ParentStatus, Reason: command.Reason,
			ErrorClass: command.ErrorClass, RecoveredAt: command.RecoveredAt,
		},
	}, nil
}

func (noOpRunOutcomeCascade) RecoverChildDriverRunCascade(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.RecoverChildDriverRunCascadeCommand,
) (execution.CascadeChildDriverRunsResult, error) {
	return execution.CascadeChildDriverRunsResult{
		ActionID: command.RequestID,
		Committed: &execution.CascadeChildDriverRunsCommit{
			WorkspaceKey: command.WorkspaceKey, ParentRunID: command.ParentRunID,
			ParentStatus: command.ParentStatus, Reason: command.Reason,
			ErrorClass: command.ErrorClass, CascadedAt: command.CascadedAt,
			MaxDepth: command.MaxDepth,
		},
	}, nil
}

func NewRunOutcomeReconciler(
	outbox store.DriverRunOutcomeStore,
	awaits RunOutcomeAwaitNotifier,
	journal store.TriggerEventAppender,
	publisher RunOutcomePublisher,
	workspace string,
	workspaces RunOutcomeWorkspaceLister,
) (*RunOutcomeReconciler, error) {
	queue, authorities, err := newLegacyReconciliationQueues(nil, outbox)
	if err != nil {
		return nil, err
	}
	return NewRunOutcomeReconcilerWithExecution(
		queue, noOpTerminalDriverRunWorkRecoveryQueue{}, awaits, journal, publisher, workspace, workspaces,
		noOpRunOutcomeCascade{}, authorities, string(execution.DriverRunOutcomeComponentID),
	)
}

func testRunOutcomeQueue(
	outbox store.DriverRunOutcomeStore,
) (*execution.Service, *legacyReconciliationAuthorities, error) {
	return newLegacyReconciliationQueues(nil, outbox)
}

func testAwaitEvent(workspace, eventID, sourceEventID, eventType, subjectRef, sourceKind string, origin domain.TriggerEventOrigin, actor string, payload []byte) execution.AwaitEvent {
	return execution.AwaitEvent{
		WorkspaceKey: workspace, EventID: eventID, SourceEventID: sourceEventID,
		EventType: eventType, SubjectRef: subjectRef, SourceKind: sourceKind,
		Origin: string(origin), ActorRef: actor, Payload: append([]byte(nil), payload...),
	}
}
