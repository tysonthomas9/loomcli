package prreview

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestGetPullRequestDetail(t *testing.T) {
	h := newPRReviewHarness(t, true)
	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	var decoded struct {
		Success bool                  `json:"success"`
		Data    gen.PullRequestDetail `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, raw)
	}
	if !decoded.Success || decoded.Data.HeadSha != "headsha-123" {
		t.Fatalf("response = %+v, want success with head_sha", decoded)
	}
	calls := h.github.snapshot()
	if len(calls) != 1 || calls[0].path != "/repos/octocat/hello/pulls/7" {
		t.Fatalf("calls = %+v, want one PR read", calls)
	}
	if calls[0].authorization != "Bearer "+prReviewTestToken {
		t.Fatalf("authorization = %q, want bearer token", calls[0].authorization)
	}
	assertGrantActions(t, h, prReadActions)
}

func TestGetPullRequestUsesSettingsGitHubCredential(t *testing.T) {
	const settingsToken = "github-settings-token"
	h := newPRReviewHarnessWithCredential(t, true, nil, testCredentialSettings, settingsToken)
	if !h.module.connectorListAvailable() {
		t.Fatal("settings credential was not reflected in connector availability")
	}

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	calls := h.github.snapshot()
	if len(calls) != 1 || calls[0].authorization != "Bearer "+settingsToken {
		t.Fatalf("calls = %+v, want settings-token authorization", calls)
	}
	assertGrantActions(t, h, prReadActions)
}

func TestGetPullRequestEnvTokenOverridesSettings(t *testing.T) {
	const settingsToken = "github-settings-token"
	const envToken = "github-env-token"
	h := newPRReviewHarnessWithCredential(t, true, nil, testCredentialSettings, settingsToken)
	t.Setenv(webuiGitHubTokenEnv, "  "+envToken+"  ")

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	calls := h.github.snapshot()
	if len(calls) != 1 || calls[0].authorization != "Bearer "+envToken {
		t.Fatalf("calls = %+v, want trimmed env-token authorization", calls)
	}
}

func TestPRReviewWithoutGitHubCredentialFailsAndWarns(t *testing.T) {
	fallback := &fallbackAgentService{result: &ops.GitPullRequestList{}}
	h := newPRReviewHarnessWithCredential(t, true, fallback, testCredentialNone, "")

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("detail status = %d, want 503 (body %s)", status, raw)
	}
	if code := decodeErrorCode(t, raw); code != "egress_unavailable" {
		t.Fatalf("detail code = %q, want egress_unavailable", code)
	}

	status, raw = h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("list status = %d, want 200 (body %s)", status, raw)
	}
	var decoded struct {
		Data struct {
			Warnings []string `json:"warnings"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode list response: %v (body %s)", err, raw)
	}
	if !slices.Contains(decoded.Data.Warnings, connectorUnavailableWarning) {
		t.Fatalf("list warnings = %v, want %q", decoded.Data.Warnings, connectorUnavailableWarning)
	}
}

func TestSettingsGitHubCredentialPatchInvalidatesSeedAndRotates(t *testing.T) {
	const oldToken = "github-settings-token-old"
	const newToken = "github-settings-token-new"
	h := newPRReviewHarnessWithCredential(t, true, nil, testCredentialSettings, oldToken)
	cacheKey := grantSeedCacheKey(prReviewTestWorkspace, prResource("octocat", "hello"), prReadActions)

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
	if status != http.StatusOK {
		t.Fatalf("initial status = %d, want 200 (body %s)", status, raw)
	}
	if _, seeded := h.module.seeded.Load(cacheKey); !seeded {
		t.Fatal("read seed cache entry missing after initial request")
	}
	status, raw = h.patchLocalSettings(t,
		`{"runtime_credentials":{"github":{"token":"github-settings-token-new"}}}`,
		h.module.InvalidateCredentialSeeds,
	)
	if status != http.StatusOK {
		t.Fatalf("settings PATCH status = %d, want 200 (body %s)", status, raw)
	}
	if _, seeded := h.module.seeded.Load(cacheKey); seeded {
		t.Fatal("read seed cache entry survived GitHub credential PATCH")
	}

	status, raw = h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
	if status != http.StatusOK {
		t.Fatalf("rotation status = %d, want 200 (body %s)", status, raw)
	}
	calls := h.github.snapshot()
	if len(calls) != 2 {
		t.Fatalf("calls = %+v, want initial and post-PATCH reads", calls)
	}
	if calls[0].authorization != "Bearer "+oldToken {
		t.Fatalf("initial authorization = %q, want old token", calls[0].authorization)
	}
	if calls[1].authorization != "Bearer "+newToken {
		t.Fatalf("post-PATCH authorization = %q, want new token", calls[1].authorization)
	}
}

