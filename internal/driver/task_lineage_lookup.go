package driver

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

// StackLineageLookup adapts the stackstore + stacklineage domain into the
// TaskLineageLookup the worktree resolver consumes. It is the host-local bridge
// between "where a task sits in its stack" and "which branch its worktree is cut
// from" — the same lineage that drives the PR base now also drives the worktree
// base, so the two cannot diverge.
type StackLineageLookup struct {
	Store stackstore.Store
}

var _ TaskLineageLookup = StackLineageLookup{}

// BaseRefForTask finds the stack (scoped to repoName) that contains taskID and
// returns its lineage base branch. It fails OPEN (ok=false, nil) when no lineage
// applies — task not in a stack for this repo, or an ambiguous taskID that
// appears in more than one stack for the repo. An empty-diff/closed/not-yet-
// published predecessor does NOT fall open: BaseBranchSliding re-parents onto
// the nearest real ancestor branch (or the stack RootBase), so the worktree
// base still tracks lineage. It fails CLOSED (returns the error) on store I/O
// errors and on graph-integrity corruption (ErrMissingPredecessor/ErrCycle)
// so corruption is observable rather than silently masquerading as "no lineage"
// and rebasing onto the default branch. Repo scoping is fail-closed: a stack with an empty RepoName
// (only reachable via a hand-edited/legacy store; `loom stack init` requires
// --repo) never matches, so it cannot hijack an unrelated repo's base ref.
func (l StackLineageLookup) BaseRefForTask(ctx context.Context, workspaceKey, repoName, taskID string) (string, bool, error) {
	st, node, byTask, ok, err := findTaskStack(ctx, l.Store, workspaceKey, repoName, taskID)
	if err != nil || !ok {
		return "", false, err
	}
	// Stage 2: slide past empty/closed/branchless ancestors to the nearest real
	// OutputBranch (or RootBase) instead of falling open to the repo default
	// branch — decision (a). Still fails closed on graph-integrity corruption
	// (missing predecessor / cycle) so it stays observable.
	base, err := stacklineage.BaseBranchSliding(st, node, byTask)
	if err != nil {
		return "", false, err
	}
	return base, true, nil
}

// DefaultStackLineageLookup returns a lineage lookup backed by the per-user loom
// stack store, or nil when the loom directory cannot be resolved (in which case
// the resolver keeps its pre-stacking default-branch behavior). The nil return is
// an untyped nil interface, so `Lineage != nil` checks behave correctly.
func DefaultStackLineageLookup() TaskLineageLookup {
	store, err := stackstore.Default()
	if err != nil {
		return nil
	}
	return StackLineageLookup{Store: store}
}
