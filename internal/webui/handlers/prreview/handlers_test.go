package prreview

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/localsettings"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	localsettingshandler "github.com/tysonthomas9/loomcli/internal/webui/handlers/localsettings"
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
	query         string
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
		query:         r.URL.RawQuery,
		authorization: r.Header.Get("Authorization"),
		body:          body,
	})
	headSha := g.headSha
	state := g.state
	list, hasList := g.lists[r.URL.Path+"?page="+r.URL.Query().Get("page")]
	if !hasList {
		list, hasList = g.lists[r.URL.Path]
	}
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

func (g *fakeGitHub) setListPage(owner, repo string, page int, pulls []map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := "/repos/" + owner + "/" + repo + "/pulls?page=" + strconv.Itoa(page)
	g.lists[key] = fakePullList{status: http.StatusOK, pulls: pulls}
}

func (g *fakeGitHub) setListStatus(owner, repo string, status int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lists["/repos/"+owner+"/"+repo+"/pulls"] = fakePullList{status: status}
}

func fakePullRequestPage(first, count int) []map[string]any {
	pulls := make([]map[string]any, 0, count)
	for number := first; number < first+count; number++ {
		pulls = append(pulls, map[string]any{
			"number": number,
			"state":  "open",
			"title":  "PR " + strconv.Itoa(number),
			"head":   map[string]any{"sha": "head", "ref": "feature"},
			"base":   map[string]any{"sha": "base", "ref": "main"},
		})
	}
	return pulls
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

func decodePullRequestsResponse(t *testing.T, raw []byte) pullRequestsData {
	t.Helper()
	var decoded struct {
		Success bool             `json:"success"`
		Data    pullRequestsData `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode pull request response: %v (body %s)", err, raw)
	}
	if !decoded.Success {
		t.Fatalf("pull request response was not successful: %s", raw)
	}
	return decoded.Data
}

type prReviewHarness struct {
	store   store.Store
	github  *fakeGitHub
	mux     *http.ServeMux
	module  *Module
	dataDir string
}

type testCredentialSource string

const (
	testCredentialEnv      testCredentialSource = "env"
	testCredentialSettings testCredentialSource = "settings"
	testCredentialNone     testCredentialSource = "none"
)

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
	return newPRReviewHarnessWithCredential(t, withDispatcher, agentSvc, testCredentialEnv, prReviewTestToken)
}

func newPRReviewHarnessWithCredential(
	t *testing.T,
	withDispatcher bool,
	agentSvc service.AgentService,
	source testCredentialSource,
	token string,
) *prReviewHarness {
	t.Helper()
	h := &prReviewHarness{
		store:   memstore.New(),
		github:  newFakeGitHub(t),
		mux:     http.NewServeMux(),
		dataDir: t.TempDir(),
	}
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv(connector.GitHubBaseURLEnvVar, h.github.server.URL)
	switch source {
	case testCredentialEnv:
		t.Setenv(connector.VaultKeyEnvVar, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32)))
		t.Setenv(webuiGitHubTokenEnv, token)
	case testCredentialSettings:
		t.Setenv(connector.VaultKeyEnvVar, "")
		t.Setenv(webuiGitHubTokenEnv, "")
		h.setSettingsGitHubToken(t, token)
	case testCredentialNone:
		t.Setenv(connector.VaultKeyEnvVar, "")
		t.Setenv(webuiGitHubTokenEnv, "")
	default:
		t.Fatalf("unknown test credential source %q", source)
	}
	h.seedWorkspace(t)

	var dispatcher *connector.Dispatcher
	if withDispatcher {
		vault, err := connector.NewVaultFromEnvOrKeyFile(h.dataDir)
		if err != nil {
			t.Fatalf("NewVaultFromEnvOrKeyFile: %v", err)
		}
		dispatcher = &connector.Dispatcher{
			Connectors: h.store.Connectors(),
			Grants:     h.store.ConnectorGrants(),
			Audit:      h.store.ConnectorCalls(),
			Vault:      vault,
			Providers:  connector.DefaultProviderRegistry(h.github.server.Client()),
		}
	}
	h.module = NewModule(h.store, dispatcher, agentSvc, nil, h.dataDir)
	h.module.Register(h.mux)
	return h
}

func (h *prReviewHarness) setSettingsGitHubToken(t *testing.T, token string) {
	t.Helper()
	settings, err := localsettings.Load(h.dataDir)
	if err != nil {
		t.Fatalf("Load local settings: %v", err)
	}
	credential, err := localsettings.SealRuntimeCredential(
		h.dataDir,
		localsettings.RuntimeCredentialProviderGitHub,
		token,
		time.Now(),
	)
	if err != nil {
		t.Fatalf("SealRuntimeCredential: %v", err)
	}
	settings.RuntimeCredentials.GitHub = credential
	if err := localsettings.Save(h.dataDir, settings); err != nil {
		t.Fatalf("Save local settings: %v", err)
	}
}

func (h *prReviewHarness) rebuildWithDataDir(t *testing.T, dataDir string) {
	t.Helper()
	vault, err := connector.NewVaultFromEnvOrKeyFile(dataDir)
	if err != nil {
		t.Fatalf("NewVaultFromEnvOrKeyFile: %v", err)
	}
	dispatcher := &connector.Dispatcher{
		Connectors: h.store.Connectors(),
		Grants:     h.store.ConnectorGrants(),
		Audit:      h.store.ConnectorCalls(),
		Vault:      vault,
		Providers:  connector.DefaultProviderRegistry(h.github.server.Client()),
	}
	h.dataDir = dataDir
	h.module = NewModule(h.store, dispatcher, nil, nil, dataDir)
	h.mux = http.NewServeMux()
	h.module.Register(h.mux)
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

func (h *prReviewHarness) updateRepoRemote(t *testing.T, name, remoteURL string) {
	t.Helper()
	if _, err := h.store.Repos().Update(context.Background(), prReviewTestWorkspace, name, store.RepoUpdate{
		RemoteURL: &remoteURL,
	}); err != nil {
		t.Fatalf("Update repo %s remote URL: %v", name, err)
	}
}

func (h *prReviewHarness) rememberLocalPaths(t *testing.T, workspacePath, repoName, repoPath string) {
	t.Helper()
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		if sc.Workspaces == nil {
			sc.Workspaces = map[string]bootstrap.WorkspaceLocalState{}
		}
		sc.Workspaces[prReviewTestWorkspace] = bootstrap.WorkspaceLocalState{
			Path:  workspacePath,
			Repos: map[string]string{repoName: repoPath},
		}
		return nil
	}); err != nil {
		t.Fatalf("MutateStateCache: %v", err)
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

func (h *prReviewHarness) patchLocalSettings(t *testing.T, body string, onGitHubCredentialChanged func()) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/local/settings", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	localsettingshandler.HandlePatch(h.dataDir, localsettingshandler.PatchOptions{
		OnGitHubRuntimeCredentialChanged: onGitHubCredentialChanged,
	}).ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func assertGrantActions(t *testing.T, h *prReviewHarness, want []string) {
	t.Helper()
	grants, err := h.store.ConnectorGrants().ListByBinding(context.Background(), prReviewTestWorkspace, bindingID)
	if err != nil {
		t.Fatalf("ListByBinding: %v", err)
	}
	got := make([]string, 0, len(grants))
	for _, grant := range grants {
		got = append(got, grant.Action)
	}
	slices.Sort(got)
	want = slices.Clone(want)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("grant actions = %v, want %v", got, want)
	}
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

func TestReviewerAgentNameSanitizesOwnerAndRepo(t *testing.T) {
	if got := reviewerAgentName("OpenAI.Inc", "Hello.World_repo", 7); got != "review-openai-inc-hello-world-repo-14113012-pr-7" {
		t.Fatalf("reviewerAgentName() = %q, want sanitized name with canonical identity hash", got)
	}
	if got := reviewerAgentName("Octocat", "...///", 7); got != "review-octocat-repo-b990e831-pr-7" {
		t.Fatalf("reviewerAgentName(empty segment) = %q, want fallback segment with identity hash", got)
	}
}

func TestReviewerAgentNameIncludesOwnerIdentity(t *testing.T) {
	orgA := reviewerAgentName("org-a", "api", 7)
	orgB := reviewerAgentName("org-b", "api", 7)
	if orgA == orgB {
		t.Fatalf("reviewer names collide: %q", orgA)
	}
}

func TestReviewerAgentNameSeparatesLossySanitizationCollisions(t *testing.T) {
	dotted := reviewerAgentName("org-a", "api.v2", 7)
	dashed := reviewerAgentName("org-a", "api-v2", 7)
	if dotted == dashed {
		t.Fatalf("reviewer names collide after sanitization: %q", dotted)
	}
	if !strings.Contains(dotted, "-a7549c10-pr-7") || !strings.Contains(dashed, "-6a12be2d-pr-7") {
		t.Fatalf("reviewer names lack canonical identity hashes: %q, %q", dotted, dashed)
	}
}

func TestReviewerAgentNameSeparatesTruncatedPrefixCollisions(t *testing.T) {
	ownerA := strings.Repeat("owner", 30) + "a"
	ownerB := strings.Repeat("owner", 30) + "b"
	repo := strings.Repeat("repository", 20)
	if oldA, oldB := intermediateReviewerAgentName(ownerA, repo, 7), intermediateReviewerAgentName(ownerB, repo, 7); oldA != oldB {
		t.Fatalf("test inputs do not collide in the intermediate shape: %q, %q", oldA, oldB)
	}
	if nameA, nameB := reviewerAgentName(ownerA, repo, 7), reviewerAgentName(ownerB, repo, 7); nameA == nameB {
		t.Fatalf("hashed reviewer names collide after truncation: %q", nameA)
	}
}

func TestReviewerAgentNameTruncatesToStoredNameLimit(t *testing.T) {
	owner := strings.Repeat("owner-", 40)
	repo := strings.Repeat("repo-", 40)
	name := reviewerAgentName(owner, repo, 123)
	if len(name) > reviewerAgentNameMaxLen {
		t.Fatalf("reviewerAgentName length = %d, want <= %d: %q", len(name), reviewerAgentNameMaxLen, name)
	}
	if !service.ValidStoredAgentName.MatchString(name) {
		t.Fatalf("reviewerAgentName() = %q, want valid stored agent name", name)
	}
	if !strings.HasPrefix(name, "review-") || !strings.HasSuffix(name, "-pr-123") {
		t.Fatalf("reviewerAgentName() = %q, want preserved prefix and suffix", name)
	}
	if !strings.HasSuffix(name, "-"+reviewerIdentityHash(owner, repo)+"-pr-123") {
		t.Fatalf("reviewerAgentName() = %q, want hash segment before PR suffix", name)
	}
	if strings.Contains(name, "--") {
		t.Fatalf("reviewerAgentName() = %q, want segments not ending in dashes", name)
	}
}

func TestLegacyReviewerAgentNameUsesOldRepoOnlyShape(t *testing.T) {
	if got := legacyReviewerAgentName("Hello.World_repo", 7); got != "review-hello-world-repo-pr-7" {
		t.Fatalf("legacyReviewerAgentName() = %q, want review-hello-world-repo-pr-7", got)
	}
	longRepo := strings.Repeat("a", legacyReviewerRepoSegmentMaxLen-1) + ".tail"
	want := "review-" + strings.Repeat("a", legacyReviewerRepoSegmentMaxLen-1) + "-pr-7"
	if got := legacyReviewerAgentName(longRepo, 7); got != want {
		t.Fatalf("legacyReviewerAgentName(truncated) = %q, want %q", got, want)
	}
	if got := intermediateReviewerAgentName("OpenAI.Inc", "Hello.World_repo", 7); got != "review-openai-inc-hello-world-repo-pr-7" {
		t.Fatalf("intermediateReviewerAgentName() = %q, want owner-inclusive unhashed shape", got)
	}
}

func TestPostReviewerMessageQueuesPending(t *testing.T) {
	h := newPRReviewHarness(t, false)
	agentName := createReviewerAgentForTest(t, h, "octocat", "hello", 7)

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
	createReviewerAgentForTest(t, h, "octocat", "hello", 7)
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
	createReviewerAgentForTest(t, h, "octocat", "hello", 7)
	status, raw := h.post(t, "/api/workspaces/WS/pull-requests/acme/widgets/7/messages", `{"text":"hello"}`)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", status, raw)
	}
	if code := decodeErrorCode(t, raw); code != "repo_not_registered" {
		t.Fatalf("code = %q, want repo_not_registered", code)
	}
	if queued := queuedReviewerMessagesForTest(t, h.store, reviewerAgentName("octocat", "hello", 7)); len(queued) != 0 {
		t.Fatalf("queued messages = %d, want 0", len(queued))
	}
}

func TestStreamReviewerStartingWhenNoSession(t *testing.T) {
	h := newPRReviewHarness(t, false)
	createReviewerAgentForTest(t, h, "octocat", "hello", 7)

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
	agentName := createReviewerAgentForTest(t, h, "octocat", "hello", 7)
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
	createReviewerAgentForTest(t, h, "octocat", "hello", 7)

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
	agentName := createReviewerAgentForTest(t, h, "octocat", "hello", 7)
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
	assertGrantActions(t, h, prReadActions)
	var decoded struct {
		Success bool                     `json:"success"`
		Data    gen.ReviewerEnsureResult `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, raw)
	}
	agentName := reviewerAgentName("octocat", "hello", 7)
	if !decoded.Success || decoded.Data.AgentName != agentName || decoded.Data.CheckedOutSha != headSHA || !decoded.Data.Seeded {
		t.Fatalf("response = %+v, want agent %s sha %s seeded", decoded, agentName, headSHA)
	}

	agent, err := h.store.Agents().Get(context.Background(), prReviewTestWorkspace, agentName)
	if err != nil {
		t.Fatalf("get reviewer agent: %v", err)
	}
	if agent.Backend != "codex" || agent.RoleName != reviewerRoleName || agent.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("agent = %+v, want codex %s running", agent, reviewerRoleName)
	}
	role, err := h.store.Roles().Get(context.Background(), prReviewTestWorkspace, reviewerRoleName)
	if err != nil {
		t.Fatalf("reviewer role missing: %v", err)
	}
	if role.Kind != domain.RoleKindInteractive || role.PromptFile != reviewerPromptFile || role.Prompt != "" {
		t.Fatalf("reviewer role = kind:%q prompt_file:%q prompt:%q", role.Kind, role.PromptFile, role.Prompt)
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

func TestEnsureReviewerRejectsChangedFetchedTip(t *testing.T) {
	h := newPRReviewHarness(t, true)
	workspacePath := t.TempDir()
	repoPath := filepath.Join(workspacePath, "hello")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo path: %v", err)
	}
	h.rememberLocalPaths(t, workspacePath, "hello", repoPath)
	h.github.setHead("ABC123")

	checkoutCalled := false
	h.module.checkoutReviewerPRHead = func(_ context.Context, _, _ string, _ string, _ int, headSHA string) (string, error) {
		checkoutCalled = true
		if headSHA != "ABC123" {
			t.Fatalf("checkout head sha = %q, want ABC123", headSHA)
		}
		return "def456", &localworkspace.PRHeadChangedError{
			ExpectedSHA: headSHA,
			TipSHA:      "def456",
		}
	}

	status, raw := h.post(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/reviewer", `{}`)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", status, raw)
	}
	var decoded struct {
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, raw)
	}
	if decoded.Code != "stale_subject" || !decoded.Retryable {
		t.Fatalf("response = %+v, want stale_subject retryable=true", decoded)
	}
	if !checkoutCalled {
		t.Fatal("checkout seam was not called")
	}

	agentName := reviewerAgentName("octocat", "hello", 7)
	if _, err := h.store.Agents().Get(context.Background(), prReviewTestWorkspace, agentName); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("reviewer agent lookup error = %v, want ErrNotFound", err)
	}
	cache, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("LoadStateCache: %v", err)
	}
	if _, remembered := cache.Workspaces[prReviewTestWorkspace].Agents[agentName]; remembered {
		t.Fatalf("reviewer worktree was remembered for stale agent %q", agentName)
	}
}

