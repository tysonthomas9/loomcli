// Package taskreview moves a task that waits for its code review out of
// review once the review is decided (D29, P1.26). A finished attempt with code
// leaves its task in status review with the code-review label. Approve, once
// the code is applied or its PR is opened, closes the task; Reject sends it
// back to open for another attempt; an empty newest attempt ("No changes",
// P1.25) closes it. Tasks without the label (plan review) are never touched.
package taskreview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// Decision is what a task in code review should become.
type Decision string

const (
	// Wait keeps the task in review: its code still awaits a verdict, or an
	// approval is not applied yet.
	Wait Decision = ""
	// CloseNoChanges closes a task whose newest attempt changed nothing.
	CloseNoChanges Decision = "no_changes"
	// CloseApproved closes a task whose approved code is applied or published.
	CloseApproved Decision = "approved"
	// Reopen sends a rejected task back to open for another attempt.
	Reopen Decision = "rejected"
)

// CloseReason is the reason a settled task closes with.
func (d Decision) CloseReason() string {
	switch d {
	case CloseNoChanges:
		return "No changes"
	case CloseApproved:
		return "Approved: code applied"
	}
	return ""
}

// approving verdict kinds: a review approval, a human override, a lead
// policy approval, and the system kinds that carry an approval to a derived
// revision (Apply's rebuild, or a review fix-up of an open PR).
var approving = map[string]bool{"approve": true, "override": true, "policy": true, "carried": true, "feedback": true}

// Decide reads a task's revisions, newest per change (one change per repo).
func Decide(revisions []review.TaskRevision) Decision {
	current := map[string]review.TaskRevision{}
	for _, revision := range revisions {
		if prior, ok := current[revision.ChangeID]; !ok || revision.Number > prior.Number {
			current[revision.ChangeID] = revision
		}
	}
	if len(current) == 0 {
		return Wait
	}
	empty, delivered := 0, 0
	for _, revision := range current {
		switch {
		case revision.Verdict == "reject":
			return Reopen
		case revision.NoChanges:
			empty++
		case approving[revision.Verdict] && isDelivered(revision):
			delivered++
		}
	}
	switch {
	case empty == len(current):
		return CloseNoChanges
	case empty+delivered == len(current):
		return CloseApproved
	}
	return Wait
}

// isDelivered reports whether an approved revision has left review: it is
// applied to a working area, or its PR is open (or could not be opened only
// because no provider is set up, which leaves it applied).
func isDelivered(revision review.TaskRevision) bool {
	return revision.Applied || revision.FollowStatus == "applied" || revision.PRNumber > 0 ||
		revision.PublishStatus == "published" || revision.PublishStatus == "not_published"
}

// Revisions lists a task's revisions; review.Local implements it.
type Revisions interface {
	TaskRevisionsForLead(ctx context.Context, workspace, task, lead string) ([]review.TaskRevision, error)
}

// SettleTask applies the decision for one task. Only a task in status review
// carrying the code-review label is changed.
func SettleTask(ctx context.Context, issues backend.IssueBackend, revisions Revisions, workspace, task string) (Decision, error) {
	issue, err := issues.Get(ctx, task)
	if err != nil {
		return Wait, fmt.Errorf("load task %s: %w", task, err)
	}
	if issue == nil || issue.Status != "review" || !backend.HasCodeReviewLabel(issue.Labels) {
		return Wait, nil
	}
	list, err := revisions.TaskRevisionsForLead(ctx, workspace, task, "")
	if err != nil {
		return Wait, err
	}
	decision := Decide(list)
	switch decision {
	case Reopen:
		open := "open"
		err = issues.Update(ctx, task, backend.UpdateParams{Status: &open, RemoveLabels: []string{backend.CodeReviewLabel}})
	case CloseApproved, CloseNoChanges:
		// A closed issue refuses label changes, so the label goes first. If
		// the close then fails, the label goes back: a task in review without
		// it reads as a plan review, and the next settle pass would skip it.
		if err = issues.Update(ctx, task, backend.UpdateParams{RemoveLabels: []string{backend.CodeReviewLabel}}); err == nil {
			if _, err = issues.Close(ctx, task, backend.CloseParams{Reason: decision.CloseReason(), Force: true}); err != nil {
				if restore := issues.Update(ctx, task, backend.UpdateParams{AddLabels: []string{backend.CodeReviewLabel}}); restore != nil {
					err = errors.Join(err, fmt.Errorf("restore the %s label: %w", backend.CodeReviewLabel, restore))
				}
			}
		}
	}
	if err != nil {
		return Wait, fmt.Errorf("settle task %s review (%s): %w", task, decision, err)
	}
	return decision, nil
}

// issuesFor returns the issue backend for a workspace; tests replace it.
var issuesFor = func(ctx context.Context, workspace string) (context.Context, backend.IssueBackend) {
	ctx = middleware.WithWorkspace(ctx, workspace)
	return ctx, cli.WorkspaceAwareIssueBackend()(ctx)
}

func journalPath(path string) string {
	if path != "" {
		return path
	}
	return filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
}

func open(path string) (*review.Local, error) {
	local, err := review.OpenLocalAt(journalPath(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return local, err
}

// SettleChange settles the task a change belongs to, right after a verdict on
// it, so the task list shows the outcome at once.
func SettleChange(ctx context.Context, path, workspace, change string) (Decision, error) {
	local, err := open(path)
	if err != nil || local == nil {
		return Wait, err
	}
	defer func() { _ = local.Close() }()
	task, err := local.TaskForChange(ctx, workspace, change)
	if err != nil || task == "" {
		return Wait, err
	}
	ctx, issues := issuesFor(ctx, workspace)
	return SettleTask(ctx, issues, local, workspace, task)
}

// SettleAll settles every task in code review, in every workspace with task
// changes. The Loom Git reconcile loop runs it, so an approval applied later
// (a held apply, the CLI) or an empty attempt frozen after its agent closed the
// task (daemon-managed agents) still settles.
func SettleAll(ctx context.Context, path string) error {
	local, err := open(path)
	if err != nil || local == nil {
		return err
	}
	defer func() { _ = local.Close() }()
	workspaces, err := local.TaskWorkspaces(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, workspace := range workspaces {
		wsCtx, issues := issuesFor(ctx, workspace)
		tasks, err := issues.List(wsCtx, backend.ListOpts{Status: "review", Labels: []string{backend.CodeReviewLabel}, Limit: 1000})
		if err != nil {
			errs = append(errs, fmt.Errorf("list %s tasks in code review: %w", workspace, err))
			continue
		}
		for _, task := range tasks {
			if _, err := SettleTask(wsCtx, issues, local, workspace, task.ID); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