func TestSettingsCredentialResealsAfterVaultKeyChanges(t *testing.T) {
	const token = "github-settings-token"
	h := newPRReviewHarnessWithCredential(t, true, nil, testCredentialSettings, token)
	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
	if status != http.StatusOK {
		t.Fatalf("initial status = %d, want 200 (body %s)", status, raw)
	}

	newDataDir := t.TempDir()
	h.dataDir = newDataDir
	h.setSettingsGitHubToken(t, token)
	h.rebuildWithDataDir(t, newDataDir)
	status, raw = h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
	if status != http.StatusOK {
		t.Fatalf("status after vault-key change = %d, want 200 (body %s)", status, raw)
	}
	calls := h.github.snapshot()
	if len(calls) != 2 || calls[1].authorization != "Bearer "+token {
		t.Fatalf("calls after vault-key change = %+v, want dispatch with current token", calls)
	}
	sealed, err := h.store.Connectors().ResolveOutboundCredentialSealed(
		context.Background(), prReviewTestWorkspace, connectorID,
	)
	if err != nil {
		t.Fatalf("ResolveOutboundCredentialSealed: %v", err)
	}
	if _, err := h.module.dispatcher.Vault.Unseal(
		sealed, connector.CredentialAAD(prReviewTestWorkspace, connectorID),
	); err != nil {
		t.Fatalf("credential was not re-sealed under the replacement vault key: %v", err)
	}
}

func TestCredentialInvalidationDuringEnsureUsesNewToken(t *testing.T) {
	const oldToken = "github-settings-token-old"
	const newToken = "github-settings-token-new"
	h := newPRReviewHarnessWithCredential(t, true, nil, testCredentialSettings, oldToken)
	cacheKey := grantSeedCacheKey(prReviewTestWorkspace, prResource("octocat", "hello"), prReadActions)
	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
	if status != http.StatusOK {
		t.Fatalf("initial status = %d, want 200 (body %s)", status, raw)
	}
	h.module.InvalidateCredentialSeeds()

	var invalidate sync.Once
	h.module.beforeCredentialSeedCommit = func() {
		invalidate.Do(func() {
			h.setSettingsGitHubToken(t, newToken)
			h.module.InvalidateCredentialSeeds()
		})
	}
	status, raw = h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
	if status != http.StatusOK {
		t.Fatalf("status after raced invalidation = %d, want 200 (body %s)", status, raw)
	}
	calls := h.github.snapshot()
	if len(calls) != 2 || calls[1].authorization != "Bearer "+newToken {
		t.Fatalf("calls after raced invalidation = %+v, want new-token dispatch", calls)
	}
	if _, seeded := h.module.seeded.Load(cacheKey); !seeded {
		t.Fatal("new credential seed was not cached")
	}
	sealed, err := h.store.Connectors().ResolveOutboundCredentialSealed(
		context.Background(), prReviewTestWorkspace, connectorID,
	)
	if err != nil {
		t.Fatalf("ResolveOutboundCredentialSealed: %v", err)
	}
	plain, err := h.module.dispatcher.Vault.Unseal(
		sealed, connector.CredentialAAD(prReviewTestWorkspace, connectorID),
	)
	if err != nil {
		t.Fatalf("Unseal raced credential: %v", err)
	}
	if string(plain) != newToken {
		t.Fatalf("stored credential = %q, want new token", plain)
	}
}

