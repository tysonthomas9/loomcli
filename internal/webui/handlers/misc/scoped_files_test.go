// This file references NewFileService from the root webui package which
// cannot be imported without creating a cycle (see test_bridge_test.go).

package misc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// wsRootFor returns a mockFileOps whose workspace root resolves to dir.
func wsRootFor(dir string) *mockFileOps {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolved = dir
	}
	return &mockFileOps{
		resolveWsRootFunc: func() (string, error) {
			return resolved, nil
		},
		resolveWsDataFunc: func() (*sourcecontrol.WorkspaceTopology, error) {
			return &sourcecontrol.WorkspaceTopology{ID: "test-ws", Path: resolved}, nil
		},
	}
}

// scopedReq builds a GET request for a scope-rooted file route with the
// workspace context the handler reads wsID from.
func scopedReq(target string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := middleware.WithWorkspace(req.Context(), "test-ws")
	ctx = middleware.WithFileAccessGrant(ctx, testFileGrantIssuer.ReadWrite(true))
	return req.WithContext(ctx)
}

func scopedReqBody(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	ctx := middleware.WithWorkspace(req.Context(), "test-ws")
	ctx = middleware.WithFileAccessGrant(ctx, testFileGrantIssuer.ReadWrite(true))
	return req.WithContext(ctx)
}

type handlerScopeCase struct {
	name   string
	scope  string
	target string
	root   string
}

type recordingNavigationFileService struct {
	stubFileService
	indexWS      string
	indexScope   sourcecontrol.FileScope
	indexTarget  string
	indexRepo    string
	searchWS     string
	searchScope  sourcecontrol.FileScope
	searchTarget string
	searchRepo   string
	searchReq    sourcecontrol.FileSearchRequest
	statusWS     string
	statusScope  sourcecontrol.FileScope
	statusTarget string
	statusRepo   string
}

type recordingCheckoutsFileService struct {
	stubFileService
	wsID string
}

type failingRevisionFileService struct {
	stubFileService
	err error
}

func (s *failingRevisionFileService) ReadFileAtRevision(context.Context, sourcecontrol.RevisionQuery) (*sourcecontrol.FileReadResult, error) {
	return nil, s.err
}

func (s *recordingCheckoutsFileService) ListCheckouts(_ context.Context, query sourcecontrol.WorkspaceQuery) (*sourcecontrol.FileCheckoutsResult, error) {
	s.wsID = query.WorkspaceKey
	return &sourcecontrol.FileCheckoutsResult{Checkouts: []sourcecontrol.FileCheckout{{
		Kind:        "agent",
		Agent:       "agent-a",
		Repo:        "repo-a",
		Exists:      true,
		Branch:      "main",
		ChangeCount: 2,
	}}}, nil
}

func (s *recordingNavigationFileService) IndexFiles(_ context.Context, query sourcecontrol.LocationQuery) (*sourcecontrol.FileIndexResult, error) {
	s.indexWS = query.Location.WorkspaceKey
	s.indexScope = query.Location.Scope
	s.indexTarget = query.Location.Target
	s.indexRepo = query.Location.Repository
	return &sourcecontrol.FileIndexResult{Paths: []string{"src/main.go"}, Truncated: true, PartialReasons: []sourcecontrol.FilePartialReason{sourcecontrol.FilePartialFileCount}}, nil
}

func (s *recordingNavigationFileService) SearchFiles(_ context.Context, query sourcecontrol.SearchQuery) (*sourcecontrol.FileSearchResult, error) {
	s.searchWS = query.Location.WorkspaceKey
	s.searchScope = query.Location.Scope
	s.searchTarget = query.Location.Target
	s.searchRepo = query.Location.Repository
	s.searchReq = query.Search
	return &sourcecontrol.FileSearchResult{
		Results: []sourcecontrol.FileSearchFileResult{{
			Path: "src/main.go",
			Matches: []sourcecontrol.FileSearchMatch{{
				Line:    2,
				Col:     4,
				Preview: "const needle = true",
			}},
		}},
		LimitHit:       true,
		PartialReasons: []sourcecontrol.FilePartialReason{sourcecontrol.FilePartialResultCount},
	}, nil
}

