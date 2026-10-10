package review

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

// A dependent task starts on its predecessor's frozen revision before that
// revision is reviewed (Tyson, 2026-10-09). Its revisions report what the
// reviewer needs: an approval that waits for the predecessor's code to be
// applied first, and a base that went stale because the predecessor's
// revision was rejected, replaced or abandoned. Rebuild is never automatic.

// lineageReader is the journal's view of a change's local lineage. Stores
// without lineage (test fakes) skip the stale-approval check.
type lineageReader interface {
	DependentLineage(context.Context, string, string) (journal.LineageState, string, bool, error)
}

// refuseStaleApproval refuses to approve a dependent whose base is stale:
// its code was built on a predecessor revision that is no longer the one to
// build on. Override, a human's deliberate decision, is not refused.
func refuseStaleApproval(ctx context.Context, store Store, r loomgit.Revision, kind string) error {
	reader, ok := store.(lineageReader)
	if !ok || kind != "approve" {
		return nil
	}
	state, predecessor, found, err := reader.DependentLineage(ctx, r.Workspace, r.Change)
	if err != nil || !found || state.State == "current" {
		return err
	}
	return loomgit.NewError(loomgit.Stale, "approve is refused: "+state.Reason(predecessor), nil)
}

// addDependencyState fills in, on a task's newest source revision, its
// predecessor, its stale base, and an approval that waits for the
// predecessor's code.
func (l *Local) addDependencyState(ctx context.Context, workspace, lead, kind string, i *TaskRevision) error {
	if i.Superseded || kind != "source" {
		return nil
	}
	state, predecessor, found, err := l.store.DependentLineage(ctx, workspace, i.ChangeID)
	if err != nil || !found {
		return err
	}
	i.DependsOn = predecessor
	if state.State != "current" {
		i.LineageState, i.LineageReason, i.RebuildOn = state.State, state.Reason(predecessor), state.Available
		return nil
	}
	if i.FollowStatus != "approved" || i.Applied {
		return nil
	}
	leads, err := l.store.ApprovalLeads(ctx, workspace, i.ChangeID, i.Number)
	if err != nil {
		return err
	}
	for _, target := range leads {
		if lead != "" && target != lead {
			continue
		}
		applied, err := l.store.PredecessorApplied(ctx, workspace, target, state.Pinned.PredecessorChange)
		if err != nil || applied {
			if err != nil {
				return err
			}
			continue
		}
		i.FollowStatus = "waiting_for_dependency"
		i.FollowReason, err = l.waitingReason(ctx, workspace, predecessor, state.Pinned.PredecessorChange)
		return err
	}
	return nil
}

// DependencyWaitReason says what an approved dependent change waits for
// before it can apply; "" for a change built on no predecessor.
func (l *Local) DependencyWaitReason(ctx context.Context, workspace, change string) (string, error) {
	state, predecessor, found, err := l.store.DependentLineage(ctx, workspace, change)
	if err != nil || !found {
		return "", err
	}
	return l.waitingReason(ctx, workspace, predecessor, state.Pinned.PredecessorChange)
}

// waitingReason names what an approved dependent waits for: its
// predecessor's approval, or the apply of the predecessor's approved code.
func (l *Local) waitingReason(ctx context.Context, workspace, predecessor, change string) (string, error) {
	number, err := l.store.LatestSourceNumber(ctx, workspace, change)
	if err != nil {
		return "", err
	}
	verdict, err := l.store.LatestVerdict(ctx, loomgit.Revision{Workspace: workspace, Change: change, Number: number})
	if err != nil && !IsNotFound(err) {
		return "", err
	}
	switch verdict.Kind {
	case "approve", "override", "policy":
		return fmt.Sprintf("waiting for %s's approved code to be applied", predecessor), nil
	}
	return fmt.Sprintf("waiting for %s to be approved", predecessor), nil
}

