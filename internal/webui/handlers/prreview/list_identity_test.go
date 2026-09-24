package prreview

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

func listPullRequestsForTest(t *testing.T, h *prReviewHarness) (pullRequestsData, map[string]any) {
	t.Helper()
	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	data := decodePullRequestsResponse(t, raw)
	var generic struct {
		Data struct {
			PullRequests []map[string]any `json:"pull_requests"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("decode generic response: %v", err)
	}
	var first map[string]any
	if len(generic.Data.PullRequests) > 0 {
		first = generic.Data.PullRequests[0]
	}
	return data, first
}

func TestListPullRequestsCarriesExternalPRIdentity(t *testing.T) {
	h := newPRReviewHarness(t, true)
	h.github.setListPayload("octocat", "hello", []map[string]any{{
		"number":   31,
		"node_id":  "PR_kwDOexternal31",
		"html_url": "https://github.com/octocat/hello/pull/31",
		"state":    "open",
		"title":    "Contributor PR not tracked by Loom",
		"head":     map[string]any{"sha": "abc123def456", "ref": "contrib"},
		"base":     map[string]any{"sha": "base", "ref": "main"},
	}})

	data, raw := listPullRequestsForTest(t, h)
	if len(data.PullRequests) != 1 {
		t.Fatalf("pull requests = %+v, want one", data.PullRequests)
	}
	pr := data.PullRequests[0]
	if pr.PRKey != "github:octocat/hello#31" {
		t.Errorf("pr_key = %q, want github:octocat/hello#31", pr.PRKey)
	}
	if pr.NodeID != "PR_kwDOexternal31" {
		t.Errorf("node_id = %q", pr.NodeID)
	}
	if pr.HeadSHA != "abc123def456" {
		t.Errorf("head_sha = %q", pr.HeadSHA)
	}
	if pr.URL != "https://github.com/octocat/hello/pull/31" {
		t.Errorf("url = %q", pr.URL)
	}
	// Assert the wire field names, not just the Go struct round trip.
	for field, want := range map[string]string{
		"pr_key":   "github:octocat/hello#31",
		"node_id":  "PR_kwDOexternal31",
		"head_sha": "abc123def456",
		"url":      "https://github.com/octocat/hello/pull/31",
	} {
		if got := raw[field]; got != want {
			t.Errorf("JSON %s = %v, want %q", field, got, want)
		}
	}
}

func TestListPullRequestsForkPRUsesBaseRepoKey(t *testing.T) {
	h := newPRReviewHarness(t, true)
	h.github.setListPayload("octocat", "hello", []map[string]any{{
		"number":   32,
		"node_id":  "PR_kwDOfork32",
		"html_url": "https://github.com/octocat/hello/pull/32",
		"state":    "open",
		"title":    "Fork PR",
		"user":     map[string]any{"login": "forker"},
		"head": map[string]any{
			"sha":   "forkhead",
			"ref":   "patch-1",
			"label": "forker:patch-1",
			"repo": map[string]any{
				"name":      "hello-fork",
				"full_name": "forker/hello-fork",
				"owner":     map[string]any{"login": "forker"},
			},
		},
		"base": map[string]any{
			"sha": "base",
			"ref": "main",
			"repo": map[string]any{
				"name":      "hello",
				"full_name": "octocat/hello",
				"owner":     map[string]any{"login": "octocat"},
			},
		},
	}})

	data, _ := listPullRequestsForTest(t, h)
	if len(data.PullRequests) != 1 {
		t.Fatalf("pull requests = %+v, want one", data.PullRequests)
	}
	pr := data.PullRequests[0]
	if pr.PRKey != "github:octocat/hello#32" {
		t.Fatalf("pr_key = %q, want base-repo key github:octocat/hello#32", pr.PRKey)
	}
	if pr.RepoName != "octocat/hello" || pr.HeadSHA != "forkhead" || pr.HeadRefName != "patch-1" {
		t.Fatalf("fork PR = %+v, want base repo name and fork head sha/ref", pr)
	}
}

func TestListPullRequestsMixedCaseRemoteYieldsLowercaseKey(t *testing.T) {
	h := newPRReviewHarness(t, true)
	h.updateRepoRemote(t, "hello", "https://github.com/Octo/Hello.git")
	h.github.setListPayload("Octo", "Hello", []map[string]any{{
		"number":   33,
		"node_id":  "PR_kwDOmixed33",
		"html_url": "https://github.com/Octo/Hello/pull/33",
		"state":    "open",
		"title":    "Mixed case remote",
		"head":     map[string]any{"sha": "mixedhead", "ref": "feat"},
		"base":     map[string]any{"sha": "base", "ref": "main"},
	}})

	data, _ := listPullRequestsForTest(t, h)
	if len(data.PullRequests) != 1 {
		t.Fatalf("pull requests = %+v (warnings %v), want one", data.PullRequests, data.Warnings)
	}
	pr := data.PullRequests[0]
	if pr.PRKey != "github:octo/hello#33" {
		t.Fatalf("pr_key = %q, want lowercase github:octo/hello#33", pr.PRKey)
	}
	if pr.URL != "https://github.com/Octo/Hello/pull/33" {
		t.Fatalf("url = %q, want GitHub html_url verbatim", pr.URL)
	}
}

func TestListPullRequestsFallsBackToConstructedURLWithoutHTMLURL(t *testing.T) {
	h := newPRReviewHarness(t, true)
	h.github.setListPayload("octocat", "hello", []map[string]any{{
		"number": 34,
		"state":  "open",
		"title":  "No html_url",
		"head":   map[string]any{"sha": "h34", "ref": "feat"},
		"base":   map[string]any{"sha": "base", "ref": "main"},
	}})

	data, raw := listPullRequestsForTest(t, h)
	if len(data.PullRequests) != 1 {
		t.Fatalf("pull requests = %+v, want one", data.PullRequests)
	}
	pr := data.PullRequests[0]
	if pr.URL != "https://github.com/octocat/hello/pull/34" {
		t.Fatalf("url = %q, want constructed fallback", pr.URL)
	}
	if pr.PRKey != "github:octocat/hello#34" || pr.HeadSHA != "h34" {
		t.Fatalf("pr = %+v, want pr_key and head_sha", pr)
	}
	if _, present := raw["node_id"]; present {
		t.Fatalf("node_id = %v, want omitted when GitHub sent none", raw["node_id"])
	}
}

func TestPullRequestFromSummaryIdentity(t *testing.T) {
	tests := []struct {
		name    string
		owner   string
		repo    string
		body    map[string]any
		wantKey string
		wantURL string
		wantSHA string
		wantID  string
	}{
		{
			name:    "html url preferred",
			owner:   "octocat",
			repo:    "hello",
			body:    map[string]any{"number": float64(5), "htmlUrl": "https://github.com/octocat/hello/pull/5", "nodeId": "PR_5", "headSha": "s5"},
			wantKey: "github:octocat/hello#5",
			wantURL: "https://github.com/octocat/hello/pull/5",
			wantSHA: "s5",
			wantID:  "PR_5",
		},
		{
			name:    "empty html url falls back",
			owner:   "Octo",
			repo:    "Hello",
			body:    map[string]any{"number": 6, "htmlUrl": ""},
			wantKey: "github:octo/hello#6",
			wantURL: "https://github.com/Octo/Hello/pull/6",
		},
		{
			name:    "nil identity fields",
			owner:   "octocat",
			repo:    "hello",
			body:    map[string]any{"number": 8, "nodeId": nil, "htmlUrl": nil},
			wantKey: "github:octocat/hello#8",
			wantURL: "https://github.com/octocat/hello/pull/8",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pullRequestFromSummary(tc.owner, tc.repo, "hello", tc.body)
			if got.PRKey != tc.wantKey || got.URL != tc.wantURL || got.HeadSHA != tc.wantSHA || got.NodeID != tc.wantID {
				t.Fatalf("got key=%q url=%q sha=%q id=%q, want key=%q url=%q sha=%q id=%q",
					got.PRKey, got.URL, got.HeadSHA, got.NodeID,
					tc.wantKey, tc.wantURL, tc.wantSHA, tc.wantID)
			}
		})
	}
}

func TestListPullRequestsGhFallbackPreservesIdentity(t *testing.T) {
	fallback := &fallbackAgentService{
		result: &ops.GitPullRequestList{
			PullRequests: []ops.GitPullRequest{{
				Number:   21,
				PRKey:    "github:octocat/hello#21",
				NodeID:   "PR_kwDOgh21",
				HeadSHA:  "ghhead21",
				URL:      "https://github.com/octocat/hello/pull/21",
				State:    "OPEN",
				RepoName: "octocat/hello",
			}},
		},
	}
	h := newPRReviewHarnessWithAgent(t, false, fallback)

	_, raw := listPullRequestsForTest(t, h)
	for field, want := range map[string]string{
		"pr_key":   "github:octocat/hello#21",
		"node_id":  "PR_kwDOgh21",
		"head_sha": "ghhead21",
	} {
		if got := raw[field]; got != want {
			t.Errorf("JSON %s = %v, want %q", field, got, want)
		}
	}
}