func TestDaytonaCredentialPatchDoesNotInvalidatePRReviewSeeds(t *testing.T) {
	h := newPRReviewHarnessWithCredential(t, true, nil, testCredentialSettings, "github-settings-token")
	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
	if status != http.StatusOK {
		t.Fatalf("initial status = %d, want 200 (body %s)", status, raw)
	}
	cacheKey := grantSeedCacheKey(prReviewTestWorkspace, prResource("octocat", "hello"), prReadActions)
	invalidations := 0
	status, raw = h.patchLocalSettings(t,
		`{"runtime_credentials":{"daytona":{"api_key":"dtn-new"}}}`,
		func() {
			invalidations++
			h.module.InvalidateCredentialSeeds()
		},
	)
	if status != http.StatusOK {
		t.Fatalf("settings PATCH status = %d, want 200 (body %s)", status, raw)
	}
	if invalidations != 0 {
		t.Fatalf("PR-review seed invalidations = %d, want 0", invalidations)
	}
	if _, seeded := h.module.seeded.Load(cacheKey); !seeded {
		t.Fatal("Daytona-only PATCH cleared PR-review seed cache")
	}
	rotations, err := h.store.ConnectorCalls().ListByBinding(
		context.Background(), prReviewTestWorkspace, connector.RotationAuditBindingID, store.ConnectorCallFilter{},
	)
	if err != nil {
		t.Fatalf("list connector rotations: %v", err)
	}
	if len(rotations) != 0 {
		t.Fatalf("connector rotations after Daytona-only PATCH = %d, want 0", len(rotations))
	}
}