func (s *recordingNavigationFileService) Status(_ context.Context, query sourcecontrol.LocationQuery) (sourcecontrol.FileGitStatusResult, error) {
	s.statusWS = query.Location.WorkspaceKey
	s.statusScope = query.Location.Scope
	s.statusTarget = query.Location.Target
	s.statusRepo = query.Location.Repository
	return sourcecontrol.FileGitStatusResult{Status: map[string]string{"src/main.go": " M"}, Errors: []sourcecontrol.FileCheckoutError{}}, nil
}

func scopedHandlersFixture(t *testing.T) (*mockFileOps, []handlerScopeCase) {
	t.Helper()
	wsRoot := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(wsRoot); err == nil {
		wsRoot = resolved
	}
	repoRoot := filepath.Join(wsRoot, "repo-a")
	agentRoot := filepath.Join(wsRoot, "worktrees", "repo-a", "agent-a")
	for _, dir := range []string{repoRoot, agentRoot} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	fileOps := &mockFileOps{
		resolveFunc: func(name string) (*sourcecontrol.Worktree, error) {
			if name != "agent-a" {
				return nil, errors.New("not found")
			}
			return &sourcecontrol.Worktree{Name: name, Path: agentRoot, RepoName: "repo-a"}, nil
		},
		resolveWsRootFunc: func() (string, error) {
			return wsRoot, nil
		},
		resolveWsDataFunc: func() (*sourcecontrol.WorkspaceTopology, error) {
			return &sourcecontrol.WorkspaceTopology{
				ID:     "test-ws",
				Path:   wsRoot,
				Repos:  []sourcecontrol.WorkspaceRepo{{Name: "repo-a", Path: repoRoot}},
				Agents: []sourcecontrol.WorkspaceAgent{{Name: "agent-a"}},
			}, nil
		},
	}
	return fileOps, []handlerScopeCase{
		{name: "workspace", scope: "workspace", root: wsRoot},
		{name: "repo", scope: "repo", target: "repo-a", root: repoRoot},
		{name: "agent", scope: "agent", target: "agent-a", root: agentRoot},
	}
}

func scopedPathURL(base string, sc handlerScopeCase, path string) string {
	url := base + "?scope=" + sc.scope
	if sc.target != "" {
		url += "&target=" + sc.target
	}
	if path != "" {
		url += "&path=" + path
	}
	return url
}

func scopedMoveURL(sc handlerScopeCase) string {
	url := "/api/workspaces/test-ws/files/move?scope=" + sc.scope
	if sc.target != "" {
		url += "&target=" + sc.target
	}
	return url
}

func TestHandleScopedFileIndex_UsesScopeTarget(t *testing.T) {
	svc := &recordingNavigationFileService{}
	h := HandleScopedFileIndex(svc)
	req := scopedReq("/api/workspaces/test-ws/files/index?scope=repo&target=repo-a")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if svc.indexWS != "test-ws" || svc.indexScope != sourcecontrol.ScopeRepo || svc.indexTarget != "repo-a" {
		t.Fatalf("recorded call = ws %q scope %q target %q", svc.indexWS, svc.indexScope, svc.indexTarget)
	}
	var body loomapi.FileIndexResponse
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !body.Truncated || len(body.Paths) != 1 || body.Paths[0] != "src/main.go" || !hasFilePartialReason(body.PartialReasons, loomapi.FilePartialReason(sourcecontrol.FilePartialFileCount)) {
		t.Fatalf("body = %+v", body)
	}
}

