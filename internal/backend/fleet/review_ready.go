package fleet

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// A task whose code awaits review (status review with the code-review label,
// D29) has a finished agent and a frozen revision, so its dependents start on
// that revision instead of waiting for the review (Tyson, 2026-10-09). FleetDB
// counts every blocker that is not closed as open, so this backend moves a
// task whose only open direct blockers are in code review from Blocked to
// Ready. A task blocked through its parent stays blocked.

// codeReviewListLimit bounds the code-review lookup; it matches the review
// settle loop's own listing.
const codeReviewListLimit = 1000

var reviewReadyNow = time.Now

// codeReviewTasks returns the IDs of tasks whose code awaits review.
func (b *FleetBackend) codeReviewTasks(ctx context.Context) (map[string]bool, error) {
	issues, err := b.List(ctx, backend.ListOpts{Status: "review", Labels: []string{backend.CodeReviewLabel}, Limit: codeReviewListLimit})
	if err != nil {
		return nil, err
	}
	inReview := make(map[string]bool, len(issues))
	for _, issue := range issues {
		if issue.Status == "review" && backend.HasCodeReviewLabel(issue.Labels) {
			inReview[issue.ID] = true
		}
	}
	return inReview, nil
}

// startableBehindReview returns the blocked tasks that may start because each
// of their open direct blockers is in code review. It reports nothing when no
// task is in code review, so the common case costs one list call.
func (b *FleetBackend) startableBehindReview(ctx context.Context) ([]backend.IssueData, error) {
	inReview, err := b.codeReviewTasks(ctx)
	if err != nil || len(inReview) == 0 {
		return nil, err
	}
	blocked, err := b.blockedFromFleet(ctx, backend.BlockedOpts{})
	if err != nil {
		return nil, err
	}
	blockedIDs := make(map[string]bool, len(blocked))
	for _, issue := range blocked {
		blockedIDs[issue.ID] = true
	}
	var startable []backend.IssueData
	for _, issue := range blocked {
		if startsBehindReview(issue, inReview, blockedIDs, reviewReadyNow()) {
			startable = append(startable, issue)
		}
	}
	return startable, nil
}

// startsBehindReview is FleetDB's ready rule (open, not an epic, not deferred
// to the future) with code-review blockers counted as satisfied. FleetDB
// lists a task blocked only through its parent with no direct blockers, and a
// task whose parent is itself blocked keeps waiting.
func startsBehindReview(issue backend.IssueData, inReview, blocked map[string]bool, now time.Time) bool {
	if issue.Status != "open" || issue.IssueType == "epic" || len(issue.BlockedBy) == 0 {
		return false
	}
	if issue.Parent != "" && blocked[issue.Parent] {
		return false
	}
	if issue.DeferUntil != nil && issue.DeferUntil.After(now) {
		return false
	}
	for _, blocker := range issue.BlockedBy {
		if !inReview[blocker] {
			return false
		}
	}
	return true
}

// withTasksBehindReview adds the tasks that may start behind code review to
// FleetDB's ready list, in priority order, within opts' filters and limit.
func (b *FleetBackend) withTasksBehindReview(ctx context.Context, ready []backend.IssueData, opts backend.ReadyOpts) []backend.IssueData {
	if opts.MolType != "" {
		return ready
	}
	startable, err := b.startableBehindReview(ctx)
	if err != nil {
		// Fall back to FleetDB's answer: the dependents wait one more poll.
		slog.Warn("ready: could not read tasks in code review", "err", err)
		return ready
	}
	if len(startable) == 0 {
		return ready
	}
	present := make(map[string]bool, len(ready))
	for _, issue := range ready {
		present[issue.ID] = true
	}
	unlimited := opts
	unlimited.Limit = 0
	var added []backend.IssueData
	for _, issue := range filterReadyIssues(startable, unlimited) {
		if present[issue.ID] || (opts.Unassigned && issue.Assignee != "") {
			continue
		}
		added = append(added, issue)
	}
	if len(added) == 0 {
		return ready
	}
	sort.SliceStable(added, func(i, j int) bool { return added[i].Priority < added[j].Priority })
	merged := append([]backend.IssueData(nil), ready...)
	for _, issue := range added {
		// FleetDB's order is kept: an added task goes after the last ready
		// task of the same or a more urgent priority.
		at := 0
		for i, existing := range merged {
			if existing.Priority <= issue.Priority {
				at = i + 1
			}
		}
		merged = append(merged[:at], append([]backend.IssueData{issue}, merged[at:]...)...)
	}
	if opts.Limit > 0 && len(merged) > opts.Limit {
		merged = merged[:opts.Limit]
	}
	return merged
}

// withoutTasksBehindReview drops from FleetDB's blocked list the tasks that
// may start behind code review, so Ready and Blocked never both list a task.
func (b *FleetBackend) withoutTasksBehindReview(ctx context.Context, blocked []backend.IssueData) []backend.IssueData {
	if len(blocked) == 0 {
		return blocked
	}
	startable, err := b.startableBehindReview(ctx)
	if err != nil {
		slog.Warn("blocked: could not read tasks in code review", "err", err)
		return blocked
	}
	if len(startable) == 0 {
		return blocked
	}
	lifted := make(map[string]bool, len(startable))
	for _, issue := range startable {
		lifted[issue.ID] = true
	}
	out := make([]backend.IssueData, 0, len(blocked))
	for _, issue := range blocked {
		if !lifted[issue.ID] {
			out = append(out, issue)
		}
	}
	return out
}