func TestListPullRequestsConnector(t *testing.T) {
	h := newPRReviewHarness(t, true)
	h.github.setListPayload("octocat", "hello", []map[string]any{
		{
			"number":     11,
			"state":      "open",
			"title":      "First PR",
			"draft":      false,
			"merged":     false,
			"updated_at": "2026-07-13T13:00:00Z",
			"user":       map[string]any{"login": "octocat"},
			"head":       map[string]any{"sha": "head-11", "ref": "feature/one"},
			"base":       map[string]any{"sha": "base-11", "ref": "main"},
		},
		{
			"number": 12,
			"state":  "open",
			"title":  "Draft PR",
			"draft":  true,
			"merged": false,
			"head":   map[string]any{"sha": "head-12", "ref": "feature/two"},
			"base":   map[string]any{"sha": "base-12", "ref": "main"},
		},
		{
			"number":    13,
			"state":     "closed",
			"title":     "Merged PR",
			"draft":     false,
			"merged_at": "2026-07-13T12:00:00Z",
			"head":      map[string]any{"sha": "head-13", "ref": "feature/three"},
			"base":      map[string]any{"sha": "base-13", "ref": "main"},
		},
	})

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=all")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	var decoded struct {
		Success bool `json:"success"`
		Data    struct {
			PullRequests []ops.GitPullRequest `json:"pull_requests"`
			Warnings     []string             `json:"warnings,omitempty"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, raw)
	}
	if !decoded.Success || len(decoded.Data.PullRequests) != 3 {
		t.Fatalf("response = %+v, want success with three PRs", decoded)
	}
	first := decoded.Data.PullRequests[0]
	if first.Number != 11 || first.URL != "https://github.com/octocat/hello/pull/11" || first.RepoName != "octocat/hello" {
		t.Fatalf("first PR = %+v, want URL and GitHub repo name populated", first)
	}
	if first.SourceRepo != "hello" || first.SourceRepo == first.RepoName {
		t.Fatalf("first PR source_repo = %q, want workspace repo name %q", first.SourceRepo, "hello")
	}
	// The frontend filters on the UPPERCASE state the gh path emits
	// (isOpenPr: pr.state === "OPEN"). GitHub REST returns lowercase, so the
	// connector list MUST normalize or the PR list renders empty.
	if first.State != "OPEN" {
		t.Fatalf("first PR state = %q, want %q (frontend keys open rows off this)", first.State, "OPEN")
	}
	if first.AuthorLogin != "octocat" || first.UpdatedAt != "2026-07-13T13:00:00Z" {
		t.Fatalf("first PR author/update = %q/%q, want connector list fields", first.AuthorLogin, first.UpdatedAt)
	}
	if got := decoded.Data.PullRequests[1]; !got.IsDraft || got.HeadRefName != "feature/two" || got.BaseRefName != "main" {
		t.Fatalf("second PR = %+v, want draft/head/base mapped", got)
	}
	if got := decoded.Data.PullRequests[2]; got.State != "MERGED" {
		t.Fatalf("third PR state = %q, want %q", got.State, "MERGED")
	}
	calls := h.github.snapshot()
	if len(calls) != 1 || calls[0].path != "/repos/octocat/hello/pulls" {
		t.Fatalf("calls = %+v, want one short-page PR list", calls)
	}
	for _, want := range []string{"state=all", "per_page=100", "page=1"} {
		if !strings.Contains(calls[0].query, want) {
			t.Fatalf("short-page query %q missing %q", calls[0].query, want)
		}
	}
	assertGrantActions(t, h, prReadActions)
}

func TestSSHRemoteAuthorizesAndListsPullRequests(t *testing.T) {
	h := newPRReviewHarness(t, true)
	h.updateRepoRemote(t, "hello", "ssh://git@github.com/octocat/hello.git")
	h.github.setListPayload("octocat", "hello", []map[string]any{{
		"number": 7,
		"state":  "open",
		"title":  "SSH remote PR",
	}})

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK || !bytes.Contains(raw, []byte(`"number":7`)) {
		t.Fatalf("list status = %d, body = %s", status, raw)
	}
	status, raw = h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
	if status != http.StatusOK {
		t.Fatalf("detail status = %d, want authorized 200 (body %s)", status, raw)
	}
}

func TestListPullRequestsWarnsForUnparseableRemote(t *testing.T) {
	fallback := &fallbackAgentService{result: &ops.GitPullRequestList{}}
	h := newPRReviewHarnessWithAgent(t, true, fallback)
	h.updateRepoRemote(t, "hello", "not a repository URL")

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	if !fallback.called {
		t.Fatal("expected gh fallback when no remote is connector-eligible")
	}
	var decoded struct {
		Data struct {
			Warnings []string `json:"warnings"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode: %v (body %s)", err, raw)
	}
	if len(decoded.Data.Warnings) != 1 || !strings.Contains(decoded.Data.Warnings[0], "hello") ||
		!strings.Contains(decoded.Data.Warnings[0], "not a supported GitHub URL") {
		t.Fatalf("warnings = %v, want explicit unsupported-remote warning", decoded.Data.Warnings)
	}
}

func TestListPullRequestsConnectorPaginatesWithDistinctCallSeq(t *testing.T) {
	h := newPRReviewHarness(t, true)
	h.github.setListPage("octocat", "hello", 1, fakePullRequestPage(1, 100))
	h.github.setListPage("octocat", "hello", 2, fakePullRequestPage(101, 42))

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=all")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	data := decodePullRequestsResponse(t, raw)
	if len(data.PullRequests) != 142 {
		t.Fatalf("pull request count = %d, want 142", len(data.PullRequests))
	}

	calls := h.github.snapshot()
	if len(calls) != 2 {
		t.Fatalf("GitHub calls = %d, want 2", len(calls))
	}
	for i, call := range calls {
		for _, want := range []string{"state=all", "per_page=100", "page=" + strconv.Itoa(i+1)} {
			if !strings.Contains(call.query, want) {
				t.Fatalf("page %d query %q missing %q", i+1, call.query, want)
			}
		}
	}

	runID := "webui-review:user-1:octocat/hello:list:" + providers.ActionGitHubPullsList
	records, err := h.store.ConnectorCalls().ListByRun(
		context.Background(), prReviewTestWorkspace, runID, store.ConnectorCallFilter{},
	)
	if err != nil {
		t.Fatalf("list connector call audit: %v", err)
	}
	if len(records) != 2 || records[0].Seq != 0 || records[1].Seq != 1 {
		t.Fatalf("connector call records = %+v, want seq 0 and 1", records)
	}
	if records[0].CallID == records[1].CallID {
		t.Fatalf("connector page calls reused audit id %q", records[0].CallID)
	}
}

