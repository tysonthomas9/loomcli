package service

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// D29 / P1.26: only Loom's Approve/Reject change the code-review label; the
// issue API refuses every caller that would add, remove or replace it.
func TestIssueAPIRefusesCodeReviewLabelEdits(t *testing.T) {
	inReview := &backend.IssueDetailData{IssueData: backend.IssueData{ID: "i-1", Status: "review", Labels: []string{"x", backend.CodeReviewLabel}}}
	for _, tc := range []struct {
		name   string
		issue  *backend.IssueDetailData
		params PatchIssueParams
	}{
		{"add", nil, PatchIssueParams{AddLabels: []string{backend.CodeReviewLabel}}},
		{"remove", inReview, PatchIssueParams{RemoveLabels: []string{backend.CodeReviewLabel}}},
		{"replace dropping it", inReview, PatchIssueParams{SetLabels: []string{"x"}}},
		{"clear all labels", inReview, PatchIssueParams{SetLabels: []string{}}},
		{"replace adding it", &backend.IssueDetailData{IssueData: backend.IssueData{ID: "i-1"}}, PatchIssueParams{SetLabels: []string{backend.CodeReviewLabel}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fb := &fakeIssueBackend{getResult: tc.issue}
			tc.params.IssueID = "i-1"
			err := newServiceWithFake(fb).PatchIssue(context.Background(), tc.params)
			var sErr *ServiceError
			if !errors.As(err, &sErr) || sErr.Kind != KindConflict || len(fb.updateCalls) != 0 {
				t.Fatalf("err = %v updates = %d, want a conflict and no update", err, len(fb.updateCalls))
			}
		})
	}

	fb := &fakeIssueBackend{getResult: inReview}
	if err := newServiceWithFake(fb).PatchIssue(context.Background(), PatchIssueParams{IssueID: "i-1",
		AddLabels: []string{"bug"}, SetLabels: nil}); err != nil || len(fb.updateCalls) != 1 {
		t.Fatalf("other label edits must pass: %v (%d updates)", err, len(fb.updateCalls))
	}
	fb = &fakeIssueBackend{getResult: inReview}
	if err := newServiceWithFake(fb).PatchIssue(context.Background(), PatchIssueParams{IssueID: "i-1",
		SetLabels: []string{"y", backend.CodeReviewLabel}}); err != nil || len(fb.updateCalls) != 1 {
		t.Fatalf("a replace keeping the label must pass: %v (%d updates)", err, len(fb.updateCalls))
	}

	fb = &fakeIssueBackend{}
	_, err := newServiceWithFake(fb).CreateIssue(context.Background(), CreateIssueParams{Title: "t", IssueType: "task",
		Labels: []string{backend.CodeReviewLabel}})
	var sErr *ServiceError
	if !errors.As(err, &sErr) || sErr.Kind != KindValidation || len(fb.createParams) != 0 {
		t.Fatalf("create with the label: %v, want a validation error and no create", err)
	}
}

// D29 / P1.26: a task in code review moves only on Approve or Reject of its
// revision; a hand-made status change, close or reopen is refused, so it can
// neither skip the review nor leave a stale code-review label.
func TestIssueAPIRefusesStatusChangesOnATaskInCodeReview(t *testing.T) {
	inReview := &backend.IssueDetailData{IssueData: backend.IssueData{ID: "i-1", Status: "review", Labels: []string{backend.CodeReviewLabel}}}
	plainReview := &backend.IssueDetailData{IssueData: backend.IssueData{ID: "i-1", Status: "review"}}
	refused := func(t *testing.T, err error, fb *fakeIssueBackend) {
		t.Helper()
		var sErr *ServiceError
		if !errors.As(err, &sErr) || sErr.Kind != KindConflict {
			t.Fatalf("err = %v, want a conflict", err)
		}
		if len(fb.updateCalls)+len(fb.closeCalls)+len(fb.reopenCalls) != 0 {
			t.Fatalf("a refused change still wrote to the backend")
		}
	}
	for _, status := range []string{"closed", "open", "in_progress"} {
		t.Run("patch to "+status, func(t *testing.T) {
			fb := &fakeIssueBackend{getResult: inReview}
			s := status
			refused(t, newServiceWithFake(fb).PatchIssue(context.Background(), PatchIssueParams{IssueID: "i-1", Status: &s}), fb)
		})
	}
	t.Run("close", func(t *testing.T) {
		fb := &fakeIssueBackend{getResult: inReview}
		_, err := newServiceWithFake(fb).CloseIssue(context.Background(), CloseIssueParams{IssueID: "i-1", Reason: "done"})
		refused(t, err, fb)
	})
	t.Run("reopen", func(t *testing.T) {
		fb := &fakeIssueBackend{getResult: inReview}
		refused(t, newServiceWithFake(fb).ReopenIssue(context.Background(), ReopenIssueParams{IssueID: "i-1"}), fb)
	})

	review := "review"
	fb := &fakeIssueBackend{getResult: inReview}
	if err := newServiceWithFake(fb).PatchIssue(context.Background(), PatchIssueParams{IssueID: "i-1", Status: &review}); err != nil || len(fb.updateCalls) != 1 {
		t.Fatalf("keeping it in review must pass: %v (%d updates)", err, len(fb.updateCalls))
	}
	closed := "closed"
	fb = &fakeIssueBackend{getResult: plainReview}
	if err := newServiceWithFake(fb).PatchIssue(context.Background(), PatchIssueParams{IssueID: "i-1", Status: &closed}); err != nil || len(fb.updateCalls) != 1 {
		t.Fatalf("plan review without the label must still close: %v (%d updates)", err, len(fb.updateCalls))
	}
	fb = &fakeIssueBackend{getResult: plainReview, closeResult: &backend.CloseResult{}}
	if _, err := newServiceWithFake(fb).CloseIssue(context.Background(), CloseIssueParams{IssueID: "i-1"}); err != nil || len(fb.closeCalls) != 1 {
		t.Fatalf("closing an unlabelled task must pass: %v (%d closes)", err, len(fb.closeCalls))
	}
}
