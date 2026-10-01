package publish

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/githubtoken"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/loomgit/stacklock"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

func ReconcileLoomMerges(ctx context.Context) error {
	return ReconcileLoomMergesAt(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"),
		stackpublish.NewConfiguredGitHubForge(githubtoken.GitHub(ctx)))
}

func ReconcileLoomMergesAt(ctx context.Context, path string, forge loomMergeForge) error {
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	merges, err := store.OpenLoomMerges(ctx)
	if err != nil {
		return err
	}
	for _, merge := range merges {
		if err := stacklock.With(ctx, merge.Workspace, merge.StackID, func(lockedCtx context.Context) error {
			current, err := store.LoomMerge(lockedCtx, merge.Workspace, merge.StackID)
			if err != nil || current.Phase == "done" || current.Phase == "blocked" {
				return err
			}
			return advanceLoomMerge(lockedCtx, store, forge, current)
		}); err != nil {
			return err
		}
	}
	return nil
}

func advanceLoomMerge(ctx context.Context, store *journal.SQLite, forge loomMergeForge, merge journal.LoomMerge) error {
	if merge.Index >= len(merge.Layers) {
		return blockLoomMerge(ctx, store, merge, loomgit.MergeBlocked, "merge cursor exceeds the stack")
	}
	publication, found, err := store.Publication(ctx, merge.Workspace, merge.Layers[merge.Index].Change)
	if err != nil {
		return err
	}
	if !found || publication.StackID != merge.StackID || publication.Phase != "done" {
		return blockLoomMerge(ctx, store, merge, loomgit.MergeBlocked, "merge publication is unavailable")
	}
	owner, repo, ok := strings.Cut(publication.Slug, "/")
	if !ok || owner == "" || repo == "" {
		return blockLoomMerge(ctx, store, merge, loomgit.MergeBlocked, "merge repository slug is invalid")
	}
	pr, err := forge.PullByNumber(ctx, owner, repo, publication.PRNumber)
	if err != nil {
		return err
	}
	if pr.Number != publication.PRNumber || pr.Head != publication.Branch {
		return blockLoomMerge(ctx, store, merge, loomgit.Stale, "merge PR identity changed")
	}
	if merge.Phase == "ready" || merge.Phase == "dispatching" || merge.Phase == "merging" {
		return reconcileLoomDispatch(ctx, store, forge, merge, publication, pr, owner, repo)
	}
	if merge.Phase == "landing" {
		return reconcileLoomLanding(ctx, store, merge, publication)
	}
	if merge.Phase == "restacking" {
		return reconcileLoomRestack(ctx, store, forge, merge, publication, owner, repo)
	}
	return blockLoomMerge(ctx, store, merge, loomgit.MergeBlocked, "unknown merge phase")
}

func reconcileLoomDispatch(ctx context.Context, store *journal.SQLite, forge loomMergeForge,
	merge journal.LoomMerge, publication journal.Publication, pr stackpublish.PR, owner, repo string) error {
	if pr.HeadSHA != publication.Head {
		if err := store.RecordPublicationDrift(ctx, publication, pr.HeadSHA); err != nil {
			return err
		}
		return blockLoomMerge(ctx, store, merge, loomgit.Stale, "merge PR head changed")
	}
	if err := requireConfirmedHead(ctx, store, merge, publication); err != nil {
		return blockConfirmedError(ctx, store, merge, err)
	}
	if pr.Merged {
		return setLoomPhase(ctx, store, merge, "landing", merge.Index, "")
	}
	if pr.State != "open" {
		return blockLoomMerge(ctx, store, merge, loomgit.MergeBlocked, "merge PR is not open")
	}
	if merge.DispatchHead != "" && (merge.DispatchHead != pr.HeadSHA || merge.PRNumber != pr.Number) {
		return blockLoomMerge(ctx, store, merge, loomgit.Stale, "dispatched merge identity changed")
	}
	if merge.ProviderRequestID != "" {
		return pollLoomMerge(ctx, store, forge, merge, publication, owner, repo)
	}
	ready, err := loomMergeHealth(ctx, forge, publication, pr, owner, repo)
	if err != nil {
		return blockConfirmedError(ctx, store, merge, err)
	}
	if !ready {
		return nil
	}
	return submitLoomMerge(ctx, store, forge, merge, pr, owner, repo)
}

