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

// addDependencyState fills in a dependent revision's predecessor, its stale
// base, and an approval that waits for the predecessor's code.
func (l *Local) addDependencyState(ctx context.Context, workspace, lead string, i *TaskRevision) error {
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
	revisions, err := l.store.ListTaskRevisions(ctx, workspace, task)
	if err != nil {
		return RebuildResult{}, err
	}
	var newest loomgit.Revision
	for _, r := range revisions {
		if r.Kind == "source" {
			newest = r
			break
		}
	}
	if newest.Change == "" {
		return RebuildResult{}, loomgit.NewError(loomgit.LineageUnresolved, "task has no revision to rebuild", nil)
	}
	state, predecessor, found, err := l.store.DependentLineage(ctx, workspace, newest.Change)
	if err != nil {
		return RebuildResult{}, err
	}
	if !found || state.State != "stale" {
		return RebuildResult{}, loomgit.NewError(loomgit.Conflict, "task is not built on a stale predecessor revision", nil)
	}
	if state.Available == 0 {
		return RebuildResult{}, loomgit.NewError(loomgit.LineageUnresolved, state.Reason(predecessor), nil)
	}
	if applied, err := l.store.RevisionApplied(ctx, workspace, "", newest.Change, newest.Number); err != nil || applied {
		if err != nil {
			return RebuildResult{}, err
		}
		return RebuildResult{}, loomgit.NewError(loomgit.Conflict, "the revision is already applied; unapply it first", nil)
	}
	reason := fmt.Sprintf("rebuild on %s's new code", predecessor)
	if err := l.store.SpendWaitingApprovals(ctx, workspace, newest.Change, newest.Number, state.Reason(predecessor)); err != nil {
		return RebuildResult{}, err
	}
	kind := "reject"
	if latest, err := l.store.LatestVerdict(ctx, newest); err == nil && latest.Kind == "reject" {
		kind = "" // Already rejected: the task is open; only the pin moves.
	} else if err != nil && !IsNotFound(err) {
		return RebuildResult{}, err
	}
	if kind != "" {
		if _, err := Submit(ctx, l.store, workspace, newest.Change, newest.Number, newest.HeadSHA, kind, reason, actor); err != nil {
			return RebuildResult{}, err
		}
	}
	if err := l.store.ClearLocalLineage(ctx, workspace, task, state.Pinned.Repo); err != nil {
		return RebuildResult{}, err
	}
	return RebuildResult{Change: newest.Change, Revision: newest.Number, DependsOn: predecessor,
		RebuildOn: state.Available, VerdictKind: kind}, nil
}
