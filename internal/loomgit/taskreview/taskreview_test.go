package taskreview

import (
	"context"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

func TestDecideFollowsTheD29TaskStates(t *testing.T) {
	code := review.TaskRevision{ChangeID: "c1", Number: 1}
	approved := func(r review.TaskRevision) review.TaskRevision { r.Verdict = "approve"; return r }
	for _, tc := range []struct {
		name      string
		revisions []review.TaskRevision
		want      Decision
	}{
		{"no revision yet", nil, Wait},
		{"code awaiting a verdict", []review.TaskRevision{code}, Wait},
		{"approved, apply held", []review.TaskRevision{approved(code)}, Wait},
		{"approved and applied", []review.TaskRevision{func() review.TaskRevision { r := approved(code); r.Applied = true; return r }()}, CloseApproved},
		{"approved, PR opened", []review.TaskRevision{func() review.TaskRevision { r := approved(code); r.PRNumber = 4; return r }()}, CloseApproved},
		{"approved, no provider", []review.TaskRevision{func() review.TaskRevision { r := approved(code); r.PublishStatus = "not_published"; return r }()}, CloseApproved},
		{"apply rebuilt it (carried)", []review.TaskRevision{approved(code), {ChangeID: "c1", Number: 2, Verdict: "carried", Applied: true}}, CloseApproved},
		{"policy approval applied", []review.TaskRevision{{ChangeID: "c1", Number: 1, Verdict: "policy", FollowStatus: "applied"}}, CloseApproved},
		{"rejected", []review.TaskRevision{{ChangeID: "c1", Number: 1, Verdict: "reject"}}, Reopen},
		{"no changes", []review.TaskRevision{{ChangeID: "c1", Number: 1, NoChanges: true}}, CloseNoChanges},
		{"retry after no changes has code", []review.TaskRevision{{ChangeID: "c1", Number: 1, NoChanges: true}, {ChangeID: "c1", Number: 2}}, Wait},
		{"rejected, then a new attempt", []review.TaskRevision{{ChangeID: "c1", Number: 1, Verdict: "reject"}, {ChangeID: "c1", Number: 2}}, Wait},
		{"two repos, one still waiting", []review.TaskRevision{{ChangeID: "c1", Number: 1, Verdict: "approve", Applied: true}, {ChangeID: "c2", Number: 1}}, Wait},
		{"two repos, one empty one applied", []review.TaskRevision{{ChangeID: "c1", Number: 1, Verdict: "approve", Applied: true}, {ChangeID: "c2", Number: 1, NoChanges: true}}, CloseApproved},
		{"two repos, one rejected", []review.TaskRevision{{ChangeID: "c1", Number: 1, Verdict: "approve", Applied: true}, {ChangeID: "c2", Number: 1, Verdict: "reject"}}, Reopen},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Decide(tc.revisions); got != tc.want {
				t.Fatalf("Decide = %q, want %q", got, tc.want)
			}
		})
	}
}

type fakeIssues struct {
	backend.IssueBackend
	issue   *backend.IssueDetailData
	updates []backend.UpdateParams
	closed  []string
}

func (f *fakeIssues) Get(context.Context, string) (*backend.IssueDetailData, error) {
	return f.issue, nil
}

func (f *fakeIssues) Update(_ context.Context, _ string, params backend.UpdateParams) error {
	f.updates = append(f.updates, params)
	return nil
}

func (f *fakeIssues) Close(_ context.Context, _ string, params backend.CloseParams) (*backend.CloseResult, error) {
	f.closed = append(f.closed, params.Reason)
	return &backend.CloseResult{}, nil
}

type fakeRevisions []review.TaskRevision

func (f fakeRevisions) TaskRevisionsForLead(context.Context, string, string, string) ([]review.TaskRevision, error) {
	return f, nil
}

func issueWith(status string, labels ...string) *backend.IssueDetailData {
	return &backend.IssueDetailData{IssueData: backend.IssueData{ID: "T", Status: status, Labels: labels}}
}

func TestSettleTaskClosesApprovedAndReopensRejectedCodeReviews(t *testing.T) {
	ctx := context.Background()
	applied := fakeRevisions{{ChangeID: "c1", Number: 1, Verdict: "approve", Applied: true}}
	rejected := fakeRevisions{{ChangeID: "c1", Number: 1, Verdict: "reject"}}

	issues := &fakeIssues{issue: issueWith("review", "x", backend.CodeReviewLabel)}
	if got, err := SettleTask(ctx, issues, applied, "W", "T"); err != nil || got != CloseApproved {
		t.Fatalf("approved: %q %v", got, err)
	}
	wantLabelOff := []backend.UpdateParams{{RemoveLabels: []string{backend.CodeReviewLabel}}}
	if !reflect.DeepEqual(issues.updates, wantLabelOff) || !reflect.DeepEqual(issues.closed, []string{"Approved: code applied"}) {
		t.Fatalf("approved: updates %+v closed %v, want the label removed, then closed", issues.updates, issues.closed)
	}

	issues = &fakeIssues{issue: issueWith("review", backend.CodeReviewLabel)}
	if got, err := SettleTask(ctx, issues, rejected, "W", "T"); err != nil || got != Reopen {
		t.Fatalf("rejected: %q %v", got, err)
	}
	if len(issues.updates) != 1 || *issues.updates[0].Status != "open" ||
		!reflect.DeepEqual(issues.updates[0].RemoveLabels, []string{backend.CodeReviewLabel}) || len(issues.closed) != 0 {
		t.Fatalf("rejected: updates %+v closed %v, want open without the label", issues.updates, issues.closed)
	}

	issues = &fakeIssues{issue: issueWith("review", backend.CodeReviewLabel)}
	if got, err := SettleTask(ctx, issues, fakeRevisions{{ChangeID: "c1", Number: 1, NoChanges: true}}, "W", "T"); err != nil || got != CloseNoChanges {
		t.Fatalf("no changes: %q %v", got, err)
	}
	if !reflect.DeepEqual(issues.closed, []string{"No changes"}) {
		t.Fatalf("no changes: closed %v", issues.closed)
	}
}

// Plan review is unchanged: a task in review without the code-review label
// (a planner's plan) is never touched, nor is a task no longer in review.
func TestSettleTaskLeavesPlanReviewAndOtherStatusesAlone(t *testing.T) {
	ctx := context.Background()
	rejected := fakeRevisions{{ChangeID: "c1", Number: 1, Verdict: "reject"}}
	for _, issue := range []*backend.IssueDetailData{
		issueWith("review"),
		issueWith("review", "needs-revision"),
		issueWith("closed", backend.CodeReviewLabel),
		issueWith("in_progress", backend.CodeReviewLabel),
		nil,
	} {
		issues := &fakeIssues{issue: issue}
		if got, err := SettleTask(ctx, issues, rejected, "W", "T"); err != nil || got != Wait || len(issues.updates)+len(issues.closed) != 0 {
			t.Fatalf("issue %+v: %q %v updates %+v closed %v, want untouched", issue, got, err, issues.updates, issues.closed)
		}
	}
}

func TestSettleTaskWaitsWhileCodeAwaitsReview(t *testing.T) {
	issues := &fakeIssues{issue: issueWith("review", backend.CodeReviewLabel)}
	got, err := SettleTask(context.Background(), issues, fakeRevisions{{ChangeID: "c1", Number: 1, Verdict: "approve"}}, "W", "T")
	if err != nil || got != Wait || len(issues.updates)+len(issues.closed) != 0 {
		t.Fatalf("held approval: %q %v updates %+v closed %v, want the task kept in review", got, err, issues.updates, issues.closed)
	}
}
