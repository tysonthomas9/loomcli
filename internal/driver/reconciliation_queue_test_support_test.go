package driver

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// testReconciliationQueuePort adapts compact memstore fixtures to Execution's
// current queue ports. Production composition publishes only the typed API.
type testReconciliationQueuePort struct {
	awaitEvents execution.AwaitEventNotificationStore
	runOutcomes execution.DriverRunOutcomeStore
}

func (port *testReconciliationQueuePort) ClaimAwaitEventNotifications(
	ctx context.Context,
	lease execution.AwaitEventNotificationLease,
) ([]execution.AwaitEventNotification, error) {
	values, err := port.awaitEvents.ClaimAwaitEventNotifications(ctx, execution.AwaitEventNotificationLease{
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
				Origin: value.Event.Origin, ActorRef: value.Event.ActorRef,
				Payload: append([]byte(nil), value.Event.Payload...),
			},
			Attempt: value.Attempt, DurableEventID: value.DurableEventID,
			CanonicalEventID: value.CanonicalEventID, PayloadOversized: value.PayloadOversized,
			PayloadSize: value.PayloadSize,
		})
	}
	return out, nil
}

func (port *testReconciliationQueuePort) CompleteAwaitEventNotification(
	ctx context.Context,
	completion execution.AwaitEventNotificationCompletion,
) error {
	return port.awaitEvents.CompleteAwaitEventNotification(ctx, execution.AwaitEventNotificationCompletion{
		WorkspaceKey: completion.WorkspaceKey, EventID: completion.EventID,
		ClaimID: completion.ClaimID, CompletedAt: completion.CompletedAt,
	})
}

func (port *testReconciliationQueuePort) RetryAwaitEventNotification(
	ctx context.Context,
	retry execution.AwaitEventNotificationRetry,
) error {
	return port.awaitEvents.RetryAwaitEventNotification(ctx, execution.AwaitEventNotificationRetry{
		WorkspaceKey: retry.WorkspaceKey, EventID: retry.EventID, ClaimID: retry.ClaimID,
		AvailableAt: retry.AvailableAt, Error: retry.Error,
	})
}

func (port *testReconciliationQueuePort) ClaimDriverRunOutcomes(
	ctx context.Context,
	lease execution.DriverRunOutcomeLease,
) ([]execution.DriverRunOutcome, error) {
	values, err := port.runOutcomes.ClaimDriverRunOutcomes(ctx, execution.DriverRunOutcomeLease{
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
			Status: value.Status, Summary: value.Summary,
			ErrorClass: value.ErrorClass, ParentRunID: value.ParentRunID,
			ParentEventID: value.ParentEventID, EpicID: value.EpicID,
			OccurredAt: value.OccurredAt, Attempt: value.Attempt,
		})
	}
	return out, nil
}

func (port *testReconciliationQueuePort) CompleteDriverRunOutcome(
	ctx context.Context,
	completion execution.DriverRunOutcomeCompletion,
) error {
	return port.runOutcomes.CompleteDriverRunOutcome(ctx, execution.DriverRunOutcomeCompletion{
		WorkspaceKey: completion.WorkspaceKey, RunID: completion.RunID,
		ClaimID: completion.ClaimID, CompletedAt: completion.CompletedAt,
	})
}

func (port *testReconciliationQueuePort) RetryDriverRunOutcome(
	ctx context.Context,
	retry execution.DriverRunOutcomeRetry,
) error {
	return port.runOutcomes.RetryDriverRunOutcome(ctx, execution.DriverRunOutcomeRetry{
		WorkspaceKey: retry.WorkspaceKey, RunID: retry.RunID, ClaimID: retry.ClaimID,
		AvailableAt: retry.AvailableAt, Error: retry.Error,
	})
}

type testReconciliationAuthorities struct {
	issuer *authority.Issuer
}

func (resolver *testReconciliationAuthorities) ResolveExecutionSystemAuthority(
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

func newTestReconciliationQueues(
	awaitEvents execution.AwaitEventNotificationStore,
	runOutcomes execution.DriverRunOutcomeStore,
) (*execution.Service, *testReconciliationAuthorities, error) {
	issuer := authority.NewIssuer()
	rules := append(execution.OperationRules(), execution.DriverRunOperationRules()...)
	admission, err := issuer.NewAdmission(rules...)
	if err != nil {
		return nil, nil, err
	}
	port := &testReconciliationQueuePort{awaitEvents: awaitEvents, runOutcomes: runOutcomes}
	service, err := execution.New(execution.Dependencies{AwaitEvents: port, RunOutcomes: port}, admission)
	if err != nil {
		return nil, nil, err
	}
	return service, &testReconciliationAuthorities{issuer: issuer}, nil
}

func newTestAwaitEventReconciler(
	outbox execution.AwaitEventNotificationStore,
	dispatcher trigger.AwaitEventDispatcher,
	workspace string,
	workspaces RunOutcomeWorkspaceLister,
) (*trigger.AwaitEventReconciler, error) {
	queue, authorities, err := newTestReconciliationQueues(outbox, nil)
	if err != nil {
		return nil, err
	}
	return trigger.NewAwaitEventReconcilerWithExecution(
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

func newTestRunOutcomeReconciler(
	outbox execution.DriverRunOutcomeStore,
	awaits RunOutcomeAwaitNotifier,
	journal automation.TriggerEventAppender,
	publisher RunOutcomePublisher,
	workspace string,
	workspaces RunOutcomeWorkspaceLister,
) (*RunOutcomeReconciler, error) {
	queue, authorities, err := newTestReconciliationQueues(nil, outbox)
	if err != nil {
		return nil, err
	}
	return NewRunOutcomeReconcilerWithExecution(
		queue, noOpTerminalDriverRunWorkRecoveryQueue{}, awaits, journal, publisher, workspace, workspaces,
		noOpRunOutcomeCascade{}, authorities, string(execution.DriverRunOutcomeComponentID),
	)
}

func testRunOutcomeQueue(
	outbox execution.DriverRunOutcomeStore,
) (*execution.Service, *testReconciliationAuthorities, error) {
	return newTestReconciliationQueues(nil, outbox)
}

func testAwaitEvent(workspace, eventID, sourceEventID, eventType, subjectRef, sourceKind string, origin automation.EventOrigin, actor string, payload []byte) execution.AwaitEvent {
	return execution.AwaitEvent{
		WorkspaceKey: workspace, EventID: eventID, SourceEventID: sourceEventID,
		EventType: eventType, SubjectRef: subjectRef, SourceKind: sourceKind,
		Origin: string(origin), ActorRef: actor, Payload: append([]byte(nil), payload...),
	}
}
