package serve

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

// executionReconciliationQueueAdapter is the production persistence adapter
// for Execution's durable reconciliation queues. Callers receive the typed
// module API, never this adapter or its storage interfaces.
type executionReconciliationQueueAdapter struct {
	awaitEvents            execution.AwaitEventNotificationStore
	runOutcomes            execution.DriverRunOutcomeStore
	terminalWorkRecoveries execution.TerminalDriverRunWorkRecoveryQueueStore
}

var (
	_ execution.AwaitEventNotificationQueuePort        = (*executionReconciliationQueueAdapter)(nil)
	_ execution.DriverRunOutcomeQueuePort              = (*executionReconciliationQueueAdapter)(nil)
	_ execution.TerminalDriverRunWorkRecoveryQueuePort = (*executionReconciliationQueueAdapter)(nil)
)

func newExecutionReconciliationQueueAdapter(
	triggerEvents automation.TriggerEventStore,
	driverRuns execution.DriverRunStore,
) (*executionReconciliationQueueAdapter, error) {
	awaitEvents, ok := triggerEvents.(execution.AwaitEventNotificationStore)
	if !ok {
		return nil, fmt.Errorf("compose Execution: TriggerEvent store lacks durable await-notification commands")
	}
	runOutcomes, ok := driverRuns.(execution.DriverRunOutcomeStore)
	if !ok {
		return nil, fmt.Errorf("compose Execution: DriverRun store lacks durable outcome commands")
	}
	terminalWorkRecoveries, ok := driverRuns.(execution.TerminalDriverRunWorkRecoveryQueueStore)
	if !ok {
		return nil, fmt.Errorf("compose Execution: DriverRun store lacks durable terminal-work recovery commands")
	}
	return &executionReconciliationQueueAdapter{
		awaitEvents: awaitEvents, runOutcomes: runOutcomes, terminalWorkRecoveries: terminalWorkRecoveries,
	}, nil
}

func (adapter *executionReconciliationQueueAdapter) ClaimAwaitEventNotifications(
	ctx context.Context,
	lease execution.AwaitEventNotificationLease,
) ([]execution.AwaitEventNotification, error) {
	values, err := adapter.awaitEvents.ClaimAwaitEventNotifications(ctx, execution.AwaitEventNotificationLease{
		WorkspaceKey: lease.WorkspaceKey,
		ClaimID:      lease.ClaimID,
		Before:       lease.Before,
		ClaimUntil:   lease.ClaimUntil,
		Limit:        lease.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]execution.AwaitEventNotification, 0, len(values))
	for _, value := range values {
		out = append(out, execution.AwaitEventNotification{
			Event: execution.AwaitEvent{
				WorkspaceKey:  value.Event.WorkspaceKey,
				EventID:       value.Event.EventID,
				SourceEventID: value.Event.SourceEventID,
				EventType:     value.Event.EventType,
				SubjectRef:    value.Event.SubjectRef,
				SourceKind:    value.Event.SourceKind,
				Origin:        value.Event.Origin,
				ActorRef:      value.Event.ActorRef,
				Payload:       append([]byte(nil), value.Event.Payload...),
			},
			Attempt:          value.Attempt,
			DurableEventID:   value.DurableEventID,
			CanonicalEventID: value.CanonicalEventID,
			PayloadOversized: value.PayloadOversized,
			PayloadSize:      value.PayloadSize,
		})
	}
	return out, nil
}

func (adapter *executionReconciliationQueueAdapter) CompleteAwaitEventNotification(
	ctx context.Context,
	completion execution.AwaitEventNotificationCompletion,
) error {
	return adapter.awaitEvents.CompleteAwaitEventNotification(ctx, execution.AwaitEventNotificationCompletion{
		WorkspaceKey: completion.WorkspaceKey,
		EventID:      completion.EventID,
		ClaimID:      completion.ClaimID,
		CompletedAt:  completion.CompletedAt,
	})
}

func (adapter *executionReconciliationQueueAdapter) RetryAwaitEventNotification(
	ctx context.Context,
	retry execution.AwaitEventNotificationRetry,
) error {
	return adapter.awaitEvents.RetryAwaitEventNotification(ctx, execution.AwaitEventNotificationRetry{
		WorkspaceKey: retry.WorkspaceKey,
		EventID:      retry.EventID,
		ClaimID:      retry.ClaimID,
		AvailableAt:  retry.AvailableAt,
		Error:        retry.Error,
	})
}

