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