func submitLoomMerge(ctx context.Context, store *journal.SQLite, forge loomMergeForge,
	merge journal.LoomMerge, pr stackpublish.PR, owner, repo string) error {
	queued, err := forge.QueuedPRNumbers(ctx, owner, repo)
	if err != nil {
		return err
	}
	if merge.Phase == "ready" {
		after := merge
		after.Phase, after.PRNumber, after.DispatchHead = "dispatching", pr.Number, pr.HeadSHA
		if err := store.AdvanceLoomMerge(ctx, merge, after); err != nil {
			return err
		}
		merge, err = store.LoomMerge(ctx, merge.Workspace, merge.StackID)
		if err != nil {
			return err
		}
	}
	if queued[pr.Number] {
		return nil
	}
	merge, err = recordLoomAttempt(ctx, store, merge)
	if err != nil {
		return err
	}
	result, err := forge.MergeLoomPull(ctx, owner, repo, pr.Number, pr.HeadSHA)
	if err != nil {
		var rejected *stackpublish.LoomMergeRejectedError
		if errors.As(err, &rejected) {
			return blockLoomMerge(ctx, store, merge, loomgit.MergeBlocked, err.Error())
		}
		return loomgit.NewError(loomgit.AttentionRequired, "merge submission outcome is unknown", err)
	}
	if result.Details.ExpectedHeadSHA != "" && result.Details.ExpectedHeadSHA != pr.HeadSHA {
		return blockLoomMerge(ctx, store, merge, loomgit.Stale, "provider accepted a different merge head")
	}
	if result.Status == "merged" {
		return setLoomPhase(ctx, store, merge, "landing", merge.Index, "")
	}
	if result.Status == "failed" {
		return blockLoomMerge(ctx, store, merge, loomgit.MergeBlocked, "provider merge failed: "+result.Details.Message)
	}
	after := merge
	after.Phase, after.ProviderRequestID = "merging", result.Details.UUID
	return store.AdvanceLoomMerge(ctx, merge, after)
}

func recordLoomAttempt(ctx context.Context, store *journal.SQLite, merge journal.LoomMerge) (journal.LoomMerge, error) {
	if merge.DispatchAttempts >= 2 {
		return merge, loomgit.NewError(loomgit.AttentionRequired, "merge outcome remains unknown after pinned recovery", nil)
	}
	after := merge
	after.DispatchAttempts++
	if err := store.AdvanceLoomMerge(ctx, merge, after); err != nil {
		return merge, err
	}
	return store.LoomMerge(ctx, merge.Workspace, merge.StackID)
}

func pollLoomMerge(ctx context.Context, store *journal.SQLite, forge loomMergeForge,
	merge journal.LoomMerge, publication journal.Publication, owner, repo string) error {
	result, err := forge.LoomMergeStatus(ctx, owner, repo, publication.PRNumber, merge.ProviderRequestID)
	if err != nil {
		return err
	}
	if result.Details.ExpectedHeadSHA != "" && result.Details.ExpectedHeadSHA != merge.DispatchHead {
		return blockLoomMerge(ctx, store, merge, loomgit.Stale, "provider merge head changed")
	}
	if result.Details.BypassRules {
		return blockLoomMerge(ctx, store, merge, loomgit.Protected, "provider merge bypassed repository rules")
	}
	if result.Status == "pending" || result.Status == "enqueued" || result.Status == "merged" {
		return nil
	}
	return blockLoomMerge(ctx, store, merge, loomgit.MergeBlocked, "provider merge failed: "+result.Details.Message)
}

func loomMergeHealth(ctx context.Context, forge loomMergeForge, publication journal.Publication,
	pr stackpublish.PR, owner, repo string) (bool, error) {
	statuses, err := forge.PRStatuses(ctx, owner, repo, publication.Branch)
	if err != nil {
		return false, err
	}
	status, found := statuses[publication.Branch]
	if !found || status.Number != pr.Number {
		return false, loomgit.NewError(loomgit.MergeBlocked, "required PR status is unavailable", nil)
	}
	if status.Checks == "failing" || status.Review == "changes_requested" || status.Mergeable == "conflicting" {
		return false, loomMergeFailure(ctx, forge, owner, repo, pr)
	}
	return (status.Checks == "passing" || status.Checks == "none") && status.Mergeable == "mergeable", nil
}

