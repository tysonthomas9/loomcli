package publish

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/githubtoken"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

// humanApprovalAuthority marks a backend merge started by a human's Approve
// and merge of the bottom PR (D29 (3)).
const humanApprovalAuthority = "human_approval"

// Merge approval states (D29): waiting merges after the PRs below it; blocked
// waits for checks or reviews and is retried; merging is with the backend.
// The rest are final: a new Approve and merge is needed after any of them
// except merged.
const (
	MergeApprovalWaiting    = "waiting"
	MergeApprovalBlocked    = "blocked"
	MergeApprovalMerging    = "merging"
	MergeApprovalMerged     = "merged"
	MergeApprovalStale      = "stale_subject"
	MergeApprovalReapproval = "reapproval_required"
	MergeApprovalCancelled  = "cancelled"
)

// MergeApprovalView is what Approve and merge recorded for a change, and why it
// has not merged yet.
type MergeApprovalView struct {
	Change     string `json:"change"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	Head       string `json:"head"`
	StackID    string `json:"stack_id,omitempty"`
	PRNumber   int    `json:"pr_number,omitempty"`
	PRURL      string `json:"pr_url,omitempty"`
	MergeAfter []int  `json:"merge_after,omitempty"`
}

// mergeApprovalForge is the provider an approval merges through: the Loom
// merge machine's calls, plus the native stack calls on GitHub.
type mergeApprovalForge interface {
	Forge
	loomMergeForge
}

var mergeApprovalNow = time.Now

// approvalForge returns the configured provider, or nil when this host has no
// GitHub credential; an approval then waits as blocked.
var approvalForge = func(ctx context.Context) mergeApprovalForge {
	if forge, _, _ := localPublishProvider(); forge != nil {
		if merger, ok := forge.(mergeApprovalForge); ok {
			return merger
		}
	}
	if token := githubtoken.GitHub(ctx); token != "" {
		return stackpublish.NewConfiguredGitHubForge(token)
	}
	return nil
}

// ApproveMergeLocal records a human's Approve and merge of change's open PR at
// head, the head the human saw, and merges it now if it is the bottom of its
// stack. Otherwise the approval waits and merges once every PR below it has.
func ApproveMergeLocal(ctx context.Context, workspace, lead, change, head string, actor MergeActor) (MergeApprovalView, error) {
	store, err := openLocalStore()
	if err != nil {
		return MergeApprovalView{}, err
	}
	defer func() { _ = store.Close() }()
	return approveMerge(ctx, store, approvalForge(ctx), workspace, lead, change, head, actor)
}

func approveMerge(ctx context.Context, store *journal.SQLite, forge mergeApprovalForge,
	workspace, lead, change, head string, actor MergeActor) (MergeApprovalView, error) {
	if actor.Kind != "human" || actor.ID == "" || actor.ID == lead {
		// D15/D29 (5): the lead never merges on its own approval.
		return MergeApprovalView{}, loomgit.NewError(loomgit.MergeNotAuthorized, "only a human can approve a merge", nil)
	}
	if workspace == "" || lead == "" || change == "" || head == "" {
		return MergeApprovalView{}, errors.New("workspace, lead, change and head are required")
	}
	publication, err := openPublication(ctx, store, workspace, change)
	if err != nil {
		return MergeApprovalView{}, err
	}
	if err := requireApprovableHead(ctx, store, workspace, change, head, publication.Head); err != nil {
		return MergeApprovalView{}, err
	}
	approval := journal.MergeApproval{Workspace: workspace, Change: change, Lead: lead, StackID: publication.StackID,
		Head: head, ActorKind: actor.Kind, ActorID: actor.ID, Status: MergeApprovalWaiting,
		CreatedAt: mergeApprovalNow().UnixNano()}
	recorded, err := store.RecordMergeApproval(ctx, approval)
	if errors.Is(err, journal.ErrStale) {
		return MergeApprovalView{}, loomgit.NewError(loomgit.Stale,
			"an Approve and merge for another version of this PR is active; cancel it first", nil)
	}
	if err != nil {
		return MergeApprovalView{}, err
	}
	// The approval stands even when the provider is unreachable now; Reconcile
	// retries it.
	_ = advanceMergeApproval(ctx, store, forge, recorded)
	return mergeApprovalView(ctx, store, workspace, change)
}

// openPublication returns change's open, unmerged PR record.
func openPublication(ctx context.Context, store *journal.SQLite, workspace, change string) (journal.Publication, error) {
	publication, found, err := store.Publication(ctx, workspace, change)
	if err != nil {
		return publication, err
	}
	if !found || publication.Phase != "done" || publication.PRNumber == 0 {
		return publication, loomgit.NewError(loomgit.MergeBlocked, "the change has no open PR to merge", nil)
	}
	landed, err := store.IsLanded(ctx, workspace, change)
	if err != nil {
		return publication, err
	}
	if landed {
		return publication, loomgit.NewError(loomgit.Stale, "the change has already merged", nil)
	}
	return publication, nil
}

// requireApprovableHead accepts the PR head, or the change's newest revision
// that is about to replace it, and only with an approval of its content.
func requireApprovableHead(ctx context.Context, store *journal.SQLite, workspace, change, head, prHead string) error {
	if head != prHead {
		newest, err := newestRevision(ctx, store, workspace, change)
		if err != nil || newest.HeadSHA != head {
			return loomgit.NewError(loomgit.Stale, "the PR changed since you looked; reload and approve again", err)
		}
	}
	revision, err := store.RevisionByHead(ctx, workspace, change, head)
	if err != nil {
		return loomgit.NewError(loomgit.ReviewRequired, "the PR head has no reviewed revision", err)
	}
	return review.RequireVerdict(ctx, store, workspace, change, revision.Number, head, "publish", "")
}

func newestRevision(ctx context.Context, store *journal.SQLite, workspace, change string) (loomgit.Revision, error) {
	number, err := store.SourceRevision(ctx, workspace, change)
	if err != nil {
		return loomgit.Revision{}, err
	}
	return store.GetRevision(ctx, workspace, change, number)
}

// CancelMergeApproval cancels change's pending Approve and merge: Cancel
// auto-merge, and a feedback fix-up that changes the PR (P2.19b). It reports
// whether an approval was cancelled. A merge already handed to the backend
// cannot be cancelled here.
func CancelMergeApproval(ctx context.Context, store *journal.SQLite, workspace, change, reason string) (bool, error) {
	approval, found, err := store.MergeApproval(ctx, workspace, change)
	if err != nil || !found || !journal.MergeApprovalActive(approval.Status) {
		return false, err
	}
	if approval.Status == MergeApprovalMerging {
		return false, loomgit.NewError(loomgit.Stale, "the merge has already started", nil)
	}
	after := approval
	after.Status, after.Reason = MergeApprovalCancelled, reason
	if err := store.AdvanceMergeApproval(ctx, approval, after); err != nil {
		if errors.Is(err, journal.ErrStale) {
			return false, loomgit.NewError(loomgit.Stale, "the approval changed during the cancel; reload", err)
		}
		return false, err
	}
	return true, nil
}

// CancelMergeApprovalLocal is Cancel auto-merge from the UI or CLI.
func CancelMergeApprovalLocal(ctx context.Context, workspace, change string, actor MergeActor) (MergeApprovalView, error) {
	if actor.ID == "" || (actor.Kind != "human" && actor.Kind != "lead") {
		return MergeApprovalView{}, loomgit.NewError(loomgit.MergeNotAuthorized, "actor kind and ID are required", nil)
	}
	store, err := openLocalStore()
	if err != nil {
		return MergeApprovalView{}, err
	}
	defer func() { _ = store.Close() }()
	if _, err := CancelMergeApproval(ctx, store, workspace, change, "auto-merge cancelled by "+actor.ID); err != nil {
		return MergeApprovalView{}, err
	}
	return mergeApprovalView(ctx, store, workspace, change)
}

// MergeApprovalLocal reports change's Approve and merge state.
func MergeApprovalLocal(ctx context.Context, workspace, change string) (MergeApprovalView, error) {
	store, err := openLocalStore()
	if err != nil {
		return MergeApprovalView{}, err
	}
	defer func() { _ = store.Close() }()
	return mergeApprovalView(ctx, store, workspace, change)
}

func mergeApprovalView(ctx context.Context, store *journal.SQLite, workspace, change string) (MergeApprovalView, error) {
	view := MergeApprovalView{Change: change}
	publication, found, err := store.Publication(ctx, workspace, change)
	if err != nil {
		return view, err
	}
	if found {
		view.StackID, view.PRNumber, view.PRURL = publication.StackID, publication.PRNumber, publication.PRURL
		if view.MergeAfter, err = prsBelow(ctx, store, publication); err != nil {
			return view, err
		}
	}
	approval, found, err := store.MergeApproval(ctx, workspace, change)
	if err != nil || !found {
		return view, err
	}
	view.Status, view.Reason, view.Head = approval.Status, approval.Reason, approval.Head
	return view, nil
}

// prsBelow lists the open, unmerged PRs below publication in its stack.
func prsBelow(ctx context.Context, store *journal.SQLite, publication journal.Publication) ([]int, error) {
	return store.UnlandedPRsBelow(ctx, publication)
}

// MergeAfterReason is the waiting status a non-bottom approval shows.
func MergeAfterReason(below []int) string {
	numbers := make([]string, len(below))
	for index, number := range below {
		numbers[index] = fmt.Sprintf("#%d", number)
	}
	return "merges after " + strings.Join(numbers, ", ")
}

// ReconcileMergeApprovals advances every open Approve and merge.
func ReconcileMergeApprovals(ctx context.Context) error {
	return ReconcileMergeApprovalsAt(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"), approvalForge(ctx))
}

// PublishedStack is one Loom Git stack of PRs, as the user can address it with
// loom merge. A cross-repo lead has one per repository.
type PublishedStack struct {
	StackID string                `json:"stack_id"`
	Repo    string                `json:"repo"`
	Slug    string                `json:"slug"`
	Layers  []PublishedStackLayer `json:"layers"`
}

type PublishedStackLayer struct {
	Change   string `json:"change"`
	PRNumber int    `json:"pr_number"`
	PRURL    string `json:"pr_url"`
	Landed   bool   `json:"landed"`
}

// PublishedStacksLocal lists workspace's Loom Git stacks, bottom layer first.
// workspace may be the workspace ID or its configured name.
func PublishedStacksLocal(ctx context.Context, workspace string) ([]PublishedStack, error) {
	if cfg, err := config.LoadConfig(); err == nil {
		if _, _, found := config.WorkspaceByID(cfg, workspace); !found {
			if configured, ok := cfg.Workspaces[workspace]; ok && configured.ID != "" {
				workspace = configured.ID
			}
		}
	}
	store, err := openLocalStore()
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	publications, err := store.PublishedStacks(ctx, workspace)
	if err != nil {
		return nil, err
	}
	var stacks []PublishedStack
	for _, publication := range publications {
		if len(stacks) == 0 || stacks[len(stacks)-1].StackID != publication.StackID {
			stacks = append(stacks, PublishedStack{StackID: publication.StackID, Repo: filepath.Base(publication.Repo), Slug: publication.Slug})
		}
		landed, err := store.IsLanded(ctx, workspace, publication.Change)
		if err != nil {
			return nil, err
		}
		stack := &stacks[len(stacks)-1]
		stack.Layers = append(stack.Layers, PublishedStackLayer{Change: publication.Change,
			PRNumber: publication.PRNumber, PRURL: publication.PRURL, Landed: landed})
	}
	return stacks, nil
}
