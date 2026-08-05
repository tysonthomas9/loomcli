package taskworktree

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

// Lineage is the per-task stack-lineage carrier stored in a TaskRun input.
type Lineage struct {
	StackID      string `json:"stackId,omitempty"`
	BaseRef      string `json:"baseRef,omitempty"`
	OutputBranch string `json:"outputBranch,omitempty"`
}

// Empty reports whether the carrier holds no lineage at all.
func (lineage Lineage) Empty() bool {
	return strings.TrimSpace(lineage.StackID) == "" &&
		strings.TrimSpace(lineage.BaseRef) == "" &&
		strings.TrimSpace(lineage.OutputBranch) == ""
}

type lineageEnvelope struct {
	Lineage *Lineage `json:"lineage,omitempty"`
}

// WithLineage merges lineage into the namespaced key of an existing TaskRun
// input, preserving every other key.
func WithLineage(input json.RawMessage, lineage Lineage) (json.RawMessage, error) {
	if lineage.Empty() {
		return input, nil
	}
	object := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(input))) > 0 {
		if err := json.Unmarshal(input, &object); err != nil {
			return input, nil
		}
	}
	encoded, err := json.Marshal(lineage)
	if err != nil {
		return nil, err
	}
	object["lineage"] = encoded
	merged, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	return merged, nil
}

// LineageFromInput extracts the lineage carrier from a TaskRun input.
func LineageFromInput(input json.RawMessage) (Lineage, bool) {
	if len(strings.TrimSpace(string(input))) == 0 {
		return Lineage{}, false
	}
	var envelope lineageEnvelope
	if err := json.Unmarshal(input, &envelope); err != nil || envelope.Lineage == nil {
		return Lineage{}, false
	}
	if envelope.Lineage.Empty() {
		return Lineage{}, false
	}
	return *envelope.Lineage, true
}

// StackLineageLookup adapts stackstore into worktree base-ref resolution.
type StackLineageLookup struct {
	Store stackstore.Store
}

// BaseRefForTask returns the lineage base branch for taskID, scoped to repoName.
func (lookup StackLineageLookup) BaseRefForTask(
	ctx context.Context,
	workspaceKey,
	repoName,
	taskID string,
) (string, bool, error) {
	stack, node, byTask, ok, err := findTaskStack(ctx, lookup.Store, workspaceKey, repoName, taskID)
	if err != nil || !ok {
		return "", false, err
	}
	base, err := stacklineage.BaseBranchSliding(stack, node, byTask)
	if err != nil {
		return "", false, err
	}
	return base, true, nil
}

// DefaultStackLineageLookup returns a lookup backed by the per-user Loom stack
// store, or nil when the Loom directory cannot be resolved.
func DefaultStackLineageLookup() *StackLineageLookup {
	value, err := stackstore.Default()
	if err != nil {
		return nil
	}
	return &StackLineageLookup{Store: value}
}

// DefaultStackStore returns the per-user Loom stack store, or nil when the Loom
// directory cannot be resolved.
func DefaultStackStore() stackstore.Store {
	value, err := stackstore.Default()
	if err != nil {
		return nil
	}
	return value
}

// BindingForTask returns the task's stack binding when the task belongs to a
// stack for repoName.
func BindingForTask(
	ctx context.Context,
	store stackstore.Store,
	workspaceKey,
	repoName,
	taskID string,
) (Lineage, bool, error) {
	stack, node, byTask, ok, err := findTaskStack(ctx, store, workspaceKey, repoName, taskID)
	if err != nil || !ok {
		return Lineage{}, false, err
	}
	base, err := stacklineage.BaseBranchSliding(stack, node, byTask)
	if err != nil {
		return Lineage{}, false, err
	}
	return Lineage{
		StackID:      string(stack.ID),
		BaseRef:      base,
		OutputBranch: node.OutputBranch,
	}, true, nil
}

// RecordOutcome maps runner evidence to a stack node state and persists it.
func RecordOutcome(
	ctx context.Context,
	store stackstore.Store,
	workspaceKey,
	repoName,
	taskID string,
	metadata map[string]string,
) (bool, error) {
	state, outputSHA, ok := stackOutcome(metadata)
	if !ok {
		return false, nil
	}
	return recordStackOutput(ctx, store, workspaceKey, repoName, taskID, state, outputSHA)
}

func recordStackOutput(
	ctx context.Context,
	store stackstore.Store,
	workspaceKey,
	repoName,
	taskID string,
	state stacklineage.NodeState,
	outputSHA string,
) (bool, error) {
	if store == nil || state == "" {
		return false, nil
	}
	stack, _, _, found, err := findTaskStack(ctx, store, workspaceKey, repoName, taskID)
	if err != nil || !found {
		return false, err
	}
	now := time.Now().UTC()
	if err := store.UpdateNode(ctx, workspaceKey, stack.ID, taskID, func(node *stacklineage.Node) error {
		node.State = state
		if strings.TrimSpace(outputSHA) != "" {
			node.OutputSHA = strings.TrimSpace(outputSHA)
		}
		if state == stacklineage.NodeStatePublished {
			node.LastPublishedAt = &now
		}
		return nil
	}); err != nil {
		return false, err
	}
	return true, nil
}

func findTaskStack(
	ctx context.Context,
	store stackstore.Store,
	workspaceKey,
	repoName,
	taskID string,
) (stacklineage.Stack, stacklineage.Node, map[string]stacklineage.Node, bool, error) {
	taskID = strings.TrimSpace(taskID)
	repoName = strings.TrimSpace(repoName)
	if store == nil || taskID == "" || repoName == "" {
		return stacklineage.Stack{}, stacklineage.Node{}, nil, false, nil
	}
	stacks, err := store.ListStacks(ctx, workspaceKey)
	if err != nil {
		return stacklineage.Stack{}, stacklineage.Node{}, nil, false, err
	}
	var (
		foundStack  stacklineage.Stack
		foundNode   stacklineage.Node
		foundByTask map[string]stacklineage.Node
		found       bool
	)
	for _, stack := range stacks {
		if strings.TrimSpace(stack.RepoName) == "" || stack.RepoName != repoName {
			continue
		}
		nodes, err := store.ListNodes(ctx, workspaceKey, stack.ID)
		if err != nil {
			return stacklineage.Stack{}, stacklineage.Node{}, nil, false, err
		}
		byTask := stacklineage.ByTask(nodes)
		node, ok := byTask[taskID]
		if !ok {
			continue
		}
		if found {
			return stacklineage.Stack{}, stacklineage.Node{}, nil, false, nil
		}
		foundStack, foundNode, foundByTask, found = stack, node, byTask, true
	}
	if !found {
		return stacklineage.Stack{}, stacklineage.Node{}, nil, false, nil
	}
	return foundStack, foundNode, foundByTask, true, nil
}

func stackOutcome(metadata map[string]string) (stacklineage.NodeState, string, bool) {
	if metadata == nil {
		return "", "", false
	}
	outputSHA := firstNonEmpty(
		metadata["github_commit_sha"],
		metadata["github_head_sha"],
		metadata["head_sha"],
		metadata["output_sha"],
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
