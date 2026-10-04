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