func TestHandleScopedFileRead_InspectionTimeoutIsGatewayTimeout(t *testing.T) {
	h := HandleScopedFileRead(&failingRevisionFileService{err: sourcecontrol.ErrTimeout})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, scopedReq("/api/workspaces/test-ws/files?scope=workspace&path=file.txt&rev=HEAD"))
	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleScopedFileRead_InvalidRevisionIsBadRequest(t *testing.T) {
	h := HandleScopedFileRead(&failingRevisionFileService{err: sourcecontrol.ErrInvalid})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, scopedReq("/api/workspaces/test-ws/files?scope=workspace&path=file.txt&rev=bad..range"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleScopedFileRead_MissingRevisionPathIsNotFound(t *testing.T) {
	h := HandleScopedFileRead(&failingRevisionFileService{err: sourcecontrol.ErrNotFound})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, scopedReq("/api/workspaces/test-ws/files?scope=workspace&path=untracked.txt&rev=HEAD"))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
}

func hasFilePartialReason(reasons []loomapi.FilePartialReason, want loomapi.FilePartialReason) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}

func TestHandleScopedFileIndex_UsesRepoQualifier(t *testing.T) {
	svc := &recordingNavigationFileService{}
	h := HandleScopedFileIndex(svc)
	req := scopedReq("/api/workspaces/test-ws/files/index?scope=agent&target=agent-a&repo=repo-b")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if svc.indexWS != "test-ws" || svc.indexScope != sourcecontrol.ScopeAgent || svc.indexTarget != "agent-a" || svc.indexRepo != "repo-b" {
		t.Fatalf("recorded call = ws %q scope %q target %q repo %q", svc.indexWS, svc.indexScope, svc.indexTarget, svc.indexRepo)
	}
}

func TestHandleScopedGitStatus_UsesScopeTarget(t *testing.T) {
	svc := &recordingNavigationFileService{}
	h := HandleScopedGitStatus(svc)
	req := scopedReq("/api/workspaces/test-ws/files/git-status?scope=repo&target=repo-a")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if svc.statusWS != "test-ws" || svc.statusScope != sourcecontrol.ScopeRepo || svc.statusTarget != "repo-a" {
		t.Fatalf("recorded call = ws %q scope %q target %q", svc.statusWS, svc.statusScope, svc.statusTarget)
	}
	var body loomapi.FileGitStatusResponse
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got := body.Status["src/main.go"]; got != " M" {
		t.Fatalf("body = %+v", body)
	}
}

func TestHandleScopedGitStatus_UsesRepoQualifier(t *testing.T) {
	svc := &recordingNavigationFileService{}
	h := HandleScopedGitStatus(svc)
	req := scopedReq("/api/workspaces/test-ws/files/git-status?scope=agent&target=agent-a&repo=repo-b")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if svc.statusWS != "test-ws" || svc.statusScope != sourcecontrol.ScopeAgent || svc.statusTarget != "agent-a" || svc.statusRepo != "repo-b" {
		t.Fatalf("recorded call = ws %q scope %q target %q repo %q", svc.statusWS, svc.statusScope, svc.statusTarget, svc.statusRepo)
	}
}

func TestHandleScopedFileSearch_DecodesRequest(t *testing.T) {
	svc := &recordingNavigationFileService{}
	h := HandleScopedFileSearch(svc)
	req := scopedReqBody(
		http.MethodPost,
		"/api/workspaces/test-ws/files/search?scope=agent&target=agent-a",
		`{"query":"needle","repo":"repo-b","regex":true,"include":["src/*.go"],"exclude":["vendor/*"],"caseSensitive":true}`,
	)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if svc.searchWS != "test-ws" || svc.searchScope != sourcecontrol.ScopeAgent || svc.searchTarget != "agent-a" || svc.searchRepo != "repo-b" {
		t.Fatalf("recorded call = ws %q scope %q target %q repo %q", svc.searchWS, svc.searchScope, svc.searchTarget, svc.searchRepo)
	}
	if svc.searchReq.Query != "needle" || !svc.searchReq.Regex || !svc.searchReq.CaseSensitive {
		t.Fatalf("search request flags = %+v", svc.searchReq)
	}
	if strings.Join(svc.searchReq.Include, ",") != "src/*.go" {
		t.Fatalf("include = %+v", svc.searchReq.Include)
	}
	if svc.searchReq.Exclude == nil || strings.Join(*svc.searchReq.Exclude, ",") != "vendor/*" {
		t.Fatalf("exclude = %+v", svc.searchReq.Exclude)
	}
	var body loomapi.FileSearchResponse
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !body.LimitHit || len(body.Results) != 1 || body.Results[0].Matches[0].Line != 2 || !hasFilePartialReason(body.PartialReasons, loomapi.FilePartialReason(sourcecontrol.FilePartialResultCount)) {
		t.Fatalf("body = %+v", body)
	}
}

