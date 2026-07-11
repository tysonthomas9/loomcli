package prreview

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

const (
	prReviewTestWorkspace = "WS"
	prReviewTestToken     = "ghp-webui-prreview-token"
)

type fakeGitHubCall struct {
	method        string
	path          string
	authorization string
	body          map[string]any
}

type fakeGitHub struct {
	mu    sync.Mutex
	calls []fakeGitHubCall

	headSha string
	state   string
	lists   map[string]fakePullList

	server *httptest.Server
}

type fakePullList struct {
	status int
	pulls  []map[string]any
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{headSha: "headsha-123", state: "open", lists: make(map[string]fakePullList)}
	g.server = httptest.NewServer(http.HandlerFunc(g.handle))
	t.Cleanup(g.server.Close)
	return g
}

func (g *fakeGitHub) handle(w http.ResponseWriter, r *http.Request) {
	bodyBytes, _ := io.ReadAll(r.Body)
	var body map[string]any
	if len(bytes.TrimSpace(bodyBytes)) > 0 {
		_ = json.Unmarshal(bodyBytes, &body)
	}
	g.mu.Lock()
	g.calls = append(g.calls, fakeGitHubCall{
		method:        r.Method,
		path:          r.URL.Path,
		authorization: r.Header.Get("Authorization"),
		body:          body,
	})
	headSha := g.headSha
	state := g.state
	list, hasList := g.lists[r.URL.Path]
	g.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && hasList:
		if list.status == 0 {
			list.status = http.StatusOK
		}
		if list.status != http.StatusOK {
			writeUpstreamJSON(w, list.status, map[string]any{"message": "list failed"})
			return
		}
		writeUpstreamJSON(w, http.StatusOK, list.pulls)
	case r.Method == http.MethodGet && r.URL.Path == "/repos/octocat/hello/pulls/7":
		writeUpstreamJSON(w, http.StatusOK, map[string]any{
			"number": 7,
			"state":  state,
			"title":  "Add review API",
			"draft":  false,
			"merged": false,
			"head":   map[string]any{"sha": headSha, "ref": "feature/review-api"},
			"base":   map[string]any{"sha": "basesha-123", "ref": "main"},
		})
	case r.Method == http.MethodGet && r.URL.Path == "/repos/octocat/hello/compare/main...headsha-123":
		writeUpstreamJSON(w, http.StatusOK, map[string]any{
			"status":        "ahead",
			"ahead_by":      2,
			"behind_by":     0,
			"total_commits": 2,
			"files": []map[string]any{
				{
					"filename":  "README.md",
					"status":    "modified",
					"additions": 3,
					"deletions": 1,
					"patch":     "@@ -1 +1 @@\n-old\n+new",
				},
			},
		})
	case r.Method == http.MethodPost && r.URL.Path == "/repos/octocat/hello/pulls/7/reviews":
		writeUpstreamJSON(w, http.StatusCreated, map[string]any{
			"id":    101,
			"state": "APPROVED",
		})
	default:
		writeUpstreamJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
	}
}

func (g *fakeGitHub) setHead(headSha string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.headSha = headSha
}

func (g *fakeGitHub) setListPayload(owner, repo string, pulls []map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lists["/repos/"+owner+"/"+repo+"/pulls"] = fakePullList{status: http.StatusOK, pulls: pulls}
}

func (g *fakeGitHub) setListStatus(owner, repo string, status int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lists["/repos/"+owner+"/"+repo+"/pulls"] = fakePullList{status: status}
}

func (g *fakeGitHub) snapshot() []fakeGitHubCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]fakeGitHubCall(nil), g.calls...)
}

func writeUpstreamJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type prReviewHarness struct {
	store  store.Store
	github *fakeGitHub
	mux    *http.ServeMux
	module *Module
}

type fallbackAgentService struct {
	service.AgentService

	called bool
	ws     string
	state  string
	result *ops.GitPullRequestList
	err    error
}

func (s *fallbackAgentService) ListPullRequests(ctx context.Context, wsID, state string) (*ops.GitPullRequestList, error) {
	s.called = true
	s.ws = wsID
	s.state = state
	return s.result, s.err
}

