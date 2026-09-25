package prreview

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/ops"
)

func TestListPullRequestsIncludesVerifiedGitHubViewer(t *testing.T) {
	h := newPRReviewHarness(t, true)
	h.github.setViewer("tysonthomas9")
	h.github.setListPayload("octocat", "hello", []map[string]any{{
		"number": 7,
		"state":  "open",
		"title":  "Mine identity",
		"user":   map[string]any{"login": "tysonthomas9"},
		"head":   map[string]any{"sha": "abc", "ref": "feat"},
		"base":   map[string]any{"sha": "def", "ref": "main"},
	}})

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("status = %d body %s", status, raw)
	}
	data := decodePullRequestsResponse(t, raw)
	if data.GitHubViewer.Status != githubViewerStatusAvailable {
		t.Fatalf("viewer = %+v, want available", data.GitHubViewer)
	}
	if data.GitHubViewer.Login != "tysonthomas9" {
		t.Fatalf("login = %q, want tysonthomas9", data.GitHubViewer.Login)
	}
	if data.GitHubViewer.Source != githubViewerSourceConnector {
		t.Fatalf("source = %q, want connector", data.GitHubViewer.Source)
	}
	if data.GitHubViewer.ConnectorID != connectorID {
		t.Fatalf("connector_id = %q, want %q", data.GitHubViewer.ConnectorID, connectorID)
	}
	assertGrantActions(t, h, append(slices.Clone(prReadActions), providers.ActionGitHubViewerRead))

	calls := h.github.snapshot()
	var sawUser bool
	for _, c := range calls {
		if c.path == "/user" {
			sawUser = true
			break
		}
	}
	if !sawUser {
		t.Fatalf("calls = %+v, want GET /user", calls)
	}
}

func TestListPullRequestsViewerRateLimited(t *testing.T) {
	h := newPRReviewHarness(t, true)
	h.github.setViewerError(http.StatusTooManyRequests, "API rate limit exceeded", map[string]string{
		"Retry-After": "30",
	})
	h.github.setListPayload("octocat", "hello", []map[string]any{{
		"number": 1,
		"state":  "open",
		"title":  "x",
		"user":   map[string]any{"login": "other"},
		"head":   map[string]any{"sha": "a", "ref": "f"},
		"base":   map[string]any{"sha": "b", "ref": "main"},
	}})

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("status = %d body %s", status, raw)
	}
	data := decodePullRequestsResponse(t, raw)
	if data.GitHubViewer.Status != githubViewerStatusRateLimited {
		t.Fatalf("viewer = %+v, want rate_limited", data.GitHubViewer)
	}
	if data.GitHubViewer.Login != "" {
		t.Fatalf("login must be empty on rate limit, got %q", data.GitHubViewer.Login)
	}
	if data.GitHubViewer.Message == "" {
		t.Fatal("expected rate-limit message")
	}
}

func TestListPullRequestsViewerUnavailableWithoutCredential(t *testing.T) {
	fallback := &fallbackAgentService{result: &ops.GitPullRequestList{}}
	h := newPRReviewHarnessWithCredential(t, true, fallback, testCredentialNone, "")
	h.module.lookupGhUser = func(ctx context.Context) (string, error) {
		return "", errEgressUnavailable
	}

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("status = %d body %s", status, raw)
	}
	data := decodePullRequestsResponse(t, raw)
	if data.GitHubViewer.Status != githubViewerStatusUnavailable &&
		data.GitHubViewer.Status != githubViewerStatusError {
		t.Fatalf("viewer = %+v, want unavailable/error", data.GitHubViewer)
	}
	if data.GitHubViewer.Login != "" {
		t.Fatalf("login must be empty when unavailable, got %q", data.GitHubViewer.Login)
	}
}

func TestListPullRequestsViewerViaGhFallback(t *testing.T) {
	fallback := &fallbackAgentService{result: &ops.GitPullRequestList{
		PullRequests: []ops.GitPullRequest{{
			Number: 3, Title: "from gh", State: "OPEN", AuthorLogin: "tysonthomas9",
			RepoName: "octocat/hello", URL: "https://github.com/octocat/hello/pull/3",
		}},
	}}
	h := newPRReviewHarnessWithCredential(t, true, fallback, testCredentialNone, "")
	h.module.lookupGhUser = func(ctx context.Context) (string, error) {
		return "tysonthomas9", nil
	}

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("status = %d body %s", status, raw)
	}
	data := decodePullRequestsResponse(t, raw)
	if data.GitHubViewer.Status != githubViewerStatusAvailable || data.GitHubViewer.Login != "tysonthomas9" {
		t.Fatalf("viewer = %+v, want available tysonthomas9", data.GitHubViewer)
	}
	if data.GitHubViewer.Source != githubViewerSourceGhCLI {
		t.Fatalf("source = %q, want gh_cli", data.GitHubViewer.Source)
	}
}

func TestViewerCacheClearedOnCredentialInvalidate(t *testing.T) {
	h := newPRReviewHarness(t, true)
	h.github.setViewer("tysonthomas9")
	h.github.setListPayload("octocat", "hello", []map[string]any{{
		"number": 1, "state": "open", "title": "a",
		"user": map[string]any{"login": "tysonthomas9"},
		"head": map[string]any{"sha": "a", "ref": "f"},
		"base": map[string]any{"sha": "b", "ref": "main"},
	}})

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("first list status = %d body %s", status, raw)
	}
	first := decodePullRequestsResponse(t, raw)
	if first.GitHubViewer.Login != "tysonthomas9" {
		t.Fatalf("first viewer = %+v", first.GitHubViewer)
	}
	callsBefore := len(h.github.snapshot())

	// Cached: second list must not re-hit /user.
	status, raw = h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("second list status = %d body %s", status, raw)
	}
	callsMid := len(h.github.snapshot())
	userHits := 0
	for _, c := range h.github.snapshot() {
		if c.path == "/user" {
			userHits++
		}
	}
	if userHits != 1 {
		t.Fatalf("expected one cached /user hit, got %d (calls before=%d mid=%d)", userHits, callsBefore, callsMid)
	}

	h.github.setViewer("rotated-login")
	h.module.InvalidateCredentialSeeds()

	status, raw = h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("post-invalidate status = %d body %s", status, raw)
	}
	after := decodePullRequestsResponse(t, raw)
	if after.GitHubViewer.Login != "rotated-login" {
		t.Fatalf("after invalidate viewer = %+v, want rotated-login", after.GitHubViewer)
	}
}

func TestViewerCacheHonorsTTL(t *testing.T) {
	h := newPRReviewHarness(t, true)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	h.module.now = func() time.Time { return now }
	h.github.setViewer("tysonthomas9")
	h.github.setListPayload("octocat", "hello", []map[string]any{{
		"number": 1, "state": "open", "title": "a",
		"user": map[string]any{"login": "tysonthomas9"},
		"head": map[string]any{"sha": "a", "ref": "f"},
		"base": map[string]any{"sha": "b", "ref": "main"},
	}})

	if _, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open"); decodePullRequestsResponse(t, raw).GitHubViewer.Login != "tysonthomas9" {
		t.Fatal("seed viewer failed")
	}
	h.github.setViewer("after-ttl")
	now = now.Add(viewerCacheTTL + time.Second)
	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("status = %d body %s", status, raw)
	}
	got := decodePullRequestsResponse(t, raw)
	if got.GitHubViewer.Login != "after-ttl" {
		t.Fatalf("viewer = %+v, want after-ttl after TTL", got.GitHubViewer)
	}
}
