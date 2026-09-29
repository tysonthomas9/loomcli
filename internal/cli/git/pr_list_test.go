package git

import (
	"slices"
	"strconv"
	"strings"
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

func TestListPullRequestsMapsGhIdentityFields(t *testing.T) {
	ghJSON := `[
		{"id":"PR_kwDOgh7","number":7,"headRefOid":"0123456789abcdef","title":"Feature",
		 "url":"https://github.com/Octo/Hello/pull/7","state":"open","isDraft":false,
		 "headRefName":"feat","baseRefName":"main","author":{"login":"octocat"}},
		{"number":8,"title":"No identity fields","url":"not a url","state":"closed"}
	]`
	mock := NewCommandMock(t, []CommandStub{{
		Dir:  "/repo",
		Name: "gh",
		Args: []string{
			"pr", "list",
			"--state", "open",
			"--limit", strconv.Itoa(defaultPRListLimit),
			"--json", prListJSONFields,
		},
		Stdout: ghJSON,
	}})
	mock.Install()

	prs, err := ListPullRequests("/repo", "open", 0)
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("len(prs) = %d, want 2", len(prs))
	}
	first := prs[0]
	if first.NodeID != "PR_kwDOgh7" || first.HeadSHA != "0123456789abcdef" {
		t.Fatalf("first identity = node_id %q head_sha %q", first.NodeID, first.HeadSHA)
	}
	if first.PRKey != "github:octo/hello#7" {
		t.Fatalf("first pr_key = %q, want github:octo/hello#7", first.PRKey)
	}
	if first.URL != "https://github.com/Octo/Hello/pull/7" || first.State != "OPEN" {
		t.Fatalf("first = %+v, want URL verbatim and uppercased state", first)
	}
	second := prs[1]
	if second.PRKey != "" || second.NodeID != "" || second.HeadSHA != "" {
		t.Fatalf("second = %+v, want empty identity for unparseable URL", second)
	}
}

func TestPRListJSONFieldsRequestIdentity(t *testing.T) {
	fields := strings.Split(prListJSONFields, ",")
	for _, want := range []string{"id", "headRefOid", "number", "url"} {
		if !slices.Contains(fields, want) {
			t.Errorf("prListJSONFields %q missing %q", prListJSONFields, want)
		}
	}
}
