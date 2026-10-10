package backend

import (
	"context"
	"fmt"
	"slices"
)

// CodeReviewLabel marks a task in status review because its code awaits
// review (D29), not its plan. Only Loom sets and clears it: a finished attempt
// with code puts the task in review, and Approve (once applied) or Reject
// takes it out. Agents may not touch it.
const CodeReviewLabel = "code-review"

// HasCodeReviewLabel reports whether labels carry CodeReviewLabel.
func HasCodeReviewLabel(labels []string) bool {
	return slices.Contains(labels, CodeReviewLabel)
}

// TouchesCodeReviewLabel reports whether an update of a task carrying
// current labels would add, remove or replace CodeReviewLabel.
func TouchesCodeReviewLabel(params UpdateParams, current []string) bool {
	if HasCodeReviewLabel(params.AddLabels) || HasCodeReviewLabel(params.RemoveLabels) {
		return true
	}
	return params.SetLabels != nil && HasCodeReviewLabel(params.SetLabels) != HasCodeReviewLabel(current)
}

// MarkCodeReview keeps a task whose finished attempt has code open, in review,
// instead of closing it (D29). The run's claim is released by the run's own
// exit path, as for any finished run.
func MarkCodeReview(ctx context.Context, issues IssueBackend, task, actor string) error {
	status := "review"
	if err := issues.Update(ctx, task, UpdateParams{Actor: actor, Status: &status, AddLabels: []string{CodeReviewLabel}}); err != nil {
		return fmt.Errorf("put task %s in review: %w", task, err)
	}
	return nil
}

// A task whose code awaits review has a finished agent and a frozen
// revision, so a dependent in its epic starts on that revision instead of
// waiting for the review (Tyson, 2026-10-09). A dependent with another open
// blocker, in another epic or in no epic cannot be built on the blocker's
// code alone, so it waits until its blockers close.

// SharesCodeReviewBase reports whether a task in parent, whose only open
// blocker is blocker (in blockerParent), starts on that blocker's code.
func SharesCodeReviewBase(parent, blockerParent string, blockerInCodeReview bool) bool {
	return parent != "" && parent == blockerParent && blockerInCodeReview
}

// CodeReviewBase returns the task whose frozen revision taskID is built on
// while its code awaits review: taskID's only open blocker, in code review,
// in taskID's epic. found is false for any other task.
func CodeReviewBase(ctx context.Context, issues IssueBackend, taskID string) (string, bool, error) {
	task, err := issues.Get(ctx, taskID)
	if err != nil {
		return "", false, fmt.Errorf("read task %s: %w", taskID, err)
	}
	if task.Parent == "" {
		return "", false, nil
	}
	var open []string
	for _, dependency := range task.Dependencies {
		if dependency.Type == "blocks" && dependency.IssueID == taskID && dependency.Status != "closed" {
			open = append(open, dependency.DependsOnID)
		}
	}
	if len(open) != 1 {
		return "", false, nil
	}
	blocker, err := issues.Get(ctx, open[0])
	if err != nil {
		return "", false, fmt.Errorf("read blocker %s: %w", open[0], err)
	}
	inReview := blocker.Status == "review" && HasCodeReviewLabel(blocker.Labels)
	if !SharesCodeReviewBase(task.Parent, blocker.Parent, inReview) {
		return "", false, nil
	}
	return blocker.ID, true, nil
}