func TestListPullRequestsConnectorWarnsAtPageCap(t *testing.T) {
	h := newPRReviewHarness(t, true)
	for page := 1; page <= maxPullsListPages; page++ {
		h.github.setListPage("octocat", "hello", page, fakePullRequestPage((page-1)*pullsListPerPage+1, pullsListPerPage))
	}

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=all")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	data := decodePullRequestsResponse(t, raw)
	if len(data.PullRequests) != pullsListPerPage*maxPullsListPages {
		t.Fatalf("pull request count = %d, want capped %d", len(data.PullRequests), pullsListPerPage*maxPullsListPages)
	}
	if len(data.Warnings) != 1 || !strings.Contains(data.Warnings[0], "octocat/hello") ||
		!strings.Contains(data.Warnings[0], "truncated") {
		t.Fatalf("warnings = %v, want repo-scoped truncation warning", data.Warnings)
	}
	if calls := h.github.snapshot(); len(calls) != maxPullsListPages {
		t.Fatalf("GitHub calls = %d, want capped %d", len(calls), maxPullsListPages)
	}
}

func TestNormalizePullState(t *testing.T) {
	cases := []struct {
		state  string
		merged bool
		want   string
	}{
		{"open", false, "OPEN"},
		{"closed", false, "CLOSED"},
		{"closed", true, "MERGED"},
		{"OPEN", false, "OPEN"},
		{"", false, ""},
	}
	for _, tc := range cases {
		if got := normalizePullState(tc.state, tc.merged); got != tc.want {
			t.Fatalf("normalizePullState(%q, %v) = %q, want %q", tc.state, tc.merged, got, tc.want)
		}
	}
}

func TestListPullRequestsMergedRoutesToGh(t *testing.T) {
	fallback := &fallbackAgentService{
		result: &ops.GitPullRequestList{
			PullRequests: []ops.GitPullRequest{{Number: 9, Title: "Merged PR", State: "merged", RepoName: "octocat/hello"}},
		},
	}
	// Connector IS available, but "merged" can't be served by the pulls API, so
	// it must route straight to gh without hitting the connector list endpoint.
	h := newPRReviewHarnessWithAgent(t, true, fallback)
	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=merged")
	if status != http.StatusOK {
		t.Fatalf("status = %d (body %s)", status, raw)
	}
	if !fallback.called || fallback.state != "merged" {
		t.Fatalf("fallback = called:%v state:%q, want merged via gh", fallback.called, fallback.state)
	}
	for _, c := range h.github.snapshot() {
		if strings.HasSuffix(c.path, "/pulls") {
			t.Fatalf("connector pulls list was called for merged; want gh only")
		}
	}
}

