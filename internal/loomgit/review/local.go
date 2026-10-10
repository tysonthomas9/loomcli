package review

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

type Local struct{ store *journal.SQLite }

func OpenLocal() (*Local, error) {
	return OpenLocalAt(filepath.Join(config.GetConfigDir(), "loomgit", "store.db"))
}

// OpenLocalAt opens the journal at path; it must exist.
func OpenLocalAt(path string) (*Local, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	return &Local{store: store}, nil
}

func (l *Local) Close() error { return l.store.Close() }

// TaskForChange names the task a change belongs to ("" when none).
func (l *Local) TaskForChange(ctx context.Context, workspace, change string) (string, error) {
	return l.store.TaskForChange(ctx, workspace, change)
}

// TaskWorkspaces lists the workspaces whose tasks have changes.
func (l *Local) TaskWorkspaces(ctx context.Context) ([]string, error) {
	return l.store.TaskWorkspaces(ctx)
}

// Revision reads one recorded revision.
func (l *Local) Revision(ctx context.Context, workspace, change string, number int) (loomgit.Revision, error) {
	return l.store.GetRevision(ctx, workspace, change, number)
}

func (l *Local) Submit(ctx context.Context, workspace, change string, number int, headSHA, kind, reason string, actor Actor) (loomgit.Verdict, error) {
	return Submit(ctx, l.store, workspace, change, number, headSHA, kind, reason, actor)
}

func (l *Local) SubmitForLead(ctx context.Context, workspace, change string, number int, headSHA, kind, reason string, actor Actor, lead string) (loomgit.Verdict, error) {
	return SubmitForLead(ctx, l.store, workspace, change, number, headSHA, kind, reason, actor, lead)
}

func (l *Local) SubmitForLeadPublishing(ctx context.Context, workspace, change string, number int, headSHA, kind, reason string, actor Actor, lead string, publish bool) (loomgit.Verdict, error) {
	return SubmitForLeadPublishing(ctx, l.store, workspace, change, number, headSHA, kind, reason, actor, lead, publish)
}

func (l *Local) FollowingPaused(ctx context.Context, workspace, lead string) (bool, error) {
	return l.store.FollowingPaused(ctx, workspace, lead)
}

func (l *Local) SetFollowingPaused(ctx context.Context, workspace, lead string, paused bool) error {
	return l.store.SetFollowingPaused(ctx, workspace, lead, paused)
}

// ApprovalFollowState reports the follow status and reason of a revision's
// approval in lead (or the latest approved lead when lead is empty).
func (l *Local) ApprovalFollowState(ctx context.Context, workspace, lead, change string, revision int) (string, string, error) {
	return l.store.ApprovalFollowState(ctx, workspace, lead, change, revision)
}

func (l *Local) WorkingAreas(ctx context.Context, workspace, lead string) ([]journal.WorkingArea, error) {
	return l.store.WorkingAreas(ctx, workspace, lead)
}