func newPRReviewHarness(t *testing.T, withDispatcher bool) *prReviewHarness {
	return newPRReviewHarnessWithAgent(t, withDispatcher, nil)
}

func newPRReviewHarnessWithAgent(t *testing.T, withDispatcher bool, agentSvc service.AgentService) *prReviewHarness {
	t.Helper()
	h := &prReviewHarness{store: memstore.New(), github: newFakeGitHub(t), mux: http.NewServeMux()}
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv(connector.VaultKeyEnvVar, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32)))
	t.Setenv(connector.GitHubBaseURLEnvVar, h.github.server.URL)
	t.Setenv(webuiGitHubTokenEnv, prReviewTestToken)
	h.seedWorkspace(t)

	var dispatcher *connector.Dispatcher
	if withDispatcher {
		vault, err := connector.NewVaultFromEnv()
		if err != nil {
			t.Fatalf("NewVaultFromEnv: %v", err)
		}
		dispatcher = &connector.Dispatcher{
			Connectors: h.store.Connectors(),
			Grants:     h.store.ConnectorGrants(),
			Audit:      h.store.ConnectorCalls(),
			Vault:      vault,
			Providers:  connector.DefaultProviderRegistry(h.github.server.Client()),
		}
	}
	h.module = NewModule(h.store, dispatcher, agentSvc, nil)
	h.module.Register(h.mux)
	return h
}

func (h *prReviewHarness) seedWorkspace(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.store.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key:  prReviewTestWorkspace,
		Name: "Test Workspace",
	}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	if _, err := h.store.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey: prReviewTestWorkspace,
		Name:         "hello",
		RemoteURL:    "https://github.com/octocat/hello",
	}); err != nil {
		t.Fatalf("Create repo: %v", err)
	}
}

func (h *prReviewHarness) addRepo(t *testing.T, name, remoteURL string) {
	t.Helper()
	if _, err := h.store.Repos().Create(context.Background(), store.RepoCreate{
		WorkspaceKey: prReviewTestWorkspace,
		Name:         name,
		RemoteURL:    remoteURL,
	}); err != nil {
		t.Fatalf("Create repo %s: %v", name, err)
	}
}

func (h *prReviewHarness) rememberLocalPaths(t *testing.T, workspacePath, repoName, repoPath string) {
	t.Helper()
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			prReviewTestWorkspace: {
				Path:  workspacePath,
				Repos: map[string]string{repoName: repoPath},
			},
		},
	}); err != nil {
		t.Fatalf("SaveStateCache: %v", err)
	}
}