func (adapter *executionReconciliationQueueAdapter) ClaimDriverRunOutcomes(
	ctx context.Context,
	lease execution.DriverRunOutcomeLease,
) ([]execution.DriverRunOutcome, error) {
	values, err := adapter.runOutcomes.ClaimDriverRunOutcomes(ctx, execution.DriverRunOutcomeLease{
		WorkspaceKey: lease.WorkspaceKey,
		ClaimID:      lease.ClaimID,
		Before:       lease.Before,
		ClaimUntil:   lease.ClaimUntil,
		Limit:        lease.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]execution.DriverRunOutcome, 0, len(values))
	for _, value := range values {
		out = append(out, execution.DriverRunOutcome{
			WorkspaceKey:  value.WorkspaceKey,
			RunID:         value.RunID,
			Status:        value.Status,
			Summary:       value.Summary,
			ErrorClass:    value.ErrorClass,
			ParentRunID:   value.ParentRunID,
			ParentEventID: value.ParentEventID,
			EpicID:        value.EpicID,
			OccurredAt:    value.OccurredAt,
			Attempt:       value.Attempt,
		})
	}
	return out, nil
}

func (adapter *executionReconciliationQueueAdapter) CompleteDriverRunOutcome(
	ctx context.Context,
	completion execution.DriverRunOutcomeCompletion,
) error {
	return adapter.runOutcomes.CompleteDriverRunOutcome(ctx, execution.DriverRunOutcomeCompletion{
		WorkspaceKey: completion.WorkspaceKey,
		RunID:        completion.RunID,
		ClaimID:      completion.ClaimID,
		CompletedAt:  completion.CompletedAt,
	})
}

func (adapter *executionReconciliationQueueAdapter) RetryDriverRunOutcome(
	ctx context.Context,
	retry execution.DriverRunOutcomeRetry,
) error {
	return adapter.runOutcomes.RetryDriverRunOutcome(ctx, execution.DriverRunOutcomeRetry{
		WorkspaceKey: retry.WorkspaceKey,
		RunID:        retry.RunID,
		ClaimID:      retry.ClaimID,
		AvailableAt:  retry.AvailableAt,
		Error:        retry.Error,
	})
}

func (adapter *executionReconciliationQueueAdapter) ClaimTerminalDriverRunWorkRecoveries(
	ctx context.Context,
	lease execution.TerminalDriverRunWorkRecoveryLease,
) ([]execution.DriverRunOutcome, error) {
	values, err := adapter.terminalWorkRecoveries.ClaimTerminalDriverRunWorkRecoveries(
		ctx,
		execution.TerminalDriverRunWorkRecoveryLease{
			WorkspaceKey: lease.WorkspaceKey,
			ClaimID:      lease.ClaimID,
			Before:       lease.Before,
			ClaimUntil:   lease.ClaimUntil,
			Limit:        lease.Limit,
		},
	)
	if err != nil {
		return nil, err
	}
	out := make([]execution.DriverRunOutcome, 0, len(values))
	for _, value := range values {
		out = append(out, execution.DriverRunOutcome{
			WorkspaceKey: value.WorkspaceKey, RunID: value.RunID, Status: value.Status,
			Summary: value.Summary, ErrorClass: value.ErrorClass, ParentRunID: value.ParentRunID,
			ParentEventID: value.ParentEventID, EpicID: value.EpicID, OccurredAt: value.OccurredAt, Attempt: value.Attempt,
		})
	}
	return out, nil
}

func (adapter *executionReconciliationQueueAdapter) CompleteTerminalDriverRunWorkRecovery(
	ctx context.Context,
	completion execution.TerminalDriverRunWorkRecoveryCompletion,
) error {
	return adapter.terminalWorkRecoveries.CompleteTerminalDriverRunWorkRecovery(
		ctx,
		execution.TerminalDriverRunWorkRecoveryCompletion{
			WorkspaceKey: completion.WorkspaceKey,
			RunID:        completion.RunID,
			ClaimID:      completion.ClaimID,
			CompletedAt:  completion.CompletedAt,
		},
	)
}

func (adapter *executionReconciliationQueueAdapter) RetryTerminalDriverRunWorkRecovery(
	ctx context.Context,
	retry execution.TerminalDriverRunWorkRecoveryRetry,
) error {
	return adapter.terminalWorkRecoveries.RetryTerminalDriverRunWorkRecovery(
		ctx,
		execution.TerminalDriverRunWorkRecoveryRetry{
			WorkspaceKey: retry.WorkspaceKey,
			RunID:        retry.RunID,
			ClaimID:      retry.ClaimID,
			AvailableAt:  retry.AvailableAt,
			Error:        retry.Error,
		},
	)
}
