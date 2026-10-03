package publish

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/mirror"
)

// FeedbackMergeCancelReason is why a review fix-up cancels a pending Approve
// and merge: the code on the PR changed, so merging needs a new Approve.
const FeedbackMergeCancelReason = "the PR changed with review fix-ups; approve it again to merge"

// Feedback update states shown on a fix-up revision (D29 (6)).
const (
	FeedbackPushing   = "pushing"
	FeedbackPushed    = "pushed"
	FeedbackHeld      = "held"
	FeedbackNotPushed = "not_pushed"
	FeedbackReplaced  = "superseded"
)

// followFeedbackLeads applies recorded fix-ups in their leads' working areas;
// tests replace it.
var followFeedbackLeads = func(ctx context.Context, store *journal.SQLite, leads map[[2]string]bool) error {
	return apply.RecoverPendingExcept(ctx, store, func(workspace, lead string) bool {
		return !leads[[2]string{workspace, lead}]
	})
}

// ReconcileFeedbackUpdates pushes review fix-ups to their open PRs without an
// Approve (D29 (6)).
func ReconcileFeedbackUpdates(ctx context.Context) error {
	return ReconcileFeedbackUpdatesAt(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"))
}

// ReconcileFeedbackUpdatesAt finds each new source revision of a change whose
// PR is open that nobody has decided on, and either records why it is not
// pushed (an incomplete capture or a secret-pattern path, D18) or cancels the
// change's pending Approve and merge and records Loom's feedback verdict, the
// follow that replaces the change's layer and the intent to push it. It then
// applies those fix-ups and tells the lead about any that are held. The
// approval reconciler pushes applied fix-ups to their PRs. Every step is
// recorded first, so an interrupted pass resumes on the next one.
func ReconcileFeedbackUpdatesAt(ctx context.Context, path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	return reconcileFeedbackUpdates(ctx, store)
}

func reconcileFeedbackUpdates(ctx context.Context, store *journal.SQLite) error {
	candidates, err := store.FixupCandidates(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, candidate := range candidates {
		if err := considerFixup(ctx, store, candidate); err != nil {
			failures = append(failures, fmt.Errorf("fix-up %s revision %d: %w", candidate.Change, candidate.Revision, err))
		}
	}
	updates, err := store.PushingFeedbackUpdates(ctx)
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	leads := make(map[[2]string]bool)
	for _, update := range updates {
		state, found, err := store.FeedbackUpdateFor(ctx, update.Workspace, update.Change, update.Revision)
		if err != nil || !found {
			failures = append(failures, err)
			continue
		}
		if state.FollowStatus != "applied" && state.FollowStatus != "superseded" && state.FollowStatus != "spent" {
			leads[[2]string{update.Workspace, update.Lead}] = true
		}
	}
	if len(leads) > 0 {
		failures = append(failures, followFeedbackLeads(ctx, store, leads))
	}
	for _, update := range updates {
		failures = append(failures, settleFeedbackUpdate(ctx, store, update))
	}
	return errors.Join(failures...)
}

// considerFixup turns one candidate into a pushing or not-pushed update.
func considerFixup(ctx context.Context, store *journal.SQLite, candidate journal.FixupCandidate) error {
	update := journal.FeedbackUpdate{Workspace: candidate.Workspace, Change: candidate.Change, Revision: candidate.Revision}
	approval, lead, found, err := store.PublishedApproval(ctx, candidate.Workspace, candidate.Change)
	if err != nil {
		return err
	}
	update.Lead = lead
	if !found {
		update.Reason = "its PR was not opened from an approval Loom can carry over; review it"
		return store.RecordFeedbackNotPushed(ctx, update)
	}
	if candidate.Incomplete {
		update.Reason = "the revision's capture is incomplete"
		return store.RecordFeedbackNotPushed(ctx, update)
	}
	secret, err := fixupSecretPath(ctx, store, candidate, lead)
	if err != nil {
		return err
	}
	if secret != "" {
		update.Reason = "it adds the secret-pattern path " + secret
		return store.RecordFeedbackNotPushed(ctx, update)
	}
	cancelled, err := cancelMergeForFixup(ctx, store, candidate)
	if err != nil {
		return err
	}
	update.VerdictID, update.MergeCancelled = approval, cancelled
	_, err = store.RecordFeedbackUpdate(ctx, update, candidate.HeadSHA)
	if errors.Is(err, journal.ErrStale) {
		// A newer revision arrived, or someone decided on this one first.
		return nil
	}
	return err
}

// cancelMergeForFixup cancels the change's pending "merge after #N" (or a
// blocked Approve and merge), because the code it approved is changing. A
// merge the backend has already started cannot be cancelled: the fix-up waits
// for the next pass. It also reports a cancel an interrupted pass made.
func cancelMergeForFixup(ctx context.Context, store *journal.SQLite, candidate journal.FixupCandidate) (bool, error) {
	cancelled, err := CancelMergeApproval(ctx, store, candidate.Workspace, candidate.Change, FeedbackMergeCancelReason)
	if err != nil || cancelled {
		return cancelled, err
	}
	approval, found, err := store.MergeApproval(ctx, candidate.Workspace, candidate.Change)
	if err != nil || !found {
		return false, err
	}
	return approval.Status == MergeApprovalCancelled && approval.Reason == FeedbackMergeCancelReason, nil
}

// fixupSecretPath returns a secret-pattern path the fix-up adds, read from the
// lead's working area for the change's repository.
func fixupSecretPath(ctx context.Context, store *journal.SQLite, candidate journal.FixupCandidate, lead string) (string, error) {
	repoName, err := store.RepoForChange(ctx, candidate.Workspace, candidate.Change)
	if err != nil {
		return "", err
	}
	area, err := workingArea(ctx, store, candidate.Workspace, lead, repoName)
	if err != nil {
		return "", err
	}
	runner, err := gitexec.New(area.Path, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return "", err
	}
	return mirror.SecretPathAdded(ctx, runner, candidate.BaseSHA, candidate.HeadSHA)
}

// settleFeedbackUpdate tells the lead once when a fix-up is held, and settles
// it once its PR has it, a newer fix-up replaced it, or its push gave up.
func settleFeedbackUpdate(ctx context.Context, store *journal.SQLite, update journal.FeedbackUpdate) error {
	state, found, err := store.FeedbackUpdateFor(ctx, update.Workspace, update.Change, update.Revision)
	if err != nil || !found {
		return err
	}
	switch {
	case state.FollowStatus == "conflict" || state.FollowStatus == "apply_pending":
		_, err = store.NoteFeedbackHeld(ctx, update, state.FollowStatus, state.Paths)
		return err
	case state.FollowStatus == "superseded" || state.PublishStatus == "superseded":
		return store.FinishFeedbackUpdate(ctx, update, FeedbackReplaced, "")
	case state.FollowStatus == "spent":
		return store.FinishFeedbackUpdate(ctx, update, FeedbackNotPushed, "it could not be applied in place")
	case state.PublishStatus == "published":
		return store.FinishFeedbackUpdate(ctx, update, FeedbackPushed, "")
	case state.PublishStatus == "not_published":
		return store.FinishFeedbackUpdate(ctx, update, FeedbackNotPushed, state.PublishReason)
	}
	return nil
}