func loomMergeFailure(ctx context.Context, forge loomMergeForge, owner, repo string, pr stackpublish.PR) error {
	names, err := forge.FailedLoomChecks(ctx, owner, repo, pr.HeadSHA)
	message := fmt.Sprintf("PR %d checks or review failed", pr.Number)
	if err != nil {
		return loomgit.NewError(loomgit.MergeBlocked, message+"; check details unavailable", err)
	}
	if len(names) > 0 {
		message += ": " + strings.Join(names, ", ")
	}
	return loomgit.NewError(loomgit.MergeBlocked, message, nil)
}

func reconcileLoomLanding(ctx context.Context, store *journal.SQLite, merge journal.LoomMerge,
	publication journal.Publication) error {
	status, err := store.LandingStatus(ctx, merge.Workspace, publication.Change)
	if err != nil {
		return err
	}
	if status.State != "landed" {
		return nil
	}
	return setLoomPhase(ctx, store, merge, "restacking", merge.Index, "")
}

func reconcileLoomRestack(ctx context.Context, store *journal.SQLite, forge loomMergeForge,
	merge journal.LoomMerge, publication journal.Publication, owner, repo string) error {
	if merge.Index+1 < len(merge.Layers) {
		ready, err := loomNextLayerReady(ctx, store, forge, merge, publication, owner, repo)
		if err != nil {
			var coded *loomgit.Error
			if errors.As(err, &coded) {
				return blockLoomMerge(ctx, store, merge, coded.Kind, err.Error())
			}
			return err
		}
		if !ready {
			return nil
		}
	}
	for _, layer := range merge.Layers[merge.Index+1:] {
		dependent, found, err := store.Publication(ctx, merge.Workspace, layer.Change)
		if err != nil || !found {
			return errors.Join(err, fmt.Errorf("dependent publication %s is unavailable", layer.Change))
		}
		pr, err := forge.PullByNumber(ctx, owner, repo, dependent.PRNumber)
		if err != nil {
			return err
		}
		if pr.State == "open" && pr.Base == publication.Branch {
			return blockLoomMerge(ctx, store, merge, loomgit.MergeBlocked, "open PR still targets merged branch")
		}
	}
	if err := forge.DeleteLoomBranch(ctx, owner, repo, publication.Branch); err != nil {
		return blockLoomMerge(ctx, store, merge, loomgit.MergeBlocked, err.Error())
	}
	if publication.Change == merge.Target {
		return setLoomPhase(ctx, store, merge, "done", merge.Index, "")
	}
	return setLoomPhase(ctx, store, merge, "ready", merge.Index+1, "")
}

func loomNextLayerReady(ctx context.Context, store *journal.SQLite, forge loomMergeForge,
	merge journal.LoomMerge, landed journal.Publication, owner, repo string) (bool, error) {
	if err := checkLoomRestackState(ctx, store, merge); err != nil {
		return false, err
	}
	next := merge.Layers[merge.Index+1]
	publication, found, err := store.Publication(ctx, merge.Workspace, next.Change)
	if err != nil {
		return false, err
	}
	if !found {
		return false, loomgit.NewError(loomgit.MergeBlocked, "next merge publication is missing", nil)
	}
	if publication.StackID != merge.StackID || publication.Phase != "done" {
		return false, loomgit.NewError(loomgit.MergeBlocked, "restacked publication is unavailable", nil)
	}
	pr, err := forge.PullByNumber(ctx, owner, repo, publication.PRNumber)
	if err != nil {
		return false, err
	}
	if pr.HeadSHA != publication.Head {
		if err := store.RecordPublicationDrift(ctx, publication, pr.HeadSHA); err != nil {
			return false, err
		}
		return false, loomgit.NewError(loomgit.Stale, "restacked PR head changed", nil)
	}
	if pr.State != "open" || pr.Merged {
		return false, loomgit.NewError(loomgit.MergeBlocked, "next PR closed during restack", nil)
	}
	if publication.Trunk != landed.Trunk {
		return false, nil
	}
	if publication.Head == next.Head {
		return false, nil
	}
	if pr.Base != landed.Trunk {
		return false, loomgit.NewError(loomgit.Stale, "restacked PR changed or was not retargeted", nil)
	}
	ready, err := loomNextChecks(ctx, forge, publication, pr, owner, repo)
	if err != nil || !ready {
		return ready, err
	}
	return true, confirmNextHeadAfterChecks(ctx, store, forge, merge, publication, owner, repo, landed.Trunk)
}

