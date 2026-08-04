// Package stackstoreadapter adapts the local stack-lineage store to Source
// Control's narrow task-outcome persistence port.
package stackstoreadapter

import (
	"context"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

type Adapter struct{ store stackstore.Store }

var _ sourcecontrol.TaskOutcomeStore = (*Adapter)(nil)

func New(store stackstore.Store) (*Adapter, error) {
	if store == nil {
		return nil, fmt.Errorf("compose Source Control stack store: %w", sourcecontrol.ErrUnavailable)
	}
	return &Adapter{store: store}, nil
}

func (adapter *Adapter) ListTaskStacks(ctx context.Context, workspace string) ([]sourcecontrol.TaskStack, error) {
	values, err := adapter.store.ListStacks(ctx, workspace)
	if err != nil {
		return nil, err
	}
	result := make([]sourcecontrol.TaskStack, len(values))
	for index, value := range values {
		result[index] = sourcecontrol.TaskStack{
			StackID: string(value.ID), WorkspaceKey: value.WorkspaceKey, Repository: value.RepoName,
		}
	}
	return result, nil
}

func (adapter *Adapter) ListTaskStackNodes(
	ctx context.Context,
	workspace,
	stackID string,
) ([]sourcecontrol.TaskStackNode, error) {
	values, err := adapter.store.ListNodes(ctx, workspace, stacklineage.StackID(stackID))
	if err != nil {
		return nil, err
	}
	result := make([]sourcecontrol.TaskStackNode, len(values))
	for index, value := range values {
		result[index] = sourcecontrol.TaskStackNode{TaskID: value.TaskID}
	}
	return result, nil
}

func (adapter *Adapter) UpdateTaskStackOutcome(
	ctx context.Context,
	workspace,
	stackID,
	taskID string,
	mutation sourcecontrol.TaskStackOutcomeMutation,
) error {
	return adapter.store.UpdateNode(
		ctx, workspace, stacklineage.StackID(stackID), taskID,
		func(node *stacklineage.Node) error {
			node.State = stacklineage.NodeState(mutation.State)
			if mutation.OutputSHA != "" {
				node.OutputSHA = mutation.OutputSHA
			}
			if mutation.PublishedAt != nil {
				node.LastPublishedAt = cloneTime(mutation.PublishedAt)
			}
			return nil
		},
	)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
