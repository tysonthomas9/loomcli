package serve

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type executionOutboxDeliveryAdapter struct {
	store store.OutboxStore
}

var _ execution.OutboxDeliveryPort = (*executionOutboxDeliveryAdapter)(nil)

func (adapter *executionOutboxDeliveryAdapter) EnqueueOutboxDelivery(
	ctx context.Context,
	request execution.OutboxDeliveryEnqueue,
) (*execution.OutboxDelivery, error) {
	value, err := adapter.store.Create(ctx, store.OutboxCreate{
		WorkspaceKey: request.WorkspaceKey,
		Kind:         domain.OutboxKind(request.Kind),
		EpicID:       request.EpicID,
		DriverRunID:  request.DriverRunID,
		TargetAgent:  request.TargetAgent,
		DedupeKey:    request.DedupeKey,
	})
	if err != nil || value == nil {
		return nil, err
	}
	return publicOutboxDelivery(value), nil
}

func (adapter *executionOutboxDeliveryAdapter) ListDueOutboxDeliveries(
	ctx context.Context,
	query execution.OutboxDeliveryQuery,
) ([]execution.OutboxDelivery, error) {
	values, err := adapter.store.ListDue(ctx, query.WorkspaceKey, store.OutboxDueFilter{
		Now: query.Now, Limit: query.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]execution.OutboxDelivery, 0, len(values))
	for _, value := range values {
		if value == nil {
			continue
		}
		out = append(out, *publicOutboxDelivery(value))
	}
	return out, nil
}

func (adapter *executionOutboxDeliveryAdapter) RecordOutboxDeliveryResult(
	ctx context.Context,
	result execution.OutboxDeliveryResult,
) (*execution.OutboxDelivery, error) {
	value, err := adapter.store.MarkResult(ctx, result.WorkspaceKey, result.OutboxID, store.OutboxDeliveryUpdate{
		Status: domain.OutboxStatus(result.Status), Attempt: result.Attempt, NextRetryAt: cloneOutboxTime(result.NextRetryAt),
		LastError: result.LastError, InboxMessageID: result.InboxMessageID,
	})
	if err != nil || value == nil {
		return nil, err
	}
	return publicOutboxDelivery(value), nil
}

func publicOutboxDelivery(value *domain.OutboxRecord) *execution.OutboxDelivery {
	return &execution.OutboxDelivery{
		WorkspaceKey: value.WorkspaceKey, OutboxID: value.OutboxID,
		Kind: execution.OutboxKind(value.Kind), EpicID: value.EpicID, DriverRunID: value.DriverRunID, TaskRunID: value.TaskRunID,
		TargetAgent: value.TargetAgent, Body: value.Body, DedupeKey: value.DedupeKey,
		Status: execution.OutboxDeliveryStatus(value.Status), Attempt: value.Attempt, NextRetryAt: cloneOutboxTime(value.NextRetryAt),
		LastError: value.LastError, InboxMessageID: value.InboxMessageID,
	}
}

func cloneOutboxTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
