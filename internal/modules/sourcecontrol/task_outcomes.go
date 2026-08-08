package sourcecontrol

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// TaskOutcomeService owns interpretation of trusted runner delivery evidence,
// unique stack selection, and the resulting lineage transition.
type TaskOutcomeService struct {
	store TaskOutcomeStore
	now   func() time.Time
}

var _ TaskOutcomeRecorder = (*TaskOutcomeService)(nil)

func NewTaskOutcomes(store TaskOutcomeStore, now func() time.Time) (*TaskOutcomeService, error) {
	if store == nil || now == nil {
		return nil, fmt.Errorf("compose Source Control task outcomes: store and clock are required: %w", ErrUnavailable)
	}
	return &TaskOutcomeService{store: store, now: now}, nil
}

//nolint:cyclop,funlen // The recorder validates every persisted stack boundary before its single mutation.
func (service *TaskOutcomeService) RecordTaskOutcome(
	ctx context.Context,
	command TaskOutcomeCommand,
) (bool, error) {
	if service == nil || service.store == nil || service.now == nil {
		return false, ErrUnavailable
	}
	mutation, ok := taskOutcomeMutation(command.Metadata, service.now)
	if !ok {
		return false, nil
	}
	workspace := strings.TrimSpace(command.WorkspaceKey)
	repository := strings.TrimSpace(command.Repository)
	taskID := strings.TrimSpace(command.TaskID)
	if workspace == "" || repository == "" || taskID == "" ||
		workspace != command.WorkspaceKey || repository != command.Repository || taskID != command.TaskID {
		return false, fmt.Errorf("task outcome coordinates must be canonical: %w", ErrInvalid)
	}
	stacks, err := service.store.ListTaskStacks(ctx, workspace)
	if err != nil {
		return false, fmt.Errorf("list task stacks: %w", err)
	}
	stackID := ""
	for _, stack := range stacks {
		if stack.WorkspaceKey != workspace || strings.TrimSpace(stack.StackID) == "" ||
			strings.TrimSpace(stack.StackID) != stack.StackID || strings.TrimSpace(stack.Repository) == "" ||
			strings.TrimSpace(stack.Repository) != stack.Repository {
			return false, fmt.Errorf("task stack escaped requested scope: %w", ErrInvalidMaterialization)
		}
		if stack.Repository != repository {
			continue
		}
		nodes, listErr := service.store.ListTaskStackNodes(ctx, workspace, stack.StackID)
		if listErr != nil {
			return false, fmt.Errorf("list task stack %q nodes: %w", stack.StackID, listErr)
		}
		found := false
		for _, node := range nodes {
			if strings.TrimSpace(node.TaskID) == "" || strings.TrimSpace(node.TaskID) != node.TaskID {
				return false, fmt.Errorf("task stack %q returned empty task identity: %w", stack.StackID, ErrInvalidMaterialization)
			}
			found = found || node.TaskID == taskID
		}
		if !found {
			continue
		}
		if stackID != "" {
			return false, nil
		}
		stackID = stack.StackID
	}
	if stackID == "" {
		return false, nil
	}
	if err := service.store.UpdateTaskStackOutcome(ctx, workspace, stackID, taskID, mutation); err != nil {
		return false, fmt.Errorf("update task stack outcome: %w", err)
	}
	return true, nil
}

func taskOutcomeMutation(metadata map[string]string, now func() time.Time) (TaskStackOutcomeMutation, bool) {
	if metadata == nil {
		return TaskStackOutcomeMutation{}, false
	}
	outputSHA := firstTaskOutcomeValue(
		metadata["github_commit_sha"], metadata["github_head_sha"], metadata["head_sha"], metadata["output_sha"],
	)
	switch {
	case strings.TrimSpace(metadata["github_branch"]) != "" || metadata["delivery"] == "pull_request":
		publishedAt := now().UTC()
		return TaskStackOutcomeMutation{State: TaskOutcomePublished, OutputSHA: outputSHA, PublishedAt: &publishedAt}, true
	case metadata["delivery"] == "pull_request_skipped_no_changes" || metadata["files_changed"] == "0":
		return TaskStackOutcomeMutation{State: TaskOutcomeEmpty}, true
	default:
		return TaskStackOutcomeMutation{}, false
	}
}

func firstTaskOutcomeValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
