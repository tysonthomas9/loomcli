package publish

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

// ReconcileMergeApprovalsAt advances every open Approve and merge in the
// journal at path. One approval's failure does not hold back the others.
func ReconcileMergeApprovalsAt(ctx context.Context, path string, forge mergeApprovalForge) error {
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
	approvals, err := store.OpenMergeApprovals(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, approval := range approvals {
		if err := advanceMergeApproval(ctx, store, forge, approval); err != nil {
			failures = append(failures, fmt.Errorf("merge approval %s/%s: %w", approval.Workspace, approval.Change, err))
		}
	}
	return errors.Join(failures...)
}

func advanceMergeApproval(ctx context.Context, store *journal.SQLite, forge mergeApprovalForge, approval journal.MergeApproval) error {
	landed, err := store.IsLanded(ctx, approval.Workspace, approval.Change)
	if err != nil {
		return err
	}
	if landed {
		return setMergeApproval(ctx, store, approval, MergeApprovalMerged, "")
	}
	switch approval.Status {
	case MergeApprovalWaiting, MergeApprovalBlocked:
		if approval.Attention {
			// A human must look at the PR; Cancel auto-merge clears it.
			return nil
		}
		return tryMergeApproval(ctx, store, forge, approval)
	case MergeApprovalMerging:
		return followMergeApproval(ctx, store, forge, approval)
	}
	return nil
}

// tryMergeApproval merges an approved PR once it is the bottom of its stack,
// still at the approved head, and green on the provider.
func tryMergeApproval(ctx context.Context, store *journal.SQLite, forge mergeApprovalForge, approval journal.MergeApproval) error {
	publication, found, err := store.Publication(ctx, approval.Workspace, approval.Change)
	if err != nil {
		return err
	}
	if !found || publication.Phase != "done" || publication.PRNumber == 0 {
		return setMergeApproval(ctx, store, approval, MergeApprovalWaiting, "waiting for the PR to open")
	}
	approval, status, reason, err := carryApprovalHead(ctx, store, approval, publication)
	if err != nil || status != "" {
		return errors.Join(err, setMergeApproval(ctx, store, approval, status, reason))
	}
	if status, reason, err = mergeReadiness(ctx, store, forge, approval, publication); err != nil || status != "" {
		return errors.Join(err, setMergeApproval(ctx, store, approval, status, reason))
	}
	after := approval
	after.Status, after.Reason, after.Attempt = MergeApprovalMerging, "", approval.Attempt+1
	after.MergeRequestID = fmt.Sprintf("approval-merge:%s:%s:%d", approval.Workspace, approval.Change, after.Attempt)
	after.DispatchHead, after.ProviderRequestID, after.DispatchAttempts = "", "", 0
	if err := store.AdvanceMergeApproval(ctx, approval, after); err != nil {
		if errors.Is(err, journal.ErrStale) {
			// Cancelled or advanced by another writer first.
			return nil
		}
		return err
	}
	current, _, err := store.MergeApproval(ctx, approval.Workspace, approval.Change)
	if err != nil {
		return err
	}
	return followMergeApproval(ctx, store, forge, current)
}

// carryApprovalHead follows a rebuilt layer (D26): the approval moves to the
// change's newest revision only if that revision's verdict was carried from
// the approved head by a clean, patch-equivalent replay. Otherwise the task
// asks for approval again, as it does when the rebuild conflicted. A rebuild
// still in progress, or one holding working-area edits, waits.
func carryApprovalHead(ctx context.Context, store *journal.SQLite, approval journal.MergeApproval,
	publication journal.Publication) (journal.MergeApproval, string, string, error) {
	if status, reason, err := restackPending(ctx, store, approval, publication); err != nil || status != "" {
		return approval, status, reason, err
	}
	newest, err := newestRevision(ctx, store, approval.Workspace, approval.Change)
	if err != nil || newest.HeadSHA == approval.Head {
		return approval, "", "", err
	}
	approved, err := store.RevisionByHead(ctx, approval.Workspace, approval.Change, approval.Head)
	if err != nil {
		return approval, "", "", err
	}
	if newest.Number < approved.Number {
		return approval, "", "", nil
	}
	carried, err := verdictCarriedFrom(ctx, store, newest, approval.Head)
	if err != nil {
		return approval, "", "", err
	}
	if !carried {
		reason := rebuildNotCleanReason
		if newest.Kind == "source" {
			// A review fix-up is decided by its feedback update first, which
			// cancels this approval when it pushes the fix-up (D29 (6)).
			if unsettled, err := unsettledFixup(ctx, store, newest); err != nil || unsettled {
				return approval, MergeApprovalWaiting, "waiting for the new version's review fix-up update", err
			}
			reason = "a new version of this task needs review; approve again"
		}
		return approval, MergeApprovalReapproval, reason, nil
	}
	approval.Head = newest.HeadSHA
	return approval, "", "", nil
}

// unsettledFixup reports whether revision is a new source version on the open
// PR that has no verdict and no feedback update yet: the feedback reconciler
// still has to push it, hold it or say why it is not pushed.
func unsettledFixup(ctx context.Context, store *journal.SQLite, revision loomgit.Revision) (bool, error) {
	if _, err := store.LatestVerdict(ctx, revision); err == nil || !errors.Is(err, journal.ErrNotFound) {
		return false, err
	}
	_, found, err := store.FeedbackUpdateFor(ctx, revision.Workspace, revision.Change, revision.Number)
	return !found, err
}

// verdictCarriedFrom reports whether revision's verdict is a chain of carried
// verdicts that reaches the verdict recorded for head.
func verdictCarriedFrom(ctx context.Context, store *journal.SQLite, revision loomgit.Revision, head string) (bool, error) {
	verdict, err := store.LatestVerdict(ctx, revision)
	if errors.Is(err, journal.ErrNotFound) {
		return false, nil
	}
	for hops := 0; err == nil && verdict.Kind == "carried" && hops < 64; hops++ {
		verdict, err = store.VerdictByID(ctx, verdict.SourceVerdictID)
		if err == nil && verdict.HeadSHA == head {
			return verdict.Kind != "reject", nil
		}
	}
	if errors.Is(err, journal.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

// conflictReaches reports whether publication's change conflicted or is
// stacked above a change that did. Stack layers are ordered by PR number. A
// conflict recorded without its changes reaches the whole stack.
func conflictReaches(ctx context.Context, store *journal.SQLite, publication journal.Publication,
	changes []string) (bool, error) {
	if len(changes) == 0 {
		return true, nil
	}
	for _, change := range changes {
		conflicted, found, err := store.Publication(ctx, publication.Workspace, change)
		if err != nil {
			return false, err
		}
		if !found || conflicted.StackID != publication.StackID || conflicted.PRNumber <= publication.PRNumber {
			return true, nil
		}
	}
	return false, nil
}

const rebuildNotCleanReason = "the rebuild after the PRs below merged was not clean; approve again"

// restackPending returns the approval status and reason for a rebuild of the
// change, after a PR below it merged, that has not finished or needs
// attention, or "" when there is none. A conflicted rebuild can never carry
// the approval, so the conflicting change and those stacked above it ask
// again; clean changes below keep theirs. The stack keeps its resolve
// attention.
func restackPending(ctx context.Context, store *journal.SQLite, approval journal.MergeApproval,
	publication journal.Publication) (string, string, error) {
	if publication.StackID != "" {
		state, err := store.StackState(ctx, approval.Workspace, publication.StackID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", "", err
		}
		switch state.Status {
		case "restack_conflict":
			reached, err := conflictReaches(ctx, store, publication, state.ConflictChanges)
			if err != nil || reached {
				return MergeApprovalReapproval, rebuildNotCleanReason, err
			}
		case "swap_held":
			return MergeApprovalWaiting, "the rebuild after the PRs below merged needs attention: " + state.Status, nil
		case "review_required":
			// The rebuild finished, but a rebuilt layer needs a new verdict, so
			// its restack offer stays open; the change's newest revision decides.
			return "", "", nil
		}
	}
	offers, err := store.RestackOffers(ctx, approval.Workspace, approval.Change)
	if err != nil {
		return "", "", err
	}
	for _, offer := range offers {
		if offer.DerivedRevision == 0 {
			return MergeApprovalWaiting, "rebuilding after the PRs below merged", nil
		}
	}
	return "", "", nil
}

// mergeReadiness returns a status and reason that hold the merge back, or ""
// when the PR is the bottom of its stack, at the approved head, and green.
func mergeReadiness(ctx context.Context, store *journal.SQLite, forge mergeApprovalForge,
	approval journal.MergeApproval, publication journal.Publication) (string, string, error) {
	if publication.Head != approval.Head {
		return MergeApprovalWaiting, "waiting for the PR to update to the approved version", nil
	}
	below, err := prsBelow(ctx, store, publication)
	if err != nil || len(below) > 0 {
		return MergeApprovalWaiting, MergeAfterReason(below), err
	}
	if forge == nil {
		return MergeApprovalBlocked, "GitHub host credential unavailable", nil
	}
	owner, repo, _ := strings.Cut(publication.Slug, "/")
	pr, err := forge.PullByNumber(ctx, owner, repo, publication.PRNumber)
	if err != nil {
		return "", "", err
	}
	if pr.Merged {
		return MergeApprovalWaiting, "merged on the provider; waiting for it to land", nil
	}
	if pr.State != "open" {
		return MergeApprovalCancelled, "the PR was closed", nil
	}
	if pr.HeadSHA != approval.Head || publication.DriftSHA != "" {
		return MergeApprovalStale, "someone else pushed to the PR after it was approved", nil
	}
	statuses, err := forge.PRStatuses(ctx, owner, repo, publication.Branch)
	if err != nil {
		return "", "", err
	}
	status, found := statuses[publication.Branch]
	if !found || status.Number != pr.Number {
		return MergeApprovalBlocked, "the PR's required checks are unavailable", nil
	}
	if reason := notGreenReason(status); reason != "" {
		return MergeApprovalBlocked, reason, nil
	}
	return "", "", nil
}

// notGreenReason says why branch protection would refuse the merge, or "".
func notGreenReason(status stackpublish.PRStatus) string {
	switch {
	case !requiredChecksPass(status) && status.Checks == "pending":
		return "waiting for required checks"
	case !requiredChecksPass(status):
		return "required checks are failing"
	case status.Review == "changes_requested":
		return "a reviewer requested changes"
	case !reviewMet(status):
		return "waiting for a required review"
	case status.Mergeable != "mergeable":
		return "the PR is not mergeable (" + status.Mergeable + ")"
	}
	return ""
}

// followMergeApproval starts the approved merge through the stack's backend,
// once, and records how it ended. A crash after the approval moved to merging
// resumes the same merge request; it never starts a second one.
func followMergeApproval(ctx context.Context, store *journal.SQLite, forge mergeApprovalForge, approval journal.MergeApproval) error {
	publication, found, err := store.Publication(ctx, approval.Workspace, approval.Change)
	if err != nil {
		return err
	}
	if !found || publication.PRNumber == 0 {
		return setMergeApproval(ctx, store, approval, MergeApprovalBlocked, "the PR record is unavailable")
	}
	if forge == nil {
		return nil
	}
	if publication.StackID == "" {
		return followTrunkMerge(ctx, store, forge, approval, publication)
	}
	backend, err := store.StackBackend(ctx, approval.Workspace, publication.StackID)
	if err != nil {
		return err
	}
	if backend == "native" {
		return followNativeApprovalMerge(ctx, store, forge, approval, publication)
	}
	return followLoomApprovalMerge(ctx, store, forge, approval, publication)
}

func followLoomApprovalMerge(ctx context.Context, store *journal.SQLite, forge mergeApprovalForge,
	approval journal.MergeApproval, publication journal.Publication) error {
	existing, err := store.LoomMerge(ctx, approval.Workspace, publication.StackID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && existing.RequestID == approval.MergeRequestID {
		switch existing.Phase {
		case "done":
			return setMergeApproval(ctx, store, approval, MergeApprovalMerged, "")
		case "blocked":
			return setMergeApproval(ctx, store, approval, MergeApprovalBlocked, existing.Reason)
		}
		return nil
	}
	if err == nil && existing.Phase != "done" && existing.Phase != "blocked" {
		return setMergeApproval(ctx, store, approval, MergeApprovalWaiting, "another merge of this stack is running")
	}
	request, err := approvalMergeRequest(ctx, store, forge, approval, publication)
	if err == nil {
		err = beginLoomMerge(ctx, store, request, approval.Change)
	}
	return blockOnCodedError(ctx, store, approval, err)
}

func followNativeApprovalMerge(ctx context.Context, store *journal.SQLite, forge mergeApprovalForge,
	approval journal.MergeApproval, publication journal.Publication) error {
	existing, err := store.NativeMerge(ctx, approval.Workspace, publication.StackID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	begun := approval.DispatchHead != ""
	if err == nil && existing.Target == approval.Change && existing.Authority == humanApprovalAuthority &&
		(begun || existing.Phase != "blocked") {
		switch existing.Phase {
		case "done":
			return setMergeApproval(ctx, store, approval, MergeApprovalMerged, "")
		case "blocked":
			return setMergeApproval(ctx, store, approval, MergeApprovalBlocked, existing.Reason)
		}
		return reconcileApprovalNative(ctx, store, forge)
	}
	if err == nil && existing.Phase != "done" && existing.Phase != "blocked" {
		return setMergeApproval(ctx, store, approval, MergeApprovalWaiting, "another merge of this stack is running")
	}
	after := approval
	after.DispatchHead = publication.Head
	if err := store.AdvanceMergeApproval(ctx, approval, after); err != nil {
		return ignoreStale(err)
	}
	approval.Version++
	approval.DispatchHead = after.DispatchHead
	request, err := approvalMergeRequest(ctx, store, forge, approval, publication)
	if err == nil {
		// GitHub merges the native prefix up to the target, so the layers below
		// that already merged are not part of it.
		request.Changes, err = unlandedChanges(ctx, store, approval.Workspace, request.Changes)
	}
	if err == nil {
		err = GitHubStackBackend{Store: store}.MergeUpTo(ctx, request, approval.Change)
	}
	return blockOnCodedError(ctx, store, approval, err)
}

func unlandedChanges(ctx context.Context, store *journal.SQLite, workspace string, changes []string) ([]string, error) {
	open := make([]string, 0, len(changes))
	for _, change := range changes {
		landed, err := store.IsLanded(ctx, workspace, change)
		if err != nil {
			return nil, err
		}
		if !landed {
			open = append(open, change)
		}
	}
	return open, nil
}

func reconcileApprovalNative(ctx context.Context, store *journal.SQLite, forge mergeApprovalForge) error {
	native, ok := forge.(nativeMergeForge)
	if !ok {
		return errors.New("forge cannot merge native stacks")
	}
	return ReconcileNativeMerges(ctx, store, native)
}

// approvalMergeRequest is the backend request for merging the approved bottom
// PR: the whole applied stack, merged up to the approved change only.
func approvalMergeRequest(ctx context.Context, store *journal.SQLite, forge mergeApprovalForge,
	approval journal.MergeApproval, publication journal.Publication) (StackRequest, error) {
	view, err := appliedMergeView(ctx, store, approval.Workspace, approval.Lead, publication.StackID, approval.Change, publication)
	if err != nil {
		return StackRequest{}, err
	}
	request, err := mergeEntryRequest(ctx, store, approval.Workspace, approval.Lead, publication.StackID, view, forge)
	request.MergeAuthority = approvedMerge{Store: store, Approval: approval}
	return request, err
}

// blockOnCodedError records a refused merge as blocked so the next pass
// re-checks it (a push to the approved PR then shows as stale_subject); an
// uncoded error (provider down) is retried as is.
func blockOnCodedError(ctx context.Context, store *journal.SQLite, approval journal.MergeApproval, err error) error {
	var coded *loomgit.Error
	if err == nil || !errors.As(err, &coded) {
		return err
	}
	return setMergeApproval(ctx, store, approval, MergeApprovalBlocked, err.Error())
}

// approvedMerge authorizes a backend merge of exactly the approved bottom PR
// at the approved head, while the approval is still the one that started it.
type approvedMerge struct {
	Store    *journal.SQLite
	Approval journal.MergeApproval
}

func (authority approvedMerge) AuthorizeMerge(ctx context.Context, request StackRequest, target string) error {
	recorded, found, err := authority.Store.MergeApproval(ctx, request.Workspace, target)
	if err != nil {
		return err
	}
	if !found || recorded.Status != MergeApprovalMerging || recorded.ActorKind != "human" ||
		recorded.MergeRequestID != authority.Approval.MergeRequestID {
		return errors.New("the merge is not approved by a human")
	}
	publication, found, err := authority.Store.Publication(ctx, request.Workspace, target)
	if err != nil {
		return err
	}
	if !found || publication.StackID != request.StackID || publication.Head != recorded.Head {
		return loomgit.NewError(loomgit.Stale, "the approved PR head changed", nil)
	}
	below, err := prsBelow(ctx, authority.Store, publication)
	if err != nil {
		return err
	}
	if len(below) > 0 {
		return errors.New("the approved PR is not the bottom of its stack")
	}
	return nil
}

// followTrunkMerge merges a trunk-mode PR (its own PR to trunk) through the
// provider, pinned to the approved head and never bypassing branch
// protection. The dispatch is journaled first, so a crash re-reads the PR
// instead of merging blind; after two unknown outcomes it reports instead.
func followTrunkMerge(ctx context.Context, store *journal.SQLite, forge mergeApprovalForge,
	approval journal.MergeApproval, publication journal.Publication) error {
	owner, repo, _ := strings.Cut(publication.Slug, "/")
	pr, err := forge.PullByNumber(ctx, owner, repo, publication.PRNumber)
	if err != nil {
		return err
	}
	if pr.Merged {
		return setMergeApproval(ctx, store, approval, MergeApprovalMerging, "merged; waiting for it to land")
	}
	if pr.HeadSHA != approval.Head {
		return setMergeApproval(ctx, store, approval, MergeApprovalStale, "someone else pushed to the PR after it was approved")
	}
	if approval.ProviderRequestID != "" {
		return pollTrunkMerge(ctx, store, forge, approval, owner, repo, pr.Number)
	}
	if approval.DispatchAttempts >= 2 {
		approval.Attention = true
		return setMergeApproval(ctx, store, approval, MergeApprovalBlocked, "the merge outcome is unknown after two attempts; check the PR")
	}
	after := approval
	after.DispatchAttempts++
	after.DispatchHead = pr.HeadSHA
	if err := store.AdvanceMergeApproval(ctx, approval, after); err != nil {
		return ignoreStale(err)
	}
	after.Version = approval.Version + 1
	result, err := forge.MergeLoomPull(ctx, owner, repo, pr.Number, pr.HeadSHA)
	if err != nil {
		var rejected *stackpublish.LoomMergeRejectedError
		if errors.As(err, &rejected) {
			return setMergeApproval(ctx, store, after, MergeApprovalBlocked, err.Error())
		}
		return err
	}
	return recordTrunkResult(ctx, store, after, result)
}

func pollTrunkMerge(ctx context.Context, store *journal.SQLite, forge mergeApprovalForge,
	approval journal.MergeApproval, owner, repo string, number int) error {
	result, err := forge.LoomMergeStatus(ctx, owner, repo, number, approval.ProviderRequestID)
	if err != nil {
		return err
	}
	return recordTrunkResult(ctx, store, approval, result)
}

func recordTrunkResult(ctx context.Context, store *journal.SQLite, approval journal.MergeApproval, result stackpublish.LoomMergeResult) error {
	switch {
	case result.Details.BypassRules:
		return setMergeApproval(ctx, store, approval, MergeApprovalBlocked, "the provider merge bypassed repository rules")
	case result.Details.ExpectedHeadSHA != "" && result.Details.ExpectedHeadSHA != approval.Head:
		return setMergeApproval(ctx, store, approval, MergeApprovalStale, "the provider merged a different head")
	case result.Status == "failed":
		return setMergeApproval(ctx, store, approval, MergeApprovalBlocked, "provider merge failed: "+result.Details.Message)
	case result.Status == "merged" || approval.ProviderRequestID != "":
		return nil
	}
	after := approval
	after.ProviderRequestID = result.Details.UUID
	return ignoreStale(store.AdvanceMergeApproval(ctx, approval, after))
}

// setMergeApproval writes a status change; a concurrent writer wins.
func setMergeApproval(ctx context.Context, store *journal.SQLite, approval journal.MergeApproval, status, reason string) error {
	current, found, err := store.MergeApproval(ctx, approval.Workspace, approval.Change)
	if err != nil || !found || current.Version != approval.Version {
		return err
	}
	if status == "" || (current.Status == status && current.Reason == reason && current.Head == approval.Head &&
		current.Attention == approval.Attention) {
		return nil
	}
	after := approval
	after.Status, after.Reason = status, reason
	return ignoreStale(store.AdvanceMergeApproval(ctx, current, after))
}

func ignoreStale(err error) error {
	if errors.Is(err, journal.ErrStale) {
		return nil
	}
	return err
}