func TestListPullRequestsFallsBackToGhWhenNoConnector(t *testing.T) {
	fallback := &fallbackAgentService{
		result: &ops.GitPullRequestList{
			PullRequests: []ops.GitPullRequest{{
				Number:   21,
				Title:    "Fallback PR",
				URL:      "https://github.com/octocat/hello/pull/21",
				State:    "open",
				RepoName: "octocat/hello",
			}},
			Warnings: []string{"local gh warning"},
		},
	}
	h := newPRReviewHarnessWithAgent(t, false, fallback)

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=review")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	if !fallback.called || fallback.ws != prReviewTestWorkspace || fallback.state != "review" {
		t.Fatalf("fallback call = called:%v ws:%q state:%q, want WS review", fallback.called, fallback.ws, fallback.state)
	}
	var decoded struct {
		Success bool `json:"success"`
		Data    struct {
			PullRequests []ops.GitPullRequest `json:"pull_requests"`
			Warnings     []string             `json:"warnings,omitempty"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, raw)
	}
	if len(decoded.Data.PullRequests) != 1 || decoded.Data.PullRequests[0].Number != 21 {
		t.Fatalf("pull_requests = %+v, want fallback PR", decoded.Data.PullRequests)
	}
	if len(decoded.Data.Warnings) != 1 || decoded.Data.Warnings[0] != "local gh warning" {
		t.Fatalf("warnings = %+v, want fallback warning", decoded.Data.Warnings)
	}
}

func TestListPullRequestsConnectorFailureSurfacesWarning(t *testing.T) {
	fallback := &fallbackAgentService{
		result: &ops.GitPullRequestList{
			PullRequests: []ops.GitPullRequest{{Number: 5, RepoName: "octocat/hello", State: "OPEN"}},
		},
	}
	// Connector IS available, but its list fails for the only repo → wholesale
	// fallback to gh. The failure must be surfaced, not silent.
	h := newPRReviewHarnessWithAgent(t, true, fallback)
	h.github.setListStatus("octocat", "hello", http.StatusInternalServerError)

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("status = %d (body %s)", status, raw)
	}
	if !fallback.called {
		t.Fatal("expected gh fallback after connector failure")
	}
	var decoded struct {
		Data struct {
			PullRequests []ops.GitPullRequest `json:"pull_requests"`
			Warnings     []string             `json:"warnings"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode: %v (body %s)", err, raw)
	}
	if len(decoded.Data.Warnings) == 0 || decoded.Data.Warnings[0] != connectorUnavailableWarning {
		t.Fatalf("warnings = %v, want the connector-unavailable notice first", decoded.Data.Warnings)
	}
	hasDetail := false
	for _, wmsg := range decoded.Data.Warnings {
		if strings.Contains(wmsg, "octocat/hello") {
			hasDetail = true
		}
	}
	if !hasDetail {
		t.Fatalf("warnings %v missing the per-repo failure reason", decoded.Data.Warnings)
	}
	if len(decoded.Data.PullRequests) != 1 {
		t.Fatalf("pull_requests = %+v, want the gh fallback list", decoded.Data.PullRequests)
	}
}

func TestListPullRequestsOversizedConnectorPageSurfacesWarning(t *testing.T) {
	fallback := &fallbackAgentService{
		result: &ops.GitPullRequestList{
			PullRequests: []ops.GitPullRequest{{Number: 5, RepoName: "octocat/hello", State: "OPEN"}},
		},
	}
	h := newPRReviewHarnessWithAgent(t, true, fallback)
	h.github.setListPayload("octocat", "hello", []map[string]any{{
		"number":  7,
		"state":   "open",
		"title":   "Oversized PR",
		"padding": strings.Repeat("x", 5<<20),
	}})

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=all")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 fallback response (body %s)", status, raw)
	}
	decoded := decodePullRequestsResponse(t, raw)
	if !fallback.called || len(decoded.PullRequests) != 1 {
		t.Fatalf("fallback = %v, pull_requests = %+v; want non-empty fallback", fallback.called, decoded.PullRequests)
	}
	warningFound := false
	for _, warning := range decoded.Warnings {
		if strings.Contains(warning, "octocat/hello") &&
			strings.Contains(warning, "response exceeded 4194304 bytes") {
			warningFound = true
		}
	}
	if !warningFound {
		t.Fatalf("warnings = %v, want per-repo oversized-response warning", decoded.Warnings)
	}
}

