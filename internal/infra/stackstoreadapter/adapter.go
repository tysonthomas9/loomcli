// Package stackstoreadapter adapts the local stack-lineage store to Source
// Control's narrow task-outcome persistence port.
package stackstoreadapter

import (
	"context"
	"fmt"
	"time"

	stackstore "github.com/tysonthomas9/loomcli/internal/infra/sourcecontrolstackstore"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol/stacklineage"
)

type Adapter struct{ store stackstore.Store }

var _ sourcecontrol.TaskOutcomeStore = (*Adapter)(nil)
var _ sourcecontrol.StackLifecycleStore = (*Adapter)(nil)

func New(store stackstore.Store) (*Adapter, error) {
	if store == nil {
		return nil, fmt.Errorf("compose Source Control stack store: %w", sourcecontrol.ErrUnavailable)
	}
	return &Adapter{store: store}, nil
}

func Default() (*Adapter, error) {
	store, err := stackstore.Default()
	if err != nil {
		return nil, err
	}
	return New(store)
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

func (adapter *Adapter) EnsureStackRecord(ctx context.Context, value sourcecontrol.Stack) error {
	return adapter.store.EnsureStack(ctx, stacklineage.Stack{
		ID: stacklineage.StackID(value.ID), WorkspaceKey: value.WorkspaceKey,
		RepoName: value.Repository, RootBase: value.RootBase,
		DefaultCommitMode: stacklineage.CommitMode(value.DefaultCommitMode),
	})
}

func (adapter *Adapter) GetStackRecord(ctx context.Context, workspace, stackID string) (*sourcecontrol.Stack, error) {
	value, err := adapter.store.GetStack(ctx, workspace, stacklineage.StackID(stackID))
	if err != nil {
		return nil, err
	}
	result := stackProjection(*value)
	return &result, nil
}

func (adapter *Adapter) ListStackRecords(ctx context.Context, workspace string) ([]sourcecontrol.Stack, error) {
	values, err := adapter.store.ListStacks(ctx, workspace)
	if err != nil {
		return nil, err
	}
	result := make([]sourcecontrol.Stack, len(values))
	for index, value := range values {
		result[index] = stackProjection(value)
	}
	return result, nil
}

func (adapter *Adapter) ListStackNodeRecords(
	ctx context.Context,
	workspace,
	stackID string,
) ([]sourcecontrol.StackNode, error) {
	values, err := adapter.store.ListNodes(ctx, workspace, stacklineage.StackID(stackID))
	if err != nil {
		return nil, err
	}
	result := make([]sourcecontrol.StackNode, len(values))
	for index, value := range values {
		result[index] = stackNodeProjection(value)
	}
	return result, nil
}

func (adapter *Adapter) AddStackNodeRecord(
	ctx context.Context,
	workspace,
	stackID,
	taskID,
	baseTaskID,
	commitMode string,
) (sourcecontrol.StackNode, error) {
	value, err := adapter.store.AddNode(
		ctx, workspace, stacklineage.StackID(stackID), taskID, baseTaskID, stacklineage.CommitMode(commitMode),
	)
	if err != nil {
		return sourcecontrol.StackNode{}, err
	}
	return stackNodeProjection(value), nil
}

func (adapter *Adapter) MoveStackNodeRecord(ctx context.Context, workspace, stackID, taskID, afterTaskID string) error {
	return adapter.store.MoveNode(ctx, workspace, stacklineage.StackID(stackID), taskID, afterTaskID)
}

func (adapter *Adapter) SetStackNodeBaseRecord(ctx context.Context, workspace, stackID, taskID, baseTaskID string) error {
	return adapter.store.SetBase(ctx, workspace, stacklineage.StackID(stackID), taskID, baseTaskID)
}

func (adapter *Adapter) RemoveStackNodeRecord(ctx context.Context, workspace, stackID, taskID string) error {
	return adapter.store.RemoveNode(ctx, workspace, stacklineage.StackID(stackID), taskID)
}

func (adapter *Adapter) UpdateStackNodePublicationRecord(
	ctx context.Context,
	workspace,
	stackID,
	taskID string,
	mutation sourcecontrol.StackNodePublicationMutation,
) error {
	return adapter.store.UpdateNode(
		ctx, workspace, stacklineage.StackID(stackID), taskID,
		func(node *stacklineage.Node) error {
			node.State = stacklineage.NodeState(mutation.State)
			if mutation.State == sourcecontrol.StackPublicationPublished {
				node.PRNumber = mutation.PRNumber
				node.PRURL = mutation.PRURL
			}
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

func stackProjection(value stacklineage.Stack) sourcecontrol.Stack {
	return sourcecontrol.Stack{
		ID: string(value.ID), WorkspaceKey: value.WorkspaceKey, Repository: value.RepoName,
		RootBase: value.RootBase, DefaultCommitMode: string(value.DefaultCommitMode),
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func stackNodeProjection(value stacklineage.Node) sourcecontrol.StackNode {
	return sourcecontrol.StackNode{
		StackID: string(value.StackID), TaskID: value.TaskID, BaseTaskID: value.BaseTaskID,
		OutputBranch: value.OutputBranch, CommitMode: string(value.CommitMode), State: string(value.State),
		PRNumber: value.PRNumber, PRURL: value.PRURL, OutputSHA: value.OutputSHA,
		LastPublishedAt: cloneTime(value.LastPublishedAt), CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
