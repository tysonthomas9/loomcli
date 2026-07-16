package git

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

func TestMapPRListGhState(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"all", "all"},
		{"open", "open"},
		{"review", "open"},
		{"merged", "merged"},
		{"closed", "closed"},
		{"", "all"},
		{" OPEN ", "open"},
	}
	for _, tc := range tests {
		if got := mapPRListGhState(tc.in); got != tc.want {
			t.Errorf("mapPRListGhState(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizePRListLimit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit int
		want  int
	}{
		{name: "absent", limit: 0, want: defaultPRListLimit},
		{name: "legacy gh default", limit: 30, want: defaultPRListLimit},
		{name: "explicit", limit: 75, want: 75},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizePRListLimit(tc.limit); got != tc.want {
				t.Fatalf("normalizePRListLimit(%d) = %d, want %d", tc.limit, got, tc.want)
			}
		})
	}
}

func TestFilterPullRequestsForReview(t *testing.T) {
	prs := []ops.GitPullRequest{
		{Number: 1, State: "OPEN", ReviewDecision: ""},
		{Number: 2, State: "OPEN", ReviewDecision: "APPROVED"},
		{Number: 3, State: "OPEN", IsDraft: true},
		{Number: 4, State: "MERGED"},
		{Number: 5, State: "OPEN", ReviewDecision: "CHANGES_REQUESTED"},
	}
	got := FilterPullRequestsForReview(prs)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Number != 1 || got[1].Number != 5 {
		t.Fatalf("unexpected numbers: %+v", got)
	}
}
