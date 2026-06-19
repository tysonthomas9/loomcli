package driver

import (
	"context"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

// DefaultStackStore returns the per-user loom stack store, or nil when the loom
// directory cannot be resolved (in which case the finalize barrier is inert).
// The nil return is an untyped nil interface so `StackStore != nil` checks work.
func DefaultStackStore() stackstore.Store {
	store, err := stackstore.Default()
	if err != nil {
		return nil
	}
	return store
}

// findTaskStack locates the single stack (scoped to repoName) that contains
// taskID and returns its stack, the task's node, and the by-task index. ok=false
// means no lineage applies — the task is in no stack for this repo, or it is
// ambiguous (present in more than one stack for the repo, so we refuse to guess).
// It is the shared scoping primitive behind both the worktree resolver's base
// lookup and the finalize barrier, so the two never disagree on which stack owns
// a task. A stack with an empty RepoName (only reachable via a hand-edited/legacy
// store) never matches — repo scoping is fail-closed.
func findTaskStack(ctx context.Context, store stackstore.Store, workspaceKey, repoName, taskID string) (stacklineage.Stack, stacklineage.Node, map[string]stacklineage.Node, bool, error) {
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
	for _, st := range stacks {
		if strings.TrimSpace(st.RepoName) == "" || st.RepoName != repoName {
			continue
		}
		nodes, err := store.ListNodes(ctx, workspaceKey, st.ID)
		if err != nil {
			return stacklineage.Stack{}, stacklineage.Node{}, nil, false, err
		}
		byTask := stacklineage.ByTask(nodes)
		node, ok := byTask[taskID]
		if !ok {
			continue
		}
		if found {
			// Ambiguous: taskID is in more than one stack for this repo.
			return stacklineage.Stack{}, stacklineage.Node{}, nil, false, nil
		}
		foundStack, foundNode, foundByTask, found = st, node, byTask, true
	}
	if !found {
		return stacklineage.Stack{}, stacklineage.Node{}, nil, false, nil
	}
	return foundStack, foundNode, foundByTask, true, nil
}

// stackBindingForTask returns the task's stack binding — the stack id, the
// canonical OutputBranch the runner must push to, and the sliding base ref the
// worktree is cut from — when the task belongs to a stack for repoName. ok=false
// means the task is not stacked (the runner keeps its non-stacked behavior). It
// fails closed on graph-integrity corruption so a bad graph is observable.
func stackBindingForTask(ctx context.Context, store stackstore.Store, workspaceKey, repoName, taskID string) (TaskLineage, bool, error) {
	st, node, byTask, ok, err := findTaskStack(ctx, store, workspaceKey, repoName, taskID)
	if err != nil || !ok {
		return TaskLineage{}, false, err
	}
	base, err := stacklineage.BaseBranchSliding(st, node, byTask)
	if err != nil {
		return TaskLineage{}, false, err
	}
	return TaskLineage{
		StackID:      string(st.ID),
		BaseRef:      base,
		OutputBranch: node.OutputBranch,
	}, true, nil
}

// stackOutcome maps a finished task's runtime_metadata to the stack-node state
// the finalize barrier should record. The second return is the output commit
// SHA when the runner reported one (best-effort — the local runner does not emit
// one today, so it is usually empty and the Stage-4 reconcile resolves it from
// the pushed branch). ok=false means the metadata is not a stacked-PR outcome
// (e.g. patch-back), so the barrier should leave the node's state untouched.
func stackOutcome(meta map[string]string) (state stacklineage.NodeState, outputSHA string, ok bool) {
	if meta == nil {
		return "", "", false
	}
	sha := firstNonEmpty(meta["github_commit_sha"], meta["github_head_sha"], meta["head_sha"], meta["output_sha"])
	switch {
	case strings.TrimSpace(meta["github_branch"]) != "" || meta["delivery"] == "pull_request":
		return stacklineage.NodeStatePublished, sha, true
	case meta["delivery"] == "pull_request_skipped_no_changes" || meta["files_changed"] == "0":
		return stacklineage.NodeStateEmpty, "", true
	default:
		return "", "", false
	}
}

// recordStackOutput is the finalize barrier: it records the task's node state
// (and output SHA, when known) in the stack store BEFORE the dependency edge
// unblocks successors, so a dependent task's resolver reads a node that is
// guaranteed durable. It never reassigns OutputBranch — that branch is stable
// from registration. A task not in any stack for repoName is a no-op
// (recorded=false). It is best-effort at the call site: the caller logs a
// non-nil error rather than failing the task run.
func recordStackOutput(ctx context.Context, store stackstore.Store, workspaceKey, repoName, taskID string, state stacklineage.NodeState, outputSHA string) (recorded bool, err error) {
	if store == nil || state == "" {
		return false, nil
	}
	st, _, _, ok, err := findTaskStack(ctx, store, workspaceKey, repoName, taskID)
	if err != nil || !ok {
		return false, err
	}
	now := time.Now().UTC()
	updateErr := store.UpdateNode(ctx, workspaceKey, st.ID, taskID, func(n *stacklineage.Node) error {
		n.State = state
		if strings.TrimSpace(outputSHA) != "" {
			n.OutputSHA = strings.TrimSpace(outputSHA)
		}
		if state == stacklineage.NodeStatePublished {
			n.LastPublishedAt = &now
		}
		return nil
	})
	if updateErr != nil {
		return false, updateErr
	}
	return true, nil
}