type TaskRevision struct {
	ChangeID string `json:"change_id"`
	// Repo is the workspace repo name the revision diff route accepts.
	Repo       string `json:"repo"`
	Number     int    `json:"number"`
	HeadSHA    string `json:"head_sha"`
	Outcome    string `json:"outcome"`
	Incomplete bool   `json:"incomplete"`
	Verdict    string `json:"verdict,omitempty"`
	Applied    bool   `json:"applied"`
	// FollowStatus is the lead follow state of this revision's approval
	// ("spent" when its apply can never run; approve again to re-arm), with
	// the reviewer-facing reason.
	FollowStatus string `json:"follow_status,omitempty"`
	FollowReason string `json:"follow_reason,omitempty"`
	// NeedsWorkingArea marks an approved, unapplied revision whose target lead
	// has no working area yet, so the UI can offer Apply after a reload.
	NeedsWorkingArea bool `json:"needs_working_area"`
	// Superseded marks a revision older than its change's newest source
	// revision; the server refuses verdicts on it.
	Superseded bool `json:"superseded"`
	// NoChanges marks a complete source revision identical to its base: the
	// task closed with no review, apply or PR, and verdicts are refused.
	NoChanges bool `json:"no_changes"`
	// Date is the revision head's commit date, when the repo is readable.
	Date string `json:"date,omitempty"`
	// PRURL and PRNumber name the change's open PR, if one was published.
	PRURL    string `json:"pr_url,omitempty"`
	PRNumber int    `json:"pr_number,omitempty"`
	// PublishStatus is this revision's Approve and create PR outcome: pending,
	// waiting, published, not_published or superseded; PublishReason says why.
	PublishStatus string `json:"publish_status,omitempty"`
	PublishReason string `json:"publish_reason,omitempty"`
	// PRHead is the open PR's head SHA; Approve and merge pins it (D29 (3)).
	PRHead string `json:"pr_head,omitempty"`
	// PRState is the PR's state: open, merged or closed.
	PRState string `json:"pr_state,omitempty"`
	// MergeAfter lists the open PRs below this one in its stack, bottom first;
	// empty means the PR is the bottom and Approve merges it now.
	MergeAfter []int `json:"merge_after,omitempty"`
	// MergeStatus and MergeReason report the change's Approve and merge:
	// waiting, blocked, merging, merged, stale_subject, reapproval_required or
	// cancelled.
	MergeStatus string `json:"merge_status,omitempty"`
	MergeReason string `json:"merge_reason,omitempty"`
	// FeedbackStatus reports a review fix-up's automatic update of its open PR
	// (D29 (6)): pushing, pushed, held, not_pushed or superseded, with
	// FeedbackReason saying why it is held or not pushed. FeedbackMergeCancelled
	// says the fix-up cancelled a pending Approve and merge.
	FeedbackStatus         string `json:"feedback_status,omitempty"`
	FeedbackReason         string `json:"feedback_reason,omitempty"`
	FeedbackMergeCancelled bool   `json:"feedback_merge_cancelled,omitempty"`
	// DependsOn is the task this revision's code was built on, before that
	// task's code was reviewed. LineageState is "stale" when that task's
	// revision was rejected or replaced, or "dependency_abandoned"; it is
	// empty when the base is current. RebuildOn is the predecessor revision a
	// rebuild would build on, 0 if none yet.
	DependsOn     string `json:"depends_on,omitempty"`
	LineageState  string `json:"lineage_state,omitempty"`
	LineageReason string `json:"lineage_reason,omitempty"`
	RebuildOn     int    `json:"rebuild_on,omitempty"`
}

func (l *Local) TaskRevisions(ctx context.Context, workspace, task string) ([]TaskRevision, error) {
	return l.TaskRevisionsForLead(ctx, workspace, task, "")
}