func loomNextChecks(ctx context.Context, forge loomMergeForge, publication journal.Publication,
	pr stackpublish.PR, owner, repo string) (bool, error) {
	statuses, err := forge.PRStatuses(ctx, owner, repo, publication.Branch)
	if err != nil {
		return false, err
	}
	status, found := statuses[publication.Branch]
	if status.Checks == "failing" {
		return false, loomMergeFailure(ctx, forge, owner, repo, pr)
	}
	if !found || status.Number != pr.Number {
		return false, loomgit.NewError(loomgit.MergeBlocked, fmt.Sprintf("PR %d required checks are unavailable", pr.Number), nil)
	}
	return status.Checks == "passing" || status.Checks == "none", nil
}

func confirmNextHeadAfterChecks(ctx context.Context, store *journal.SQLite, forge loomMergeForge,
	merge journal.LoomMerge, publication journal.Publication, owner, repo, expectedBase string) error {
	pr, err := forge.PullByNumber(ctx, owner, repo, publication.PRNumber)
	if err != nil {
		return err
	}
	if pr.HeadSHA != publication.Head || pr.State != "open" || pr.Merged || pr.Base != expectedBase {
		if pr.HeadSHA != publication.Head {
			if err := store.RecordPublicationDrift(ctx, publication, pr.HeadSHA); err != nil {
				return err
			}
		}
		return loomgit.NewError(loomgit.Stale, "next PR changed after required checks", nil)
	}
	return requireConfirmedHead(ctx, store, merge, publication)
}

func checkLoomRestackState(ctx context.Context, store *journal.SQLite, merge journal.LoomMerge) error {
	state, err := store.StackState(ctx, merge.Workspace, merge.StackID)
	if err != nil {
		return err
	}
	if state.Status == "review_required" {
		return loomgit.NewError(loomgit.ReviewRequired, "restacked layer needs a new verdict", nil)
	}
	if state.Status == "restack_conflict" || state.Status == "swap_held" {
		return loomgit.NewError(loomgit.MergeBlocked, "restack requires attention: "+state.Status, nil)
	}
	return nil
}

func requireConfirmedHead(ctx context.Context, store *journal.SQLite, merge journal.LoomMerge,
	publication journal.Publication) error {
	var original journal.LoomMergeLayer
	for _, layer := range merge.Layers {
		if layer.Change == publication.Change {
			original = layer
			break
		}
	}
	if original.Change == "" {
		return loomgit.NewError(loomgit.Stale, "merge layer is outside confirmation", nil)
	}
	revision, err := store.RevisionByHead(ctx, merge.Workspace, original.Change, publication.Head)
	if err != nil {
		return loomgit.NewError(loomgit.Stale, "merge head has no recorded revision", err)
	}
	if err := review.RequireVerdict(ctx, store, merge.Workspace, original.Change, revision.Number, revision.HeadSHA, "publish", ""); err != nil {
		return err
	}
	for revision.Number != original.Revision {
		if revision.DerivedFromChange != original.Change || revision.DerivedFromNumber < original.Revision || revision.DerivedFromNumber >= revision.Number {
			return loomgit.NewError(loomgit.Stale, "merge head was not derived from confirmation", nil)
		}
		revision, err = store.GetRevision(ctx, merge.Workspace, original.Change, revision.DerivedFromNumber)
		if err != nil {
			return err
		}
	}
	if revision.HeadSHA != original.Head {
		return loomgit.NewError(loomgit.Stale, "confirmed revision changed", nil)
	}
	return nil
}

func setLoomPhase(ctx context.Context, store *journal.SQLite, before journal.LoomMerge, phase string, index int, reason string) error {
	after := before
	after.Phase, after.Index, after.Reason = phase, index, reason
	if index != before.Index {
		after.PRNumber, after.DispatchHead, after.ProviderRequestID, after.DispatchAttempts = 0, "", "", 0
	}
	return store.AdvanceLoomMerge(ctx, before, after)
}

func blockLoomMerge(ctx context.Context, store *journal.SQLite, merge journal.LoomMerge, kind loomgit.Code, reason string) error {
	if err := setLoomPhase(ctx, store, merge, "blocked", merge.Index, reason); err != nil {
		return err
	}
	return loomgit.NewError(kind, reason, nil)
}

func blockConfirmedError(ctx context.Context, store *journal.SQLite, merge journal.LoomMerge, cause error) error {
	var coded *loomgit.Error
	if errors.As(cause, &coded) {
		return blockLoomMerge(ctx, store, merge, coded.Kind, cause.Error())
	}
	return cause
}
