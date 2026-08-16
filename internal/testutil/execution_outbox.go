package testutil

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// StoreOutboxDeliveryAPI adapts a test store to Execution's public runtime
// port without teaching driver tests about production composition.
type StoreOutboxDeliveryAPI struct {
	Store execution.OutboxStore
}

func (api StoreOutboxDeliveryAPI) ListDueOutboxDeliveries(
	ctx context.Context,
	_ authority.SystemAuthority,
	command execution.ListDueOutboxDeliveriesCommand,
) ([]execution.OutboxDelivery, error) {
	values, err := api.Store.ListDue(ctx, command.WorkspaceKey, execution.OutboxDueFilter{Now: command.Now, Limit: command.Limit})
	if err != nil {
		return nil, err
	}
	out := make([]execution.OutboxDelivery, 0, len(values))
	for _, value := range values {
		if value == nil {
			continue
		}
		out = append(out, execution.OutboxDelivery{
			WorkspaceKey: value.WorkspaceKey, OutboxID: value.OutboxID, Kind: value.Kind,
			DriverRunID: value.DriverRunID, TaskRunID: value.TaskRunID, TargetAgent: value.TargetAgent,
			Body: value.Body, DedupeKey: value.DedupeKey, Status: value.Status, Attempt: value.Attempt,
			NextRetryAt: value.NextRetryAt, LastError: value.LastError, InboxMessageID: value.InboxMessageID,
		})
	}
	return out, nil
}

func (api StoreOutboxDeliveryAPI) RecordOutboxDeliveryResult(
	ctx context.Context,
	_ authority.SystemAuthority,
	command execution.RecordOutboxDeliveryResultCommand,
) (*execution.OutboxDelivery, error) {
	value, err := api.Store.MarkResult(ctx, command.WorkspaceKey, command.OutboxID, execution.OutboxDeliveryUpdate{
		Status: command.Status, Attempt: command.Attempt, NextRetryAt: command.NextRetryAt,
		LastError: command.LastError, InboxMessageID: command.InboxMessageID,
	})
	if err != nil {
		return nil, err
	}
	return &execution.OutboxDelivery{WorkspaceKey: value.WorkspaceKey, OutboxID: value.OutboxID, Status: value.Status, Attempt: value.Attempt}, nil
}

type StaticExecutionSystemAuthorityResolver struct{}

func (StaticExecutionSystemAuthorityResolver) ResolveExecutionSystemAuthority(
	context.Context,
	string,
	authority.Action,
	string,
) (authority.SystemAuthority, error) {
	return authority.SystemAuthority{}, nil
}

type StoreWorkspaceLister struct {
	Store workspaceowner.WorkspaceStore
}

func (lister StoreWorkspaceLister) ListWorkspaceKeys(ctx context.Context) ([]string, error) {
	values, err := lister.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != nil {
			out = append(out, value.Key)
		}
	}
	return out, nil
}

var _ execution.OutboxDeliveryAPI = StoreOutboxDeliveryAPI{}
var _ execution.SystemAuthorityResolver = StaticExecutionSystemAuthorityResolver{}