func TestListPullRequestsPartialRepoErrorWarns(t *testing.T) {
	h := newPRReviewHarness(t, true)
	h.addRepo(t, "widgets", "https://github.com/acme/widgets")
	h.github.setListPayload("octocat", "hello", []map[string]any{
		{
			"number": 31,
			"state":  "open",
			"title":  "Good PR",
			"draft":  false,
			"head":   map[string]any{"sha": "head-31", "ref": "feature/good"},
			"base":   map[string]any{"sha": "base-31", "ref": "main"},
		},
	})
	h.github.setListStatus("acme", "widgets", http.StatusInternalServerError)

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	var decoded struct {
		Success bool `json:"success"`
		Data    struct {
			PullRequests []ops.GitPullRequest `json:"pull_requests"`
			Warnings     []string             `json:"warnings,omitempty"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, raw)
	}
	if len(decoded.Data.PullRequests) != 1 || decoded.Data.PullRequests[0].RepoName != "octocat/hello" {
		t.Fatalf("pull_requests = %+v, want only successful repo PR", decoded.Data.PullRequests)
	}
	if len(decoded.Data.Warnings) != 1 || !strings.Contains(decoded.Data.Warnings[0], "acme/widgets") {
		t.Fatalf("warnings = %+v, want failed repo warning", decoded.Data.Warnings)
	}
	calls := h.github.snapshot()
	if len(calls) != 2 {
		t.Fatalf("calls = %+v, want both repo list attempts", calls)
	}
}

func TestGetPullRequestDiff(t *testing.T) {
	h := newPRReviewHarness(t, true)
	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/diff")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	var decoded struct {
		Success bool                `json:"success"`
		Data    gen.PullRequestDiff `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, raw)
	}
	if !decoded.Success || len(decoded.Data.Files) != 1 {
		t.Fatalf("response = %+v, want success with one diff file", decoded)
	}
	if decoded.Data.Files[0].Path != "README.md" || decoded.Data.Files[0].Additions != 3 {
		t.Fatalf("diff file = %+v, want mapped path/additions", decoded.Data.Files[0])
	}
	if decoded.Data.Diff == "" {
		t.Fatalf("diff is empty")
	}
	calls := h.github.snapshot()
	if len(calls) != 2 {
		t.Fatalf("calls = %+v, want PR read then compare", calls)
	}
	if calls[0].path != "/repos/octocat/hello/pulls/7" {
		t.Fatalf("first call path = %q, want PR read", calls[0].path)
	}
	if calls[1].path != "/repos/octocat/hello/compare/main...headsha-123" {
		t.Fatalf("second call path = %q, want compare pinned to head sha", calls[1].path)
	}
	assertGrantActions(t, h, prReadActions)
}

func TestGetPullRequestUnregisteredRepoDoesNotDispatch(t *testing.T) {
	h := newPRReviewHarness(t, true)
	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/acme/widgets/7")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", status, raw)
	}
	if code := decodeErrorCode(t, raw); code != "repo_not_registered" {
		t.Fatalf("code = %q, want repo_not_registered", code)
	}
	if calls := h.github.snapshot(); len(calls) != 0 {
		t.Fatalf("upstream calls = %+v, want none", calls)
	}
}

func TestPostPullRequestReviewApprove(t *testing.T) {
	h := newPRReviewHarness(t, true)
	status, raw := h.post(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/review",
		`{"event":"approve","body":"LGTM","expected_head_sha":"headsha-123"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	var decoded struct {
		Success bool                        `json:"success"`
		Data    gen.PullRequestReviewResult `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, raw)
	}
	if !decoded.Success || decoded.Data.ReviewId == nil || *decoded.Data.ReviewId != 101 || decoded.Data.State == nil || *decoded.Data.State != "APPROVED" {
		t.Fatalf("response = %+v, want success with review id/state", decoded)
	}
	calls := h.github.snapshot()
	if len(calls) != 2 {
		t.Fatalf("calls = %+v, want liveness GET then review POST", calls)
	}
	if calls[0].method != http.MethodGet || calls[0].path != "/repos/octocat/hello/pulls/7" {
		t.Fatalf("first call = %+v, want PR liveness read", calls[0])
	}
	if calls[1].method != http.MethodPost || calls[1].path != "/repos/octocat/hello/pulls/7/reviews" {
		t.Fatalf("second call = %+v, want review POST", calls[1])
	}
	if calls[1].body["event"] != "APPROVE" || calls[1].body["body"] != "LGTM" {
		t.Fatalf("review payload = %+v, want APPROVE body", calls[1].body)
	}
	assertGrantActions(t, h, prReviewSubmissionActions)
}