// RebuildResult names the revision a rebuild set aside and the predecessor
// revision the dependent's next attempt builds on.
type RebuildResult struct {
	Change      string `json:"change_id"`
	Revision    int    `json:"revision"`
	DependsOn   string `json:"depends_on"`
	RebuildOn   int    `json:"rebuild_on"`
	VerdictKind string `json:"verdict"`
}

// Rebuild, a human action, sets a stale dependent's newest revision aside and rebuilds it on
// its predecessor's newer revision. It rejects that revision with the rebuild
// as the reason, so the task reopens for a new attempt; cancels any approval
// of it still waiting to apply; and drops the dependent's pin, so the attempt
// builds on the predecessor's newest revision.
func (l *Local) Rebuild(ctx context.Context, workspace, task string, actor Actor) (RebuildResult, error) {
	// A human decides to throw a dependent's code away, as with Override.
	if actor.Kind != "human" || actor.ID == "" {
		return RebuildResult{}, required("rebuild requires a human actor")
	}
	newest, err := l.newestSource(ctx, workspace, task)
	if err != nil {
		return RebuildResult{}, err
	}
	state, predecessor, err := l.rebuildable(ctx, newest)
	if err != nil {
		return RebuildResult{}, err
	}
	kind, err := l.setAside(ctx, newest, state.Reason(predecessor), fmt.Sprintf("rebuild on %s's new code", predecessor), actor)
	if err != nil {
		return RebuildResult{}, err
	}
	if err := l.store.ClearLocalLineage(ctx, workspace, task, state.Pinned.Repo); err != nil {
		return RebuildResult{}, err
	}
	return RebuildResult{Change: newest.Change, Revision: newest.Number, DependsOn: predecessor,
		RebuildOn: state.Available, VerdictKind: kind}, nil
}

// newestSource is the task's newest source revision.
func (l *Local) newestSource(ctx context.Context, workspace, task string) (loomgit.Revision, error) {
	revisions, err := l.store.ListTaskRevisions(ctx, workspace, task)
	if err != nil {
		return loomgit.Revision{}, err
	}
	for _, r := range revisions {
		if r.Kind == "source" {
			return r, nil
		}
	}
	return loomgit.Revision{}, loomgit.NewError(loomgit.LineageUnresolved, "task has no revision to rebuild", nil)
}

// rebuildable checks the revision is a stale dependent, not applied, whose
// predecessor has a newer revision to build on.
func (l *Local) rebuildable(ctx context.Context, newest loomgit.Revision) (journal.LineageState, string, error) {
	state, predecessor, found, err := l.store.DependentLineage(ctx, newest.Workspace, newest.Change)
	switch {
	case err != nil:
		return state, "", err
	case !found || state.State != "stale":
		return state, "", loomgit.NewError(loomgit.Conflict, "task is not built on a stale predecessor revision", nil)
	case state.Available == 0:
		return state, "", loomgit.NewError(loomgit.LineageUnresolved, state.Reason(predecessor), nil)
	}
	applied, err := l.store.RevisionApplied(ctx, newest.Workspace, "", newest.Change, newest.Number)
	if err == nil && applied {
		err = loomgit.NewError(loomgit.Conflict, "the revision is already applied; unapply it first", nil)
	}
	return state, predecessor, err
}

// setAside cancels the revision's approvals still waiting to apply and
// rejects it, so its task reopens; it returns "" when it was already rejected.
func (l *Local) setAside(ctx context.Context, r loomgit.Revision, spentReason, reason string, actor Actor) (string, error) {
	if err := l.store.SpendWaitingApprovals(ctx, r.Workspace, r.Change, r.Number, spentReason); err != nil {
		return "", err
	}
	latest, err := l.store.LatestVerdict(ctx, r)
	if err != nil && !IsNotFound(err) {
		return "", err
	}
	if err == nil && latest.Kind == "reject" {
		return "", nil // Already rejected: the task is open; only the pin moves.
	}
	_, err = Submit(ctx, l.store, r.Workspace, r.Change, r.Number, r.HeadSHA, "reject", reason, actor)
	return "reject", err
}
