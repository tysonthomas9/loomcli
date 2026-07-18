package serve

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// executionReconciliationQueueAdapter is the only production translation
// from Execution's queue commands to the legacy Store capability interfaces.
// Callers receive the typed module API, never this adapter or either Store.
type executionReconciliationQueueAdapter struct {
	awaitEvents store.AwaitEventNotificationStore
	runOutcomes store.DriverRunOutcomeStore
}

var (
	_ execution.AwaitEventNotificationQueuePort = (*executionReconciliationQueueAdapter)(nil)
	_ execution.DriverRunOutcomeQueuePort       = (*executionReconciliationQueueAdapter)(nil)
)

func newExecutionReconciliationQueueAdapter(
	triggerEvents store.TriggerEventStore,
	driverRuns store.DriverRunStore,
) (*executionReconciliationQueueAdapter, error) {
	awaitEvents, ok := triggerEvents.(store.AwaitEventNotificationStore)
	if !ok {
		return nil, fmt.Errorf("compose Execution: TriggerEvent store lacks durable await-notification commands")
	}
	runOutcomes, ok := driverRuns.(store.DriverRunOutcomeStore)
	if !ok {
		return nil, fmt.Errorf("compose Execution: DriverRun store lacks durable outcome commands")
	}
	return &executionReconciliationQueueAdapter{awaitEvents: awaitEvents, runOutcomes: runOutcomes}, nil
}

func (adapter *executionReconciliationQueueAdapter) ClaimAwaitEventNotifications(
	ctx context.Context,
	lease execution.AwaitEventNotificationLease,
) ([]execution.AwaitEventNotification, error) {
	values, err := adapter.awaitEvents.ClaimAwaitEventNotifications(ctx, store.AwaitEventNotificationClaim{
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
				Origin:        string(value.Event.Origin),
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
	return adapter.awaitEvents.CompleteAwaitEventNotification(ctx, store.AwaitEventNotificationCompletion{
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
	return adapter.awaitEvents.RetryAwaitEventNotification(ctx, store.AwaitEventNotificationRetry{
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
	values, err := adapter.runOutcomes.ClaimDriverRunOutcomes(ctx, store.DriverRunOutcomeClaim{
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
			Status:        execution.DriverRunStatus(value.Status),
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
	return adapter.runOutcomes.CompleteDriverRunOutcome(ctx, store.DriverRunOutcomeCompletion{
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
	return adapter.runOutcomes.RetryDriverRunOutcome(ctx, store.DriverRunOutcomeRetry{
		WorkspaceKey: retry.WorkspaceKey,
		RunID:        retry.RunID,
		ClaimID:      retry.ClaimID,
		AvailableAt:  retry.AvailableAt,
		Error:        retry.Error,
	})
}