func TestPostPullRequestReviewSeedsWriteGrantAfterRead(t *testing.T) {
	h := newPRReviewHarness(t, true)
	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
	if status != http.StatusOK {
		t.Fatalf("read status = %d, want 200 (body %s)", status, raw)
	}
	assertGrantActions(t, h, prReadActions)

	status, raw = h.post(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/review",
		`{"event":"approve","body":"LGTM","expected_head_sha":"headsha-123"}`)
	if status != http.StatusOK {
		t.Fatalf("review status = %d, want 200 (body %s)", status, raw)
	}
	calls := h.github.snapshot()
	if len(calls) != 3 || calls[2].method != http.MethodPost || calls[2].path != "/repos/octocat/hello/pulls/7/reviews" {
		t.Fatalf("calls = %+v, want read GET, liveness GET, review POST", calls)
	}
	want := append(slices.Clone(prReadActions), prReviewWriteAction)
	assertGrantActions(t, h, want)
}

func TestGrantSeedCacheKeyScopesCanonicalActionSet(t *testing.T) {
	resource := prResource("octocat", "hello")
	readKey := grantSeedCacheKey(prReviewTestWorkspace, resource, prReadActions)
	reordered := []string{
		providers.ActionGitHubCompareRead,
		providers.ActionGitHubPullRequestRead,
		providers.ActionGitHubPullsList,
		providers.ActionGitHubPullRequestRead,
	}
	if got := grantSeedCacheKey(prReviewTestWorkspace, resource, reordered); got != readKey {
		t.Fatalf("reordered/deduplicated read key = %q, want %q", got, readKey)
	}
	if writeKey := grantSeedCacheKey(prReviewTestWorkspace, resource, prReviewSubmissionActions); writeKey == readKey {
		t.Fatalf("read and review-submission action sets share cache key %q", readKey)
	}
}

func TestInvalidateCredentialSeedsClearsAllEntries(t *testing.T) {
	module := &Module{}
	module.seeded.Store("read", struct{}{})
	module.seeded.Store("write", struct{}{})

	module.InvalidateCredentialSeeds()

	count := 0
	module.seeded.Range(func(_, _ any) bool {
		count++
		return true
	})
	if count != 0 {
		t.Fatalf("seed cache entries after invalidation = %d, want 0", count)
	}
	if generation := module.credentialSeedGeneration.Load(); generation != 1 {
		t.Fatalf("credential seed generation = %d, want 1", generation)
	}
}

func TestPostPullRequestReviewMissingExpectedHeadShaDoesNotDispatch(t *testing.T) {
	h := newPRReviewHarness(t, true)
	status, raw := h.post(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/review",
		`{"event":"approve","body":"LGTM"}`)
	if status != http.StatusPreconditionRequired {
		t.Fatalf("status = %d, want 428 (body %s)", status, raw)
	}
	if code := decodeErrorCode(t, raw); code != "precondition_required" {
		t.Fatalf("code = %q, want precondition_required", code)
	}
	if calls := h.github.snapshot(); len(calls) != 0 {
		t.Fatalf("upstream calls = %+v, want none", calls)
	}
}

func TestPostPullRequestReviewStaleSubject(t *testing.T) {
	h := newPRReviewHarness(t, true)
	h.github.setHead("headsha-456")
	status, raw := h.post(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/review",
		`{"event":"approve","body":"LGTM","expected_head_sha":"headsha-123"}`)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", status, raw)
	}
	if code := decodeErrorCode(t, raw); code != "stale_subject" {
		t.Fatalf("code = %q, want stale_subject", code)
	}
	calls := h.github.snapshot()
	if len(calls) != 1 || calls[0].method != http.MethodGet || calls[0].path != "/repos/octocat/hello/pulls/7" {
		t.Fatalf("calls = %+v, want only liveness GET", calls)
	}
}

func TestPostPullRequestReviewUnregisteredRepoDoesNotDispatch(t *testing.T) {
	h := newPRReviewHarness(t, true)
	status, raw := h.post(t, "/api/workspaces/WS/pull-requests/acme/widgets/7/review",
		`{"event":"approve","body":"LGTM","expected_head_sha":"headsha-123"}`)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", status, raw)
	}
	if code := decodeErrorCode(t, raw); code != "repo_not_registered" {
		t.Fatalf("code = %q, want repo_not_registered", code)
	}
	if calls := h.github.snapshot(); len(calls) != 0 {
		t.Fatalf("upstream calls = %+v, want none", calls)
	}
}