func TestEnsureReviewerRejectsMissingRecordedCheckout(t *testing.T) {
	h := newPRReviewHarness(t, true)
	workspacePath := t.TempDir()
	repoPath := filepath.Join(workspacePath, "missing-checkout")
	h.rememberLocalPaths(t, workspacePath, "hello", repoPath)

	checkoutCalled := false
	h.module.checkoutReviewerPRHead = func(context.Context, string, string, string, int, string) (string, error) {
		checkoutCalled = true
		return "", nil
	}
	status, raw := h.post(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/reviewer", `{}`)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", status, raw)
	}
	if code := decodeErrorCode(t, raw); code != "repo_not_checked_out" {
		t.Fatalf("code = %q, want repo_not_checked_out", code)
	}
	if checkoutCalled {
		t.Fatal("checkout ran for a recorded path that does not exist")
	}
	agentName := reviewerAgentName("octocat", "hello", 7)
	if _, err := h.store.Agents().Get(context.Background(), prReviewTestWorkspace, agentName); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("reviewer agent lookup error = %v, want ErrNotFound", err)
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

func createReviewerAgentForTest(t *testing.T, h *prReviewHarness, owner, repo string, number int) string {
	t.Helper()
	ctx := context.Background()
	if _, err := h.store.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: prReviewTestWorkspace,
		Name:         reviewerRoleName,
		Kind:         string(domain.RoleKindInteractive),
		Description:  reviewerRoleDescription,
		PromptFile:   reviewerPromptFile,
	}); err != nil {
		t.Fatalf("Create reviewer role: %v", err)
	}
	agentName := reviewerAgentName(owner, repo, number)
	if _, err := h.store.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: prReviewTestWorkspace,
		Name:         agentName,
		RoleName:     reviewerRoleName,
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
		{remote: "ssh://git@github.com/octocat/hello.git", owner: "octocat", repo: "hello", ok: true},
		{remote: "ssh://deploy@github.com/octocat/hello", owner: "octocat", repo: "hello", ok: true},
		{remote: "https://github.com/octocat/hello", owner: "octocat", repo: "hello", ok: true},
		{remote: "https://github.com/octocat/hello.git", owner: "octocat", repo: "hello", ok: true},
		{remote: "ssh://git@gitlab.com/octocat/hello.git", ok: false},
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

func TestReviewSubmissionRunIDUniquePerSubmission(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/pull-requests/octocat/hello/7/review", nil)
	params := pullRequestPath{owner: "octocat", repo: "hello", number: 7}

	first, err := reviewSubmissionRunID(req, params)
	if err != nil {
		t.Fatalf("reviewSubmissionRunID: %v", err)
	}
	second, err := reviewSubmissionRunID(req, params)
	if err != nil {
		t.Fatalf("reviewSubmissionRunID: %v", err)
	}
	if first == second {
		t.Fatalf("run ids identical across submissions: %s", first)
	}
	base := syntheticRunID(req, params, providers.ActionGitHubReviewPost) + ":"
	if !strings.HasPrefix(first, base) || !strings.HasPrefix(second, base) {
		t.Fatalf("run ids %q / %q missing deterministic prefix %q", first, second, base)
	}
}