func (h *prReviewHarness) get(t *testing.T, path string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(middleware.WithUserIdentity(req.Context(), middleware.UserIdentity{UserID: "user-1"}))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func (h *prReviewHarness) streamWithContext(t *testing.T, ctx context.Context, path string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	req = req.WithContext(middleware.WithUserIdentity(req.Context(), middleware.UserIdentity{UserID: "user-1"}))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func (h *prReviewHarness) post(t *testing.T, path, body string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.WithUserIdentity(req.Context(), middleware.UserIdentity{UserID: "user-1"}))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

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
}

func TestListPullRequestsConnector(t *testing.T) {
	h := newPRReviewHarness(t, true)
	h.github.setListPayload("octocat", "hello", []map[string]any{
		{
			"number": 11,
			"state":  "open",
			"title":  "First PR",
			"draft":  false,
			"merged": false,
			"head":   map[string]any{"sha": "head-11", "ref": "feature/one"},
			"base":   map[string]any{"sha": "base-11", "ref": "main"},
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
			"number": 13,
			"state":  "closed",
			"title":  "Closed PR",
			"draft":  false,
			"merged": false,
			"head":   map[string]any{"sha": "head-13", "ref": "feature/three"},
			"base":   map[string]any{"sha": "base-13", "ref": "main"},
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
		t.Fatalf("first PR = %+v, want URL and repo name populated", first)
	}
	// The frontend filters on the UPPERCASE state the gh path emits
	// (isOpenPr: pr.state === "OPEN"). GitHub REST returns lowercase, so the
	// connector list MUST normalize or the PR list renders empty.
	if first.State != "OPEN" {
		t.Fatalf("first PR state = %q, want %q (frontend keys open rows off this)", first.State, "OPEN")
	}
	if got := decoded.Data.PullRequests[1]; !got.IsDraft || got.HeadRefName != "feature/two" || got.BaseRefName != "main" {
		t.Fatalf("second PR = %+v, want draft/head/base mapped", got)
	}
	if got := decoded.Data.PullRequests[2]; got.State != "CLOSED" {
		t.Fatalf("third PR state = %q, want %q", got.State, "CLOSED")
	}
	calls := h.github.snapshot()
	if len(calls) != 1 || calls[0].path != "/repos/octocat/hello/pulls" {
		t.Fatalf("calls = %+v, want one PR list", calls)
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
}

func TestPostPullRequestReviewAfterReadUsesSeededReviewGrant(t *testing.T) {
	h := newPRReviewHarness(t, true)
	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
	if status != http.StatusOK {
		t.Fatalf("read status = %d, want 200 (body %s)", status, raw)
	}

	status, raw = h.post(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/review",
		`{"event":"approve","body":"LGTM","expected_head_sha":"headsha-123"}`)
	if status != http.StatusOK {
		t.Fatalf("review status = %d, want 200 (body %s)", status, raw)
	}
	calls := h.github.snapshot()
	if len(calls) != 3 || calls[2].method != http.MethodPost || calls[2].path != "/repos/octocat/hello/pulls/7/reviews" {
		t.Fatalf("calls = %+v, want read GET, liveness GET, review POST", calls)
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

// A non-canonical-cased request must canonicalize owner/repo from the
// registered workspace repo BEFORE seeding + dispatch, so the seeded grant's
// (case-sensitive) resource pattern matches the dispatched resource — and a
// later canonical request reuses the same grant without a spurious 403.
func TestGetPullRequestCanonicalizesCasing(t *testing.T) {
	h := newPRReviewHarness(t, true)

	// Mixed-case path; repo is registered as octocat/hello.
	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/OCTOCAT/Hello/7")
	if status != http.StatusOK {
		t.Fatalf("mixed-case status = %d, want 200 (body %s)", status, raw)
	}
	// The dispatch must target the CANONICAL upstream path, not the raw casing.
	if calls := h.github.snapshot(); len(calls) != 1 || calls[0].path != "/repos/octocat/hello/pulls/7" {
		t.Fatalf("upstream calls = %+v, want one canonical /repos/octocat/hello/pulls/7", calls)
	}

	// A subsequent canonical request must still succeed (grant id/pattern did
	// not diverge — the pre-fix bug would 403 here).
	status, raw = h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
	if status != http.StatusOK {
		t.Fatalf("canonical status = %d, want 200 (body %s)", status, raw)
	}
}

func TestReviewerAgentNameSanitizesRepo(t *testing.T) {
	if got := reviewerAgentName("Hello.World_repo", 7); got != "review-hello-world-repo-pr-7" {
		t.Fatalf("reviewerAgentName() = %q, want review-hello-world-repo-pr-7", got)
	}
	if got := reviewerAgentName("...///", 7); got != "review-repo-pr-7" {
		t.Fatalf("reviewerAgentName(empty segment) = %q, want review-repo-pr-7", got)
	}
}

func TestPostReviewerMessageQueuesPending(t *testing.T) {
	h := newPRReviewHarness(t, false)
	agentName := createReviewerAgentForTest(t, h, "hello", 7)

	status, raw := h.post(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/messages", `{"text":"hello"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	var decoded struct {
		Success bool                      `json:"success"`
		Data    gen.ReviewerMessageResult `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, raw)
	}
	if !decoded.Success || decoded.Data.State != "pending" {
		t.Fatalf("response = %+v, want pending delivery", decoded)
	}
	queued := queuedReviewerMessagesForTest(t, h.store, agentName)
	if len(queued) != 1 {
		t.Fatalf("queued messages = %d, want 1", len(queued))
	}
	if queued[0].Body != "hello" || queued[0].SourceKind != "user_chat" {
		t.Fatalf("queued message = %+v, want user_chat hello", queued[0])
	}
}

func TestPostReviewerMessageRequiresStartedReviewer(t *testing.T) {
	h := newPRReviewHarness(t, false)
	status, raw := h.post(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/messages", `{"text":"hello"}`)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", status, raw)
	}
	if code := decodeErrorCode(t, raw); code != "reviewer_not_started" {
		t.Fatalf("code = %q, want reviewer_not_started", code)
	}
}

func TestPostReviewerMessageRejectsEmptyText(t *testing.T) {
	h := newPRReviewHarness(t, false)
	createReviewerAgentForTest(t, h, "hello", 7)
	status, raw := h.post(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/messages", `{"text":"   "}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", status, raw)
	}
	if code := decodeErrorCode(t, raw); code != "invalid" {
		t.Fatalf("code = %q, want invalid", code)
	}
}

func TestPostReviewerMessageUnregisteredRepoDoesNotEnqueue(t *testing.T) {
	h := newPRReviewHarness(t, false)
	createReviewerAgentForTest(t, h, "hello", 7)
	status, raw := h.post(t, "/api/workspaces/WS/pull-requests/acme/widgets/7/messages", `{"text":"hello"}`)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", status, raw)
	}
	if code := decodeErrorCode(t, raw); code != "repo_not_registered" {
		t.Fatalf("code = %q, want repo_not_registered", code)
	}
	if queued := queuedReviewerMessagesForTest(t, h.store, reviewerAgentName("hello", 7)); len(queued) != 0 {
		t.Fatalf("queued messages = %d, want 0", len(queued))
	}
}

func TestStreamReviewerStartingWhenNoSession(t *testing.T) {
	h := newPRReviewHarness(t, false)
	createReviewerAgentForTest(t, h, "hello", 7)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)
	status, raw := h.streamWithContext(t, ctx, "/api/workspaces/WS/pull-requests/octocat/hello/7/stream")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	body := string(raw)
	if !strings.Contains(body, "event: status") || !strings.Contains(body, `"state":"starting"`) {
		t.Fatalf("stream body = %q, want starting status event", body)
	}
}

func TestStreamReviewerEmitsMessages(t *testing.T) {
	h := newPRReviewHarness(t, false)
	agentName := createReviewerAgentForTest(t, h, "hello", 7)
	createReviewerOrchestrationSessionForTest(t, h, agentName, map[string]string{
		leadcontrol.MetadataCodexEndpoint: "ws://codex.test",
		leadcontrol.MetadataCodexThreadID: "thread-1",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.module.streamPollInterval = time.Millisecond
	onRead := cancelAfterReads(2, cancel)
	h.module.dialCodex = func(ctx context.Context, endpoint string) (codexThreadReader, error) {
		if endpoint != "ws://codex.test" {
			t.Fatalf("endpoint = %q, want ws://codex.test", endpoint)
		}
		return &fakeReviewerCodexReader{
			onRead: onRead,
			thread: &leadcontrol.CodexThread{
				ID:     "thread-1",
				Status: leadcontrol.CodexThreadStatus{Type: "idle"},
				Turns: []leadcontrol.CodexTurn{{
					ID:     "turn-1",
					Status: "completed",
					Items: []leadcontrol.CodexTurnItem{
						{
							Type:    "userMessage",
							ID:      "item-user",
							Content: []leadcontrol.CodexContentBlock{{Type: "text", Text: "hello"}},
						},
						{
							Type:  "agentMessage",
							ID:    "item-agent",
							Text:  "hi there",
							Phase: "final_answer",
						},
					},
				}},
			},
		}, nil
	}

	status, raw := h.streamWithContext(t, ctx, "/api/workspaces/WS/pull-requests/octocat/hello/7/stream")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	body := string(raw)
	if strings.Count(body, `"role":"user"`) != 1 || strings.Count(body, `"text":"hello"`) != 1 {
		t.Fatalf("stream body = %q, want one user hello message", body)
	}
	if strings.Count(body, `"role":"assistant"`) != 1 || strings.Count(body, `"text":"hi there"`) != 1 {
		t.Fatalf("stream body = %q, want one assistant hi there message", body)
	}
	if strings.Count(body, "event: message") != 2 {
		t.Fatalf("stream body = %q, want exactly two message events", body)
	}
}

func TestGetReviewerConversationStartingWhenNoSession(t *testing.T) {
	h := newPRReviewHarness(t, false)
	createReviewerAgentForTest(t, h, "hello", 7)

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/conversation")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	var decoded struct {
		Data struct {
			State    string           `json:"state"`
			Messages []map[string]any `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode: %v (body %s)", err, raw)
	}
	if decoded.Data.State != "starting" || len(decoded.Data.Messages) != 0 {
		t.Fatalf("conversation = %+v, want starting + no messages", decoded.Data)
	}
}

func TestGetReviewerConversationSnapshot(t *testing.T) {
	h := newPRReviewHarness(t, false)
	agentName := createReviewerAgentForTest(t, h, "hello", 7)
	createReviewerOrchestrationSessionForTest(t, h, agentName, map[string]string{
		leadcontrol.MetadataCodexEndpoint: "ws://codex.test",
		leadcontrol.MetadataCodexThreadID: "thread-1",
	})
	h.module.dialCodex = func(ctx context.Context, endpoint string) (codexThreadReader, error) {
		return &fakeReviewerCodexReader{
			thread: &leadcontrol.CodexThread{
				ID:     "thread-1",
				Status: leadcontrol.CodexThreadStatus{Type: "idle"},
				Turns: []leadcontrol.CodexTurn{{
					ID:     "turn-1",
					Status: "completed",
					Items: []leadcontrol.CodexTurnItem{
						{Type: "userMessage", ID: "item-user", Content: []leadcontrol.CodexContentBlock{{Type: "text", Text: "hello"}}},
						{Type: "agentMessage", ID: "item-agent", Text: "hi there", Phase: "final_answer"},
					},
				}},
			},
		}, nil
	}

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/conversation")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	var decoded struct {
		Data struct {
			State    string `json:"state"`
			Messages []struct {
				Role string `json:"role"`
				Text string `json:"text"`
			} `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode: %v (body %s)", err, raw)
	}
	if decoded.Data.State != "idle" || len(decoded.Data.Messages) != 2 {
		t.Fatalf("conversation = %+v, want idle + 2 messages", decoded.Data)
	}
	if decoded.Data.Messages[0].Role != "user" || decoded.Data.Messages[0].Text != "hello" {
		t.Fatalf("message[0] = %+v, want user/hello", decoded.Data.Messages[0])
	}
	if decoded.Data.Messages[1].Role != "assistant" || decoded.Data.Messages[1].Text != "hi there" {
		t.Fatalf("message[1] = %+v, want assistant/hi there", decoded.Data.Messages[1])
	}
}

func TestEnsureReviewerCreatesAgentWorktreeAndSeed(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	h := newPRReviewHarness(t, true)
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	workspacePath := filepath.Join(root, "workspace")
	repoPath := filepath.Join(workspacePath, "hello")

	git(t, "", "init", "--bare", remote)
	git(t, "", "init", seed)
	git(t, seed, "checkout", "-b", "main")
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeTestFile(t, filepath.Join(seed, "base.txt"), "base\n")
	git(t, seed, "add", "base.txt")
	git(t, seed, "commit", "-m", "base")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "HEAD:refs/heads/main")

	writeTestFile(t, filepath.Join(seed, "pr.txt"), "pr head\n")
	git(t, seed, "add", "pr.txt")
	git(t, seed, "commit", "-m", "pr head")
	headSHA := gitOutput(t, seed, "rev-parse", "HEAD")
	git(t, seed, "push", "origin", "HEAD:refs/pull/7/head")

	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("mkdir workspace path: %v", err)
	}
	git(t, "", "clone", remote, repoPath)
	git(t, repoPath, "checkout", "main")
	h.rememberLocalPaths(t, workspacePath, "hello", repoPath)
	h.github.setHead(headSHA)

	status, raw := h.post(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/reviewer", `{}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	var decoded struct {
		Success bool                     `json:"success"`
		Data    gen.ReviewerEnsureResult `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, raw)
	}
	agentName := reviewerAgentName("hello", 7)
	if !decoded.Success || decoded.Data.AgentName != agentName || decoded.Data.CheckedOutSha != headSHA || !decoded.Data.Seeded {
		t.Fatalf("response = %+v, want agent %s sha %s seeded", decoded, agentName, headSHA)
	}

	agent, err := h.store.Agents().Get(context.Background(), prReviewTestWorkspace, agentName)
	if err != nil {
		t.Fatalf("get reviewer agent: %v", err)
	}
	if agent.Backend != "codex" || agent.RoleName != "lead" || agent.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("agent = %+v, want codex lead running", agent)
	}
	if _, err := h.store.Roles().Get(context.Background(), prReviewTestWorkspace, "lead"); err != nil {
		t.Fatalf("lead role missing: %v", err)
	}
	worktreePath, err := localworkspace.PRReviewWorktreePath(workspacePath, "hello", 7)
	if err != nil {
		t.Fatalf("PRReviewWorktreePath: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		t.Fatalf("worktree .git missing: %v", err)
	}
	if got := gitOutput(t, worktreePath, "rev-parse", "HEAD"); got != headSHA {
		t.Fatalf("worktree HEAD = %s, want %s", got, headSHA)
	}
	// The checkout is self-describing: the base is recorded in per-worktree git
	// config and diffs cleanly against HEAD. No seed message is delivered — the
	// prompt (codex's first turn) drives the review.
	base := strings.TrimSpace(gitOutput(t, worktreePath, "config", "loom.reviewBase"))
	if base == "" {
		t.Fatal("loom.reviewBase not recorded in the review worktree")
	}
	if diff := gitOutput(t, worktreePath, "diff", base+"...HEAD", "--name-only"); !strings.Contains(diff, "pr.txt") {
		t.Fatalf("review diff = %q, want the PR's pr.txt change", diff)
	}
	if queued := queuedReviewerMessagesForTest(t, h.store, agentName); len(queued) != 0 {
		t.Fatalf("queued messages = %d, want 0 (prompt drives the review, no seed)", len(queued))
	}

	// A second ensure is idempotent: still 200, base still recorded.
	status, raw = h.post(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/reviewer", `{}`)
	if status != http.StatusOK {
		t.Fatalf("second status = %d, want 200 (body %s)", status, raw)
	}
	if got := strings.TrimSpace(gitOutput(t, worktreePath, "config", "loom.reviewBase")); got != base {
		t.Fatalf("loom.reviewBase after second ensure = %q, want %q", got, base)
	}
}

func TestGetPullRequestEgressUnavailable(t *testing.T) {
	t.Run("nil dispatcher", func(t *testing.T) {
		h := newPRReviewHarness(t, false)
		status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
		if status != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body %s)", status, raw)
		}
		if code := decodeErrorCode(t, raw); code != "egress_unavailable" {
			t.Fatalf("code = %q, want egress_unavailable", code)
		}
	})

	t.Run("token unset", func(t *testing.T) {
		h := newPRReviewHarness(t, true)
		t.Setenv(webuiGitHubTokenEnv, "")
		status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
		if status != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body %s)", status, raw)
		}
		if code := decodeErrorCode(t, raw); code != "egress_unavailable" {
			t.Fatalf("code = %q, want egress_unavailable", code)
		}
	})
}

func createReviewerAgentForTest(t *testing.T, h *prReviewHarness, repo string, number int) string {
	t.Helper()
	ctx := context.Background()
	if _, err := h.store.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: prReviewTestWorkspace,
		Name:         "lead",
		Description:  "Lead/orchestrator terminal",
	}); err != nil {
		t.Fatalf("Create lead role: %v", err)
	}
	agentName := reviewerAgentName(repo, number)
	if _, err := h.store.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: prReviewTestWorkspace,
		Name:         agentName,
		RoleName:     "lead",
		Backend:      "codex",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("Create reviewer agent: %v", err)
	}
	return agentName
}

func createReviewerOrchestrationSessionForTest(t *testing.T, h *prReviewHarness, agentName string, metadata map[string]string) {
	t.Helper()
	if _, err := h.store.AgentSessions().Create(context.Background(), store.AgentSessionCreate{
		WorkspaceKey: prReviewTestWorkspace,
		SessionID:    "session-" + agentName,
		AgentID:      agentName,
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
		Metadata:     metadata,
	}); err != nil {
		t.Fatalf("Create reviewer orchestration session: %v", err)
	}
}

type fakeReviewerCodexReader struct {
	thread *leadcontrol.CodexThread
	onRead func()
}

func (r *fakeReviewerCodexReader) ReadThreadWithTurns(context.Context, string) (*leadcontrol.CodexThread, error) {
	if r.onRead != nil {
		r.onRead()
	}
	return r.thread, nil
}

func (r *fakeReviewerCodexReader) Close(string) error {
	return nil
}

func cancelAfterReads(want int, cancel context.CancelFunc) func() {
	count := 0
	return func() {
		count++
		if count >= want {
			cancel()
		}
	}
}

func queuedReviewerMessagesForTest(t *testing.T, st store.Store, agentName string) []*domain.AgentInboxMessage {
	t.Helper()
	items, err := st.AgentInboxMessages().List(context.Background(), prReviewTestWorkspace, store.AgentInboxMessageFilter{
		TargetAgentID: agentName,
		Status:        domain.AgentInboxMessageQueued,
		Limit:         100,
	})
	if err != nil {
		t.Fatalf("List queued inbox messages: %v", err)
	}
	return items
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // fixed test helper command; args are test-controlled.
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // fixed test helper command; args are test-controlled.
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}

func decodeErrorCode(t *testing.T, raw []byte) string {
	t.Helper()
	var decoded struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode error: %v (body %s)", err, raw)
	}
	if decoded.Success {
		t.Fatalf("error response marked success: %s", raw)
	}
	if decoded.Error == "" {
		t.Fatalf("error response missing error: %s", raw)
	}
	return decoded.Code
}

func TestParseGitHubOwnerRepo(t *testing.T) {
	for _, tc := range []struct {
		remote string
		owner  string
		repo   string
		ok     bool
	}{
		{remote: "git@github.com:octocat/hello.git", owner: "octocat", repo: "hello", ok: true},
		{remote: "https://github.com/octocat/hello", owner: "octocat", repo: "hello", ok: true},
		{remote: "https://github.com/octocat/hello.git", owner: "octocat", repo: "hello", ok: true},
		{remote: "https://gitlab.com/octocat/hello.git", ok: false},
	} {
		owner, repo, ok := parseGitHubOwnerRepo(tc.remote)
		if ok != tc.ok || owner != tc.owner || repo != tc.repo {
			t.Fatalf("parseGitHubOwnerRepo(%q) = %q/%q %v, want %q/%q %v",
				tc.remote, owner, repo, ok, tc.owner, tc.repo, tc.ok)
		}
	}
}

func TestTrimReviewerPreamble(t *testing.T) {
	msgs := []reviewerStreamMessage{
		{Role: "user", ItemID: "p", Text: "## READ-ONLY PR REVIEWER\n\nYou are a reviewer…"},
		{Role: "assistant", ItemID: "r", Text: "The PR does Y; risk at foo.go:12."},
		{Role: "user", ItemID: "q", Text: "why is that a risk?"},
		{Role: "assistant", ItemID: "a", Text: "because…"},
	}
	got := trimReviewerPreamble(msgs)
	if len(got) != 3 || got[0].ItemID != "r" || got[1].ItemID != "q" || got[2].ItemID != "a" {
		t.Fatalf("trim = %+v, want [r,q,a] (prompt bubble dropped, real user msg kept)", got)
	}
	// Nothing is trimmed once the conversation has started.
	if len(trimReviewerPreamble(msgs[1:])) != 3 {
		t.Fatal("must not trim once the conversation has started")
	}
	// A user message is never hidden, even one that looks like the prompt topic.
	typed := []reviewerStreamMessage{
		{Role: "user", ItemID: "p", Text: "## READ-ONLY PR REVIEWER\n…"},
		{Role: "user", ItemID: "u", Text: "what does the readonly reviewer rule mean?"},
	}
	got = trimReviewerPreamble(typed)
	if len(got) != 1 || got[0].ItemID != "u" {
		t.Fatalf("trim = %+v, want the user's typed message kept", got)
	}
	// No preamble → unchanged.
	plain := []reviewerStreamMessage{{Role: "assistant", ItemID: "x", Text: "hi"}}
	if len(trimReviewerPreamble(plain)) != 1 {
		t.Fatal("no-preamble case must be unchanged")
	}
}