// TaskRevisionsForLead reports applied and needs-working-area state for one
// lead. An empty lead reports applied in any lead, and needs a working area
// when any approved target lead without the revision applied has none.
func (l *Local) TaskRevisionsForLead(ctx context.Context, workspace, task, lead string) ([]TaskRevision, error) {
	revisions, err := l.store.ListTaskRevisions(ctx, workspace, task)
	if err != nil {
		return nil, err
	}
	out := make([]TaskRevision, 0, len(revisions))
	for _, r := range revisions {
		i := TaskRevision{ChangeID: r.Change, Number: r.Number, HeadSHA: r.HeadSHA, Outcome: r.Outcome, Incomplete: r.Incomplete, NoChanges: r.NoChanges}
		if i.Repo, err = l.store.RepoForChange(ctx, workspace, r.Change); err != nil && !errors.Is(err, journal.ErrNotFound) {
			return nil, err
		}
		latest, err := l.store.LatestSourceNumber(ctx, workspace, r.Change)
		if err != nil {
			return nil, err
		}
		i.Superseded = latest > r.Number
		v, err := l.store.LatestVerdict(ctx, r)
		if err == nil {
			i.Verdict = v.Kind
		} else if !errors.Is(err, journal.ErrNotFound) {
			return nil, err
		}
		if i.Applied, err = l.store.RevisionApplied(ctx, workspace, lead, r.Change, r.Number); err != nil {
			return nil, err
		}
		if v.Kind == "approve" || v.Kind == "override" || v.Kind == "policy" {
			if i.FollowStatus, i.FollowReason, err = l.store.ApprovalFollowState(ctx, workspace, lead, r.Change, r.Number); err != nil {
				return nil, err
			}
			if i.NeedsWorkingArea, err = l.needsWorkingArea(ctx, workspace, lead, r.Change, r.Number); err != nil {
				return nil, err
			}
		}
		if err := l.addPublishState(ctx, workspace, &i, statusSource(r)); err != nil {
			return nil, err
		}
		if err := l.addFeedbackState(ctx, workspace, &i, statusSource(r)); err != nil {
			return nil, err
		}
		if err := l.addDependencyState(ctx, workspace, lead, r.Kind, &i); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, nil
}

// needsWorkingArea reports whether a lead targeted by this revision's approval
// (only, if set) has not applied it and has no working area to apply it into.
func (l *Local) needsWorkingArea(ctx context.Context, workspace, only, change string, revision int) (bool, error) {
	leads, err := l.store.ApprovalLeads(ctx, workspace, change, revision)
	if err != nil {
		return false, err
	}
	for _, lead := range leads {
		if only != "" && lead != only {
			continue
		}
		applied, err := l.store.RevisionApplied(ctx, workspace, lead, change, revision)
		if err != nil {
			return false, err
		}
		if applied {
			continue
		}
		areas, err := l.store.WorkingAreas(ctx, workspace, lead)
		if err != nil {
			return false, err
		}
		if len(areas) == 0 {
			return true, nil
		}
	}
	return false, nil
}

func IsNotFound(err error) bool {
	return errors.Is(err, journal.ErrNotFound) || errors.Is(err, os.ErrNotExist)
}

// statusSource names the revision whose approval state a derived revision
// reports. Apply's rebuild of an approved revision has no approval or publish
// intent of its own, and the task view shows only each change's newest
// revision, so it shows its source's outcome. Zero means none.
func statusSource(r loomgit.Revision) int {
	if r.Kind == "derived" && r.DerivedFromChange == r.Change && r.DerivedFromNumber > 0 {
		return r.DerivedFromNumber
	}
	return 0
}

func (l *Local) addPublishState(ctx context.Context, workspace string, i *TaskRevision, source int) error {
	publication, found, err := l.store.Publication(ctx, workspace, i.ChangeID)
	if err != nil {
		return err
	}
	if found && publication.Phase == "done" && publication.PRNumber > 0 {
		i.PRURL, i.PRNumber, i.PRHead = publication.PRURL, publication.PRNumber, publication.Head
		if err := l.addMergeState(ctx, publication, i); err != nil {
			return err
		}
		landed, err := l.store.IsLanded(ctx, workspace, i.ChangeID)
		if err != nil {
			return err
		}
		observation, _, err := l.store.ProviderObservation(ctx, workspace, i.ChangeID)
		if err != nil {
			return err
		}
		i.PRState = PRState(landed, observation.State, i.MergeStatus)
	}
	intent, found, err := l.store.LatestApprovalPublication(ctx, workspace, i.ChangeID, i.Number)
	if err == nil && !found && source > 0 {
		intent, found, err = l.store.LatestApprovalPublication(ctx, workspace, i.ChangeID, source)
	}
	if err != nil || !found {
		return err
	}
	i.PublishStatus, i.PublishReason = intent.Status, intent.Reason
	return nil
}

// PRState reports a published PR as merged once its change landed, the
// provider reported it merged, or its Approve and merge finished; as closed
// once the provider reported it closed without merging; otherwise as open.
func PRState(landed bool, observed, mergeStatus string) string {
	switch {
	case landed || observed == "merged" || mergeStatus == "merged":
		return "merged"
	case observed == "closed" || observed == "dependency_abandoned":
		return "closed"
	}
	return "open"
}

func (l *Local) addMergeState(ctx context.Context, publication journal.Publication, i *TaskRevision) error {
	below, err := l.store.UnlandedPRsBelow(ctx, publication)
	if err != nil {
		return err
	}
	i.MergeAfter = below
	approval, found, err := l.store.MergeApproval(ctx, publication.Workspace, publication.Change)
	if err != nil || !found {
		return err
	}
	i.MergeStatus, i.MergeReason = approval.Status, approval.Reason
	return nil
}

// addFeedbackState reports how a review fix-up's automatic PR update stands.
// The layer a fix-up replaced may be rebuilt as a derived revision, which
// reports its source fix-up's update (see statusSource).
func (l *Local) addFeedbackState(ctx context.Context, workspace string, i *TaskRevision, source int) error {
	state, found, err := l.store.FeedbackUpdateFor(ctx, workspace, i.ChangeID, i.Number)
	if err == nil && !found && source > 0 {
		state, found, err = l.store.FeedbackUpdateFor(ctx, workspace, i.ChangeID, source)
	}
	if err != nil || !found {
		return err
	}
	i.FeedbackStatus, i.FeedbackReason, i.FeedbackMergeCancelled = state.Status, state.Reason, state.MergeCancelled
	if state.Status != journal.FeedbackUpdatePushing {
		return nil
	}
	switch {
	case state.FollowStatus == "conflict" || state.FollowStatus == "apply_pending":
		i.FeedbackStatus, i.FeedbackReason = "held", "it conflicts with the stack; the lead was told"
		if state.FollowStatus == "apply_pending" {
			i.FeedbackReason = "the lead's working area has edits to the same files; the lead was told"
		}
		if len(state.Paths) > 0 {
			i.FeedbackReason += " (" + strings.Join(state.Paths, ", ") + ")"
		}
	case state.PublishStatus == "published":
		i.FeedbackStatus = "pushed"
	case state.PublishStatus == "not_published":
		i.FeedbackStatus, i.FeedbackReason = "not_pushed", state.PublishReason
	case state.PublishReason != "":
		i.FeedbackReason = state.PublishReason
	}
	return nil
}
