package review

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

type Local struct{ store *journal.SQLite }

func OpenLocal() (*Local, error) {
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
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

func (l *Local) WorkingAreas(ctx context.Context, workspace, lead string) ([]journal.WorkingArea, error) {
	return l.store.WorkingAreas(ctx, workspace, lead)
}

type TaskRevision struct {
	ChangeID   string `json:"change_id"`
	Number     int    `json:"number"`
	HeadSHA    string `json:"head_sha"`
	Outcome    string `json:"outcome"`
	Incomplete bool   `json:"incomplete"`
	Verdict    string `json:"verdict,omitempty"`
	Applied    bool   `json:"applied"`
	// NeedsWorkingArea marks an approved, unapplied revision whose target lead
	// has no working area yet, so the UI can offer Apply after a reload.
	NeedsWorkingArea bool `json:"needs_working_area"`
	// PRURL and PRNumber name the change's open PR, if one was published.
	PRURL    string `json:"pr_url,omitempty"`
	PRNumber int    `json:"pr_number,omitempty"`
	// PublishStatus is this revision's Approve and create PR outcome: pending,
	// waiting, published, not_published or superseded; PublishReason says why.
	PublishStatus string `json:"publish_status,omitempty"`
	PublishReason string `json:"publish_reason,omitempty"`
	// PRHead is the open PR's head SHA; Approve and merge pins it (D29 (3)).
	PRHead string `json:"pr_head,omitempty"`
	// MergeAfter lists the open PRs below this one in its stack, bottom first;
	// empty means the PR is the bottom and Approve merges it now.
	MergeAfter []int `json:"merge_after,omitempty"`
	// MergeStatus and MergeReason report the change's Approve and merge:
	// waiting, blocked, merging, merged, stale_subject, reapproval_required or
	// cancelled.
	MergeStatus string `json:"merge_status,omitempty"`
	MergeReason string `json:"merge_reason,omitempty"`
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
		i := TaskRevision{ChangeID: r.Change, Number: r.Number, HeadSHA: r.HeadSHA, Outcome: r.Outcome, Incomplete: r.Incomplete}
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
			if i.NeedsWorkingArea, err = l.needsWorkingArea(ctx, workspace, lead, r.Change, r.Number); err != nil {
				return nil, err
			}
		}
		if err := l.addPublishState(ctx, workspace, &i); err != nil {
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

func (l *Local) addPublishState(ctx context.Context, workspace string, i *TaskRevision) error {
	publication, found, err := l.store.Publication(ctx, workspace, i.ChangeID)
	if err != nil {
		return err
	}
	if found && publication.Phase == "done" && publication.PRNumber > 0 {
		i.PRURL, i.PRNumber, i.PRHead = publication.PRURL, publication.PRNumber, publication.Head
		if err := l.addMergeState(ctx, publication, i); err != nil {
			return err
		}
	}
	intent, found, err := l.store.LatestApprovalPublication(ctx, workspace, i.ChangeID, i.Number)
	if err != nil || !found {
		return err
	}
	i.PublishStatus, i.PublishReason = intent.Status, intent.Reason
	return nil
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
