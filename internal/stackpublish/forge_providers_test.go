package stackpublish

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type cannedResponse struct {
	code int
	body string
}

// cannedServer answers "METHOD escaped-path" with fixed responses and 404
// otherwise; requests records every request it received.
func cannedServer(t *testing.T, routes map[string]cannedResponse) (*httptest.Server, *[]string) {
	t.Helper()
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.EscapedPath()
		requests = append(requests, key)
		response, ok := routes[key]
		if !ok {
			response = cannedResponse{http.StatusNotFound, `{"message":"404 Not found"}`}
		}
		w.WriteHeader(response.code)
		_, _ = w.Write([]byte(response.body))
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

const gitlabProjectPath = "/api/v4/projects/owner%2Frepo"

func newCannedGitLab(t *testing.T, routes map[string]cannedResponse) (*GitLabForge, *[]string) {
	server, requests := cannedServer(t, routes)
	return NewGitLabForge("token", server.Client(), server.URL+"/api/v4"), requests
}

func TestGitLabForgeFailsClosedOnUnknownResponses(t *testing.T) {
	ctx := context.Background()
	forge, _ := newCannedGitLab(t, map[string]cannedResponse{
		"GET " + gitlabProjectPath + "/merge_requests/1":              {200, `{"iid":1,"state":"reopened_somehow","sha":"abc"}`},
		"GET " + gitlabProjectPath + "/merge_trains/merge_requests/1": {200, `{"id":3,"status":"teleporting","pipeline":{"sha":"def"}}`},
		"GET " + gitlabProjectPath + "/merge_trains/merge_requests/2": {403, `{"message":"403 Forbidden"}`},
		"GET " + gitlabProjectPath + "/merge_trains/merge_requests/3": {200, `{"id":4,"status":"idle","pipeline":{"sha":"train-sha"}}`},
	})
	if _, err := forge.PullByNumber(ctx, "owner", "repo", 1); err == nil || !strings.Contains(err.Error(), "unknown state") {
		t.Fatalf("unknown MR state = %v", err)
	}
	if _, err := forge.MergeQueueHead(ctx, "owner", "repo", 1); err == nil || !strings.Contains(err.Error(), "unknown status") {
		t.Fatalf("unknown merge train status = %v", err)
	}
	if _, err := forge.MergeQueueHead(ctx, "owner", "repo", 2); err == nil {
		t.Fatal("forbidden merge train read treated as not queued")
	}
	if sha, err := forge.MergeQueueHead(ctx, "owner", "repo", 3); err != nil || sha != "train-sha" {
		t.Fatalf("merge train head = %q, %v", sha, err)
	}
	if sha, err := forge.MergeQueueHead(ctx, "owner", "repo", 4); err != nil || sha != "" {
		t.Fatalf("MR not in a train = %q, %v", sha, err)
	}
}

func TestGitLabPostDependencyStatusFailsClosed(t *testing.T) {
	ctx := context.Background()
	forge, requests := newCannedGitLab(t, map[string]cannedResponse{
		"POST " + gitlabProjectPath + "/statuses/same":  {400, `{"message":"Cannot transition status via :enqueue from :pending (Reason(s): Status cannot transition via \"enqueue\")"}`},
		"POST " + gitlabProjectPath + "/statuses/other": {400, `{"message":"Cannot transition status via :succeed from :failed (Reason(s): ...)"}`},
		"POST " + gitlabProjectPath + "/statuses/odd":   {200, `{}`},
	})
	if _, err := forge.PostDependencyStatus(ctx, "owner", "repo", "same", DependencyStatus{State: "unknown"}); err == nil || len(*requests) != 0 {
		t.Fatalf("unknown state posted: %v, %v", err, *requests)
	}
	if _, err := forge.PostDependencyStatus(ctx, "owner", "repo", "same", DependencyStatus{State: "pending"}); err != nil {
		t.Fatalf("re-posting the same pending state = %v", err)
	}
	if _, err := forge.PostDependencyStatus(ctx, "owner", "repo", "other", DependencyStatus{State: "success"}); err == nil {
		t.Fatal("refused success post treated as posted")
	}
	if _, err := forge.PostDependencyStatus(ctx, "owner", "repo", "odd", DependencyStatus{State: "success"}); err == nil {
		t.Fatal("unexpected 200 treated as posted")
	}
}

func TestGitLabDependencyEnforcement(t *testing.T) {
	ctx := context.Background()
	check := `[{"id":7,"name":"loom/dependencies","protected_branches":[{"id":1,"name":"release/*"}]}]`
	for _, test := range []struct {
		name, project, checks, want string
	}{
		{"status checks need not pass", `{"only_allow_merge_if_all_status_checks_passed":false}`, check, "not_enforced"},
		{"setting absent (Free tier)", `{"id":1}`, check, "not_enforced"},
		{"check covers another branch", `{"only_allow_merge_if_all_status_checks_passed":true}`, check, "not_enforced"},
		{"check covers all branches", `{"only_allow_merge_if_all_status_checks_passed":true}`, `[{"id":7,"name":"loom/dependencies","protected_branches":[]}]`, "not_pinned"},
		{"only another check", `{"only_allow_merge_if_all_status_checks_passed":true}`, `[{"id":8,"name":"security","protected_branches":[]}]`, "not_enforced"},
	} {
		forge, _ := newCannedGitLab(t, map[string]cannedResponse{
			"GET " + gitlabProjectPath:                             {200, test.project},
			"GET " + gitlabProjectPath + "/external_status_checks": {200, test.checks},
		})
		if got, err := forge.DependencyEnforcement(ctx, "owner", "repo", "main", 0); err != nil || got != test.want {
			t.Errorf("%s: enforcement = %q, %v; want %q", test.name, got, err, test.want)
		}
	}
	forge, _ := newCannedGitLab(t, map[string]cannedResponse{
		"GET " + gitlabProjectPath:                             {200, `{"only_allow_merge_if_all_status_checks_passed":true}`},
		"GET " + gitlabProjectPath + "/external_status_checks": {401, `{"message":"401 Unauthorized"}`},
	})
	if _, err := forge.DependencyEnforcement(ctx, "owner", "repo", "main", 0); err == nil {
		t.Fatal("unreadable status checks reported as known enforcement")
	}
}

const bitbucketRepoPath = "/2.0/repositories/owner/repo"

func newCannedBitbucket(t *testing.T, routes map[string]cannedResponse) (*BitbucketForge, *[]string) {
	server, requests := cannedServer(t, routes)
	return NewBitbucketForge("token", server.Client(), server.URL+"/2.0"), requests
}

func TestBitbucketForgeFailsClosedOnUnknownResponses(t *testing.T) {
	ctx := context.Background()
	full := "0123456789abcdef0123456789abcdef01234567"
	forge, requests := newCannedBitbucket(t, map[string]cannedResponse{
		"GET " + bitbucketRepoPath + "/pullrequests/1":                   {200, `{"id":1,"state":"ARCHIVED","source":{"branch":{"name":"b"},"commit":{"hash":"0123456789ab"}}}`},
		"GET " + bitbucketRepoPath + "/pullrequests/2":                   {200, `{"id":2,"state":"OPEN","source":{"branch":{"name":"b"},"commit":{"hash":"0123456789ab"}},"destination":{"branch":{"name":"main"}}}`},
		"GET " + bitbucketRepoPath + "/pullrequests/3":                   {200, `{"id":3,"state":"MERGED","source":{"branch":{"name":"b"},"commit":{"hash":"0123456789ab"}},"merge_commit":null}`},
		"GET " + bitbucketRepoPath + "/pullrequests/4":                   {200, `{"id":4,"state":"OPEN","source":{"branch":{"name":"b"},"commit":{"hash":"fedcba987654"}}}`},
		"GET " + bitbucketRepoPath + "/commit/0123456789ab":              {200, `{"hash":"` + full + `"}`},
		"GET " + bitbucketRepoPath + "/commit/fedcba987654":              {200, `{"hash":"` + full + `"}`},
		"GET " + bitbucketRepoPath + "/commit/" + full + "/pullrequests": {202, `{"type":"error","error":{"message":"Repository indexing in progress"}}`},
	})
	if _, err := forge.PullByNumber(ctx, "owner", "repo", 1); err == nil || !strings.Contains(err.Error(), "unknown state") {
		t.Fatalf("unknown PR state = %v", err)
	}
	if pr, err := forge.PullByNumber(ctx, "owner", "repo", 2); err != nil || pr.HeadSHA != full || pr.State != "open" || pr.Merged {
		t.Fatalf("open PR = %+v, %v", pr, err)
	}
	if _, err := forge.PullByNumber(ctx, "owner", "repo", 3); err == nil {
		t.Fatal("merged PR without a merge commit accepted")
	}
	if _, err := forge.PullByNumber(ctx, "owner", "repo", 4); err == nil {
		t.Fatal("abbreviated hash resolved to an unrelated commit accepted")
	}
	if _, err := forge.PullsForCommit(ctx, "owner", "repo", full); err == nil {
		t.Fatal("202 indexing answer treated as no associated PRs")
	}
	*requests = nil
	if _, err := forge.PostDependencyStatus(ctx, "owner", "repo", full, DependencyStatus{State: "failure"}); err == nil || len(*requests) != 0 {
		t.Fatalf("unknown state posted: %v, %v", err, *requests)
	}
}

// Bitbucket's require_passing_builds_to_merge counts passing builds of any key:
// with an unrelated green build on the head, the PR can merge while
// loom/dependencies is INPROGRESS. So it is never reported as enforcing Loom.
func TestBitbucketDependencyEnforcement(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	forge, _ := newCannedBitbucket(t, map[string]cannedResponse{
		"GET " + bitbucketRepoPath + "/branch-restrictions": {200,
			`{"values":[{"kind":"require_passing_builds_to_merge","branch_match_kind":"glob","pattern":"main","value":1}]}`},
		"GET " + bitbucketRepoPath + "/commit/" + sha + "/statuses": {200,
			`{"values":[{"key":"ci/build","state":"SUCCESSFUL"},{"key":"loom/dependencies","state":"INPROGRESS"}]}`},
	})
	if got, err := forge.DependencyEnforcement(context.Background(), "owner", "repo", "main", 0); err != nil || got != "not_enforced" {
		t.Fatalf("generic passing-builds rule = %q, %v; want not_enforced", got, err)
	}
}

func TestPullByNumberMarksMissingPRNotFound(t *testing.T) {
	ctx := context.Background()
	routes := func(path string) map[string]cannedResponse {
		return map[string]cannedResponse{"GET " + path + "/2": {http.StatusInternalServerError, `{"message":"boom"}`}}
	}
	server, _ := cannedServer(t, routes("/repos/owner/repo/pulls"))
	github := NewGitHubForge("token", server.Client(), server.URL)
	gitlab, _ := newCannedGitLab(t, routes(gitlabProjectPath+"/merge_requests"))
	bitbucket, _ := newCannedBitbucket(t, routes(bitbucketRepoPath+"/pullrequests"))
	for name, forge := range map[string]interface {
		PullByNumber(context.Context, string, string, int) (PR, error)
	}{"github": github, "gitlab": gitlab, "bitbucket": bitbucket} {
		_, err := forge.PullByNumber(ctx, "owner", "repo", 9)
		if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "404") {
			t.Fatalf("%s missing PR = %v", name, err)
		}
		if _, err := forge.PullByNumber(ctx, "owner", "repo", 2); err == nil || errors.Is(err, ErrNotFound) {
			t.Fatalf("%s server error marked not found: %v", name, err)
		}
	}
}
