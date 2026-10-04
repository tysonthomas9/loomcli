package backend

import (
	"context"
	"testing"
)

func TestTouchesCodeReviewLabel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		params  UpdateParams
		current []string
		want    bool
	}{
		{"add", UpdateParams{AddLabels: []string{CodeReviewLabel}}, nil, true},
		{"remove", UpdateParams{RemoveLabels: []string{CodeReviewLabel}}, []string{CodeReviewLabel}, true},
		{"replace dropping it", UpdateParams{SetLabels: []string{"x"}}, []string{CodeReviewLabel}, true},
		{"replace adding it", UpdateParams{SetLabels: []string{CodeReviewLabel}}, nil, true},
		{"replace keeping it", UpdateParams{SetLabels: []string{"x", CodeReviewLabel}}, []string{CodeReviewLabel}, false},
		{"other labels", UpdateParams{AddLabels: []string{"x"}, RemoveLabels: []string{"y"}}, []string{CodeReviewLabel}, false},
	} {
		if got := TouchesCodeReviewLabel(tc.params, tc.current); got != tc.want {
			t.Errorf("%s: TouchesCodeReviewLabel = %v, want %v", tc.name, got, tc.want)
		}
	}
}

type updateRecorder struct {
	IssueBackend
	updates []UpdateParams
}

func (u *updateRecorder) Update(_ context.Context, _ string, params UpdateParams) error {
	u.updates = append(u.updates, params)
	return nil
}

func TestMarkCodeReviewPutsTheTaskInReviewWithTheLabel(t *testing.T) {
	issues := &updateRecorder{}
	if err := MarkCodeReview(context.Background(), issues, "T-1", "loom"); err != nil {
		t.Fatal(err)
	}
	if len(issues.updates) != 1 || *issues.updates[0].Status != "review" || !HasCodeReviewLabel(issues.updates[0].AddLabels) || issues.updates[0].Actor != "loom" {
		t.Fatalf("updates = %+v, want status review with the code-review label", issues.updates)
	}
}