func TestHandleFileCheckouts(t *testing.T) {
	svc := &recordingCheckoutsFileService{}
	h := HandleFileCheckouts(svc)
	req := scopedReq("/api/workspaces/test-ws/files/checkouts")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if svc.wsID != "test-ws" {
		t.Fatalf("wsID = %q", svc.wsID)
	}
	var body loomapi.FileCheckoutsResponse
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Checkouts) != 1 || body.Checkouts[0].Kind != loomapi.FileCheckoutKindAgent || body.Checkouts[0].Repo != "repo-a" || body.Checkouts[0].ChangeCount != 2 {
		t.Fatalf("body = %+v", body)
	}
}

func TestHandleScopedFileTree_WorkspaceRootListsEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "pkg"), 0755); err != nil {
		t.Fatal(err)
	}

	h := HandleScopedFileTree(NewFileService(wsRootFor(dir)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files/tree?scope=workspace"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp FileTreeResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	names := map[string]bool{}
	for _, e := range resp.Entries {
		names[e.Name] = true
	}
	if !names["readme.txt"] || !names["pkg"] {
		t.Errorf("entries = %+v, want readme.txt and pkg", resp.Entries)
	}
}

func TestHandleScopedFileTree_DefaultsToWorkspaceScope(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}

	h := HandleScopedFileTree(NewFileService(wsRootFor(dir)))
	w := httptest.NewRecorder()
	// No scope param — handler must default to the workspace scope.
	h.ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files/tree"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestHandleScopedFileRead_WorkspaceRootReadsFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("Hello, world!\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := HandleScopedFileRead(NewFileService(wsRootFor(dir)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files?scope=workspace&path=hello.txt"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp FileReadResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Content == nil || *resp.Content != "Hello, world!\n" {
		t.Errorf("content = %v, want %q", resp.Content, "Hello, world!\n")
	}
}

func TestHandleScopedFileCRUD_AllScopes(t *testing.T) {
	fileOps, scopes := scopedHandlersFixture(t)
	svc := NewFileService(fileOps)
	writeHandler := HandleScopedFileWrite(svc)
	readHandler := HandleScopedFileRead(svc)
	statHandler := HandleScopedFileStat(svc)
	treeHandler := HandleScopedFileTree(svc)
	deleteHandler := HandleScopedFileDelete(svc)
	mkdirHandler := HandleScopedFileMkdir(svc)
	moveHandler := HandleScopedFileMove(svc)

	for _, sc := range scopes {
		t.Run(sc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeHandler.ServeHTTP(w, scopedReqBody(http.MethodPut, scopedPathURL("/api/workspaces/test-ws/files", sc, ".env"), `{"content":"A=1"}`))
			if w.Code != http.StatusOK {
				t.Fatalf("write .env status = %d, want 200; body: %s", w.Code, w.Body.String())
			}

			w = httptest.NewRecorder()
			readHandler.ServeHTTP(w, scopedReq(scopedPathURL("/api/workspaces/test-ws/files", sc, ".env")))
			if w.Code != http.StatusOK {
				t.Fatalf("read .env status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			var readResp FileReadResult
			if err := json.NewDecoder(w.Body).Decode(&readResp); err != nil {
				t.Fatalf("decode read: %v", err)
			}
			if readResp.Content == nil || *readResp.Content != "A=1" || readResp.Truncated {
				t.Fatalf("read .env = %+v, want content A=1 and truncated=false", readResp)
			}

			if err := os.MkdirAll(filepath.Join(sc.root, ".git"), 0755); err != nil {
				t.Fatal(err)
			}
			w = httptest.NewRecorder()
			writeHandler.ServeHTTP(w, scopedReqBody(http.MethodPut, scopedPathURL("/api/workspaces/test-ws/files", sc, ".git/config"), `{"content":"git-ok"}`))
			if w.Code != http.StatusForbidden {
				t.Fatalf("write .git/config status = %d, want 403; body: %s", w.Code, w.Body.String())
			}
			w = httptest.NewRecorder()
			treeHandler.ServeHTTP(w, scopedReq(scopedPathURL("/api/workspaces/test-ws/files/tree", sc, "")))
			if w.Code != http.StatusOK {
				t.Fatalf("tree status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			var treeResp FileTreeResult
			if err := json.NewDecoder(w.Body).Decode(&treeResp); err != nil {
				t.Fatalf("decode tree: %v", err)
			}
			for _, entry := range treeResp.Entries {
				if entry.Name == ".git" {
					t.Fatalf(".git listed for %s scope: %+v", sc.name, treeResp.Entries)
				}
			}
			w = httptest.NewRecorder()
			readHandler.ServeHTTP(w, scopedReq(scopedPathURL("/api/workspaces/test-ws/files", sc, ".git/config")))
			if w.Code != http.StatusForbidden {
				t.Fatalf("read .git/config status = %d, want 403; body: %s", w.Code, w.Body.String())
			}

			w = httptest.NewRecorder()
			mkdirHandler.ServeHTTP(w, scopedReqBody(http.MethodPost, scopedPathURL("/api/workspaces/test-ws/files/mkdir", sc, "dir/sub"), ""))
			if w.Code != http.StatusOK {
				t.Fatalf("mkdir status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			if err := os.WriteFile(filepath.Join(sc.root, "dir", "sub", "file.txt"), []byte("x"), 0644); err != nil {
				t.Fatal(err)
			}
			stat := httptest.NewRecorder()
			statHandler.ServeHTTP(stat, scopedReq(scopedPathURL("/api/workspaces/test-ws/files/stat", sc, "dir")))
			if stat.Code != http.StatusOK {
				t.Fatalf("stat directory status = %d, want 200; body: %s", stat.Code, stat.Body.String())
			}
			dirVersion := stat.Header().Get("ETag")
			if dirVersion == "" {
				t.Fatal("stat directory did not return an ETag")
			}
			w = httptest.NewRecorder()
			deleteReq := scopedReqBody(http.MethodDelete, scopedPathURL("/api/workspaces/test-ws/files", sc, "dir"), "")
			deleteReq.Header.Set("If-Match", dirVersion)
			deleteHandler.ServeHTTP(w, deleteReq)
			if w.Code != http.StatusConflict {
				t.Fatalf("delete nonempty status = %d, want 409; body: %s", w.Code, w.Body.String())
			}
			w = httptest.NewRecorder()
			deleteReq = scopedReqBody(http.MethodDelete, scopedPathURL("/api/workspaces/test-ws/files", sc, "dir")+"&recursive=1", "")
			deleteReq.Header.Set("If-Match", dirVersion)
			deleteHandler.ServeHTTP(w, deleteReq)
			if w.Code != http.StatusOK {
				t.Fatalf("recursive delete status = %d, want 200; body: %s", w.Code, w.Body.String())
			}

			if err := os.WriteFile(filepath.Join(sc.root, "file-path"), []byte("x"), 0644); err != nil {
				t.Fatal(err)
			}
			w = httptest.NewRecorder()
			mkdirHandler.ServeHTTP(w, scopedReqBody(http.MethodPost, scopedPathURL("/api/workspaces/test-ws/files/mkdir", sc, "file-path"), ""))
			if w.Code != http.StatusConflict {
				t.Fatalf("mkdir file status = %d, want 409; body: %s", w.Code, w.Body.String())
			}

			if err := os.WriteFile(filepath.Join(sc.root, "move-src"), []byte("src"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sc.root, "move-dst"), []byte("dst"), 0644); err != nil {
				t.Fatal(err)
			}
			grant := testFileGrantIssuer.ReadWrite(true)
			location := sourcecontrol.FileLocation{WorkspaceKey: "test-ws", Scope: sourcecontrol.FileScope(sc.scope), Target: sc.target}
			sourceStat, err := svc.StatPath(context.Background(), sourcecontrol.PathQuery{Grant: grant, Location: location, Path: "move-src"})
			if err != nil {
				t.Fatalf("stat move source: %v", err)
			}
			destinationStat, err := svc.StatPath(context.Background(), sourcecontrol.PathQuery{Grant: grant, Location: location, Path: "move-dst"})
			if err != nil {
				t.Fatalf("stat move destination: %v", err)
			}
			w = httptest.NewRecorder()
			moveHandler.ServeHTTP(w, scopedReqBody(http.MethodPatch, scopedMoveURL(sc), `{"from":"move-src","to":"move-dst","source_version":"`+sourceStat.Version+`"}`))
			if w.Code != http.StatusConflict {
				t.Fatalf("move conflict status = %d, want 409; body: %s", w.Code, w.Body.String())
			}
			w = httptest.NewRecorder()
			moveHandler.ServeHTTP(w, scopedReqBody(http.MethodPatch, scopedMoveURL(sc), `{"from":"move-src","to":"move-dst","overwrite":true,"source_version":"`+sourceStat.Version+`","destination_version":"`+destinationStat.Version+`"}`))
			if w.Code != http.StatusOK {
				t.Fatalf("move overwrite status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestHandleScopedFileTree_GitDirHiddenFromListing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "src.go"), []byte("package x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[core]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := HandleScopedFileTree(NewFileService(wsRootFor(dir)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files/tree?scope=workspace"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp FileTreeResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, e := range resp.Entries {
		if e.Name == ".git" {
			t.Fatalf(".git must be hidden from the listing; entries: %+v", resp.Entries)
		}
	}
}

func TestHandleScopedFileRead_GitPathForbidden(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("url = https://x:tok@host/r\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := HandleScopedFileRead(NewFileService(wsRootFor(dir)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files?scope=workspace&path=.git/config"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for explicit .git path; body: %s", w.Code, w.Body.String())
	}
}

func TestHandleScopedFileTree_PathTraversalDenied(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	h := HandleScopedFileTree(NewFileService(wsRootFor(dir)))
	for _, p := range []string{"../../../etc/passwd", "subdir/../../../etc/shadow"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files/tree?scope=workspace&path="+p))
		if w.Code == http.StatusOK {
			t.Errorf("path=%q: got 200, want rejection", p)
		}
	}
}

func TestHandleScopedFile_UnsupportedScope(t *testing.T) {
	dir := t.TempDir()
	h := HandleScopedFileTree(NewFileService(wsRootFor(dir)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files/tree?scope=bogus"))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unsupported scope; body: %s", w.Code, w.Body.String())
	}
}

func TestHandleScopedFileTree_WorkspaceScopeRejectsTarget(t *testing.T) {
	dir := t.TempDir()
	h := HandleScopedFileTree(NewFileService(wsRootFor(dir)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files/tree?scope=workspace&target=loomcli"))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 when workspace scope gets a target; body: %s", w.Code, w.Body.String())
	}
}

func TestHandleScopedFile_WorkspaceNotCheckedOut(t *testing.T) {
	fo := &mockFileOps{
		resolveWsRootFunc: func() (string, error) {
			return "", errors.New("workspace \"test-ws\" is not checked out on this machine at /x")
		},
	}
	h := HandleScopedFileTree(NewFileService(fo))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files/tree?scope=workspace"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when workspace not checked out; body: %s", w.Code, w.Body.String())
	}
}

func TestHandleScopedFile_InvalidTargets(t *testing.T) {
	fileOps, _ := scopedHandlersFixture(t)
	h := HandleScopedFileTree(NewFileService(fileOps))

	for _, target := range []string{
		"/api/workspaces/test-ws/files/tree?scope=repo&target=missing",
		"/api/workspaces/test-ws/files/tree?scope=agent&target=missing",
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, scopedReq(target))
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404; body: %s", target, w.Code, w.Body.String())
		}
	}
}
