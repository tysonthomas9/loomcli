package sourcecontrolcomposition

import (
	"context"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

var _ sourcecontrol.TaskOutcomeRecorder = (*SourceControlCapability)(nil)

func (capability *SourceControlCapability) RecordTaskOutcome(
	ctx context.Context,
	command sourcecontrol.TaskOutcomeCommand,
) (bool, error) {
	state, outputSHA, ok := sourceControlStackOutcome(command.Metadata)
	if !ok || capability == nil || capability.lineage == nil {
		return false, nil
	}
	stack, found, err := findSourceControlTaskStack(
		ctx, capability.lineage, command.WorkspaceKey, command.Repository, command.TaskID,
	)
	if err != nil || !found {
		return false, err
	}
	now := time.Now().UTC()
	err = capability.lineage.UpdateNode(ctx, command.WorkspaceKey, stack.ID, command.TaskID, func(node *stacklineage.Node) error {
		node.State = state
		if outputSHA != "" {
			node.OutputSHA = outputSHA
		}
		if state == stacklineage.NodeStatePublished {
			node.LastPublishedAt = &now
		}
		return nil
	})
	return err == nil, err
}

func findSourceControlTaskStack(
	ctx context.Context,
	store stackstore.Store,
	workspaceKey, repository, taskID string,
) (stacklineage.Stack, bool, error) {
	repository = strings.TrimSpace(repository)
	taskID = strings.TrimSpace(taskID)
	if store == nil || repository == "" || taskID == "" {
		return stacklineage.Stack{}, false, nil
	}
	stacks, err := store.ListStacks(ctx, workspaceKey)
	if err != nil {
		return stacklineage.Stack{}, false, err
	}
	var found *stacklineage.Stack
	for index := range stacks {
		stack := stacks[index]
		if stack.RepoName != repository {
			continue
		}
		nodes, err := store.ListNodes(ctx, workspaceKey, stack.ID)
		if err != nil {
			return stacklineage.Stack{}, false, err
		}
		if _, ok := stacklineage.ByTask(nodes)[taskID]; !ok {
			continue
		}
		if found != nil {
			return stacklineage.Stack{}, false, nil
		}
		copy := stack
		found = &copy
	}
	if found == nil {
		return stacklineage.Stack{}, false, nil
	}
	return *found, true, nil
}

func sourceControlStackOutcome(metadata map[string]string) (stacklineage.NodeState, string, bool) {
	if metadata == nil {
		return "", "", false
	}
	outputSHA := firstSourceControlValue(
		metadata["github_commit_sha"], metadata["github_head_sha"], metadata["head_sha"], metadata["output_sha"],
	)
	switch {
	case strings.TrimSpace(metadata["github_branch"]) != "" || metadata["delivery"] == "pull_request":
		return stacklineage.NodeStatePublished, outputSHA, true
	case metadata["delivery"] == "pull_request_skipped_no_changes" || metadata["files_changed"] == "0":
		return stacklineage.NodeStateEmpty, "", true
	default:
		return "", "", false
	}
}

func firstSourceControlValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
