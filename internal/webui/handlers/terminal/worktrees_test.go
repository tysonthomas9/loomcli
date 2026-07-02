package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/worktreegroups"
)

const worktreeTestWS = "WT"

type worktreeHarness struct {
	ctx        context.Context
	workspace  string
	wsPath     string
	repos      map[string]string
	store      store.Store
	groupStore *worktreegroups.Store
	service    *WorktreeGroupService
}

func newWorktreeHarness(t *testing.T, repoNames ...string) *worktreeHarness {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	ctx := context.Background()
	wsPath := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(wsPath, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: worktreeTestWS, Name: "Worktree Test"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	repos := make(map[string]string, len(repoNames))
	for _, name := range repoNames {
		repoPath := filepath.Join(wsPath, name)
		initGitRepo(t, repoPath)
		if _, err := st.Repos().Create(ctx, store.RepoCreate{
			WorkspaceKey:  worktreeTestWS,
			Name:          name,
			Remote:        "origin",
			DefaultBranch: "main",
		}); err != nil {
			t.Fatalf("create repo %s: %v", name, err)
		}
		repos[name] = repoPath
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			worktreeTestWS: {Path: wsPath, Repos: repos},
		},
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	groupStore := worktreegroups.NewStore(rdb, nil)
	svc := NewWorktreeGroupService(st, groupStore)
	svc.newID = func() string { return "group-id" }
	svc.repoTimeout = 5 * time.Second

	return &worktreeHarness{
		ctx:        ctx,
		workspace:  worktreeTestWS,
		wsPath:     wsPath,
		repos:      repos,
		store:      st,
		groupStore: groupStore,
		service:    svc,
	}
}

func initGitRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	git(t, path, "init")
	git(t, path, "checkout", "-b", "main")
	git(t, path, "config", "user.name", "Test User")
	git(t, path, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(path, "README.md"), "initial\n")
	git(t, path, "add", "README.md")
	git(t, path, "commit", "-m", "initial")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // test fixture setup shells out to git in temp repos.
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}

func gitErr(dir string, args ...string) error {
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // test assertions shell out to git in temp repos.
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.New(strings.TrimSpace(string(out)))
	}
	return nil
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func groupRoot(t *testing.T, h *worktreeHarness, name string) string {
	t.Helper()
	return filepath.Join(h.wsPath, ".loom", "terminal-worktrees", name)
}

func resultFor(t *testing.T, results []WorktreeGroupResult, repo string) WorktreeGroupResult {
	t.Helper()
	for _, result := range results {
		if result.Repo == repo {
			return result
		}
	}
	t.Fatalf("missing result for repo %s in %+v", repo, results)
	return WorktreeGroupResult{}
}

func assertPathExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be missing, stat err=%v", path, err)
	}
}

func assertBranchExists(t *testing.T, repoPath, branch string) {
	t.Helper()
	if err := gitErr(repoPath, "rev-parse", "--verify", "refs/heads/"+branch); err != nil {
		t.Fatalf("branch %s missing: %v", branch, err)
	}
}

func assertBranchMissing(t *testing.T, repoPath, branch string) {
	t.Helper()
	if err := gitErr(repoPath, "rev-parse", "--verify", "refs/heads/"+branch); err == nil {
		t.Fatalf("branch %s exists, want missing", branch)
	}
}

func serviceKind(t *testing.T, err error) service.ErrorKind {
	t.Helper()
	var svcErr *service.ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("error = %T %v, want service error", err, err)
	}
	return svcErr.Kind
}

func postWorktreeGroup(t *testing.T, svc *WorktreeGroupService, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WT/terminal/worktrees", bytes.NewBufferString(body))
	req = req.WithContext(middleware.WithWorkspace(req.Context(), worktreeTestWS))
	w := httptest.NewRecorder()
	HandleCreateWorktreeGroup(svc).ServeHTTP(w, req)
	return w
}

func TestWorktreeGroupsCreateDefaultAllLocalAndList(t *testing.T) {
	h := newWorktreeHarness(t, "api", "web")

	resp, err := h.service.Create(h.ctx, h.workspace, CreateWorktreeGroupRequest{Name: "feature-auth"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if resp.Group == nil {
		t.Fatal("group is nil")
	}
	root := groupRoot(t, h, "feature-auth")
	if resp.Group.Root != root {
		t.Fatalf("root = %q, want %q", resp.Group.Root, root)
	}
	if len(resp.Group.Members) != 2 || len(resp.Results) != 2 {
		t.Fatalf("group members/results = %+v / %+v, want two repos", resp.Group.Members, resp.Results)
	}
	for _, repo := range []string{"api", "web"} {
		if got := resultFor(t, resp.Results, repo).Status; got != worktreeStatusCreated {
			t.Fatalf("%s status = %q, want created", repo, got)
		}
		assertPathExists(t, filepath.Join(root, repo, ".git"))
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/workspaces/WT/terminal/worktrees", nil)
	listReq = listReq.WithContext(middleware.WithWorkspace(listReq.Context(), worktreeTestWS))
	w := httptest.NewRecorder()
	HandleListWorktreeGroups(h.service).ServeHTTP(w, listReq)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", w.Code, w.Body.String())
	}
	var listResp struct {
		Groups []worktreegroups.TerminalWorktreeGroup `json:"groups"`
	}
	if err := json.NewDecoder(w.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Groups) != 1 || listResp.Groups[0].Name != "feature-auth" {
		t.Fatalf("groups = %+v, want feature-auth", listResp.Groups)
	}
}

func TestWorktreeGroupsExplicitSubsetSingleRepo(t *testing.T) {
	h := newWorktreeHarness(t, "api", "web")

	resp, err := h.service.Create(h.ctx, h.workspace, CreateWorktreeGroupRequest{
		Name:  "feature-api",
		Repos: []string{"api"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	root := groupRoot(t, h, "feature-api")
	if len(resp.Group.Members) != 1 || resp.Group.Members[0].RepoName != "api" {
		t.Fatalf("members = %+v, want only api", resp.Group.Members)
	}
	assertPathExists(t, filepath.Join(root, "api", ".git"))
	assertPathMissing(t, filepath.Join(root, "web"))
}

func TestHandleCreateWorktreeGroupDuplicateBeforeDisk(t *testing.T) {
	h := newWorktreeHarness(t, "api")
	if err := h.groupStore.Add(h.ctx, h.workspace, worktreegroups.TerminalWorktreeGroup{Name: "feature-dupe"}); err != nil {
		t.Fatalf("seed group: %v", err)
	}

	w := postWorktreeGroup(t, h.service, `{"name":"feature-dupe"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
	root := groupRoot(t, h, "feature-dupe")
	assertPathMissing(t, root)
	assertBranchMissing(t, h.repos["api"], "feature-dupe")
}

func TestWorktreeGroupsExplicitNonLocalRepo(t *testing.T) {
	h := newWorktreeHarness(t, "api")
	if _, err := h.store.Repos().Create(h.ctx, store.RepoCreate{WorkspaceKey: h.workspace, Name: "missing"}); err != nil {
		t.Fatalf("create missing repo row: %v", err)
	}

	resp, err := h.service.Create(h.ctx, h.workspace, CreateWorktreeGroupRequest{
		Name:  "feature-missing",
		Repos: []string{"missing"},
	})
	if err == nil {
		t.Fatal("Create succeeded, want validation error")
	}
	if kind := serviceKind(t, err); kind != service.KindValidation {
		t.Fatalf("kind = %q, want validation", kind)
	}
	if got := resultFor(t, resp.Results, "missing").Status; got != worktreeStatusError {
		t.Fatalf("missing status = %q, want error", got)
	}
}

func TestWorktreeGroupsRollbackOnRepoFailure(t *testing.T) {
	h := newWorktreeHarness(t, "api", "web")
	root := groupRoot(t, h, "feature-rollback")
	if err := os.MkdirAll(filepath.Join(root, "web"), 0o755); err != nil {
		t.Fatalf("mkdir occupied target: %v", err)
	}
	writeFile(t, filepath.Join(root, "web", "occupied.txt"), "busy\n")

	w := postWorktreeGroup(t, h.service, `{"name":"feature-rollback"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	var body worktreeGroupErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got := resultFor(t, body.Results, "api").Status; got != worktreeStatusRolledBack {
		t.Fatalf("api status = %q, want rolled_back; results=%+v", got, body.Results)
	}
	if got := resultFor(t, body.Results, "web").Status; got != worktreeStatusError {
		t.Fatalf("web status = %q, want error; results=%+v", got, body.Results)
	}
	assertPathMissing(t, filepath.Join(root, "api"))
	assertBranchMissing(t, h.repos["api"], "feature-rollback")
	assertPathExists(t, filepath.Join(root, "web", "occupied.txt"))
	groups, err := h.groupStore.List(h.ctx, h.workspace)
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("groups persisted after rollback: %+v", groups)
	}
}

type failingWorktreeGroupStore struct {
	mu     sync.Mutex
	groups []worktreegroups.TerminalWorktreeGroup
	addErr error
}

func (s *failingWorktreeGroupStore) List(_ context.Context, _ string) ([]worktreegroups.TerminalWorktreeGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]worktreegroups.TerminalWorktreeGroup(nil), s.groups...), nil
}

func (s *failingWorktreeGroupStore) WithWorkspaceLock(_ string, fn func(worktreeGroupLockedStore) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn((*failingLockedWorktreeGroupStore)(s))
}

type failingLockedWorktreeGroupStore failingWorktreeGroupStore

func (s *failingLockedWorktreeGroupStore) Get(_ context.Context, name string) (*worktreegroups.TerminalWorktreeGroup, error) {
	for _, group := range s.groups {
		if group.Name == name {
			found := group
			return &found, nil
		}
	}
	return nil, nil
}

func (s *failingLockedWorktreeGroupStore) Add(_ context.Context, group worktreegroups.TerminalWorktreeGroup) error {
	if s.addErr != nil {
		return s.addErr
	}
	s.groups = append(s.groups, group)
	return nil
}

func TestWorktreeGroupsPersistFailureRollsBack(t *testing.T) {
	h := newWorktreeHarness(t, "api")
	failing := &failingWorktreeGroupStore{addErr: errors.New("redis down")}
	h.service.groupStore = failing

	w := postWorktreeGroup(t, h.service, `{"name":"feature-persist"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
	var body worktreeGroupErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got := resultFor(t, body.Results, "api").Status; got != worktreeStatusRolledBack {
		t.Fatalf("api status = %q, want rolled_back; results=%+v", got, body.Results)
	}
	root := groupRoot(t, h, "feature-persist")
	assertPathMissing(t, filepath.Join(root, "api"))
	assertPathMissing(t, root)
	assertBranchMissing(t, h.repos["api"], "feature-persist")
}

func TestWorktreeGroupsAdoptsCrashLeftover(t *testing.T) {
	h := newWorktreeHarness(t, "api")
	target := filepath.Join(groupRoot(t, h, "feature-leftover"), "api")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir group root: %v", err)
	}
	git(t, h.repos["api"], "worktree", "add", "-b", "feature-leftover", target)

	resp, err := h.service.Create(h.ctx, h.workspace, CreateWorktreeGroupRequest{Name: "feature-leftover"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := resultFor(t, resp.Results, "api").Status; got != worktreeStatusExists {
		t.Fatalf("api status = %q, want exists", got)
	}
	if len(resp.Group.Members) != 1 || resp.Group.Members[0].Path != target {
		t.Fatalf("members = %+v, want adopted target %s", resp.Group.Members, target)
	}
}

func TestWorktreeGroupsWrongBranchLeftoverErrors(t *testing.T) {
	h := newWorktreeHarness(t, "api")
	target := filepath.Join(groupRoot(t, h, "feature-leftover"), "api")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir group root: %v", err)
	}
	git(t, h.repos["api"], "worktree", "add", "-b", "other-branch", target)

	resp, err := h.service.Create(h.ctx, h.workspace, CreateWorktreeGroupRequest{Name: "feature-leftover"})
	if err == nil {
		t.Fatal("Create succeeded, want error")
	}
	if got := resultFor(t, resp.Results, "api").Status; got != worktreeStatusError {
		t.Fatalf("api status = %q, want error", got)
	}
	groups, _ := h.groupStore.List(h.ctx, h.workspace)
	if len(groups) != 0 {
		t.Fatalf("persisted groups = %+v, want none", groups)
	}
}

func TestWorktreeGroupsBranchCheckedOutElsewhereConflicts(t *testing.T) {
	h := newWorktreeHarness(t, "api")
	other := filepath.Join(t.TempDir(), "elsewhere")
	git(t, h.repos["api"], "worktree", "add", "-b", "feature-conflict", other)

	resp, err := h.service.Create(h.ctx, h.workspace, CreateWorktreeGroupRequest{Name: "feature-conflict"})
	if err == nil {
		t.Fatal("Create succeeded, want conflict")
	}
	if kind := serviceKind(t, err); kind != service.KindConflict {
		t.Fatalf("kind = %q, want conflict", kind)
	}
	if got := resultFor(t, resp.Results, "api").Status; got != worktreeStatusConflict {
		t.Fatalf("api status = %q, want conflict", got)
	}
}

func TestWorktreeGroupsPreExistingBranchReused(t *testing.T) {
	h := newWorktreeHarness(t, "api")
	git(t, h.repos["api"], "branch", "feature-reuse")

	resp, err := h.service.Create(h.ctx, h.workspace, CreateWorktreeGroupRequest{Name: "feature-reuse"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := resultFor(t, resp.Results, "api").Status; got != worktreeStatusReused {
		t.Fatalf("api status = %q, want reused", got)
	}
	member := resp.Group.Members[0]
	if !member.ReusedBranch || member.BaseBranch != "" {
		t.Fatalf("member = %+v, want reused with empty base", member)
	}
}

func TestWorktreeGroupsReusedBranchKeptOnRollback(t *testing.T) {
	h := newWorktreeHarness(t, "api", "web")
	git(t, h.repos["api"], "branch", "feature-reuse-fail")
	root := groupRoot(t, h, "feature-reuse-fail")
	if err := os.MkdirAll(filepath.Join(root, "web"), 0o755); err != nil {
		t.Fatalf("mkdir occupied target: %v", err)
	}
	writeFile(t, filepath.Join(root, "web", "occupied.txt"), "busy\n")

	resp, err := h.service.Create(h.ctx, h.workspace, CreateWorktreeGroupRequest{Name: "feature-reuse-fail"})
	if err == nil {
		t.Fatal("Create succeeded, want failure")
	}
	if got := resultFor(t, resp.Results, "api").Status; got != worktreeStatusRolledBack {
		t.Fatalf("api status = %q, want rolled_back", got)
	}
	assertBranchExists(t, h.repos["api"], "feature-reuse-fail")
}

func TestWorktreeGroupsOccupiedTargetEmptyAndNonEmptyRetry(t *testing.T) {
	h := newWorktreeHarness(t, "api")
	emptyTarget := filepath.Join(groupRoot(t, h, "feature-empty"), "api")
	if err := os.MkdirAll(emptyTarget, 0o755); err != nil {
		t.Fatalf("mkdir empty target: %v", err)
	}
	resp, err := h.service.Create(h.ctx, h.workspace, CreateWorktreeGroupRequest{Name: "feature-empty"})
	if err != nil {
		t.Fatalf("Create with empty target: %v", err)
	}
	if got := resultFor(t, resp.Results, "api").Status; got != worktreeStatusCreated {
		t.Fatalf("api status = %q, want created", got)
	}

	h2 := newWorktreeHarness(t, "api")
	root := groupRoot(t, h2, "feature-occupied")
	occupiedTarget := filepath.Join(root, "api")
	if err := os.MkdirAll(occupiedTarget, 0o755); err != nil {
		t.Fatalf("mkdir occupied target: %v", err)
	}
	writeFile(t, filepath.Join(occupiedTarget, "file.txt"), "busy\n")
	for i := 0; i < 2; i++ {
		resp, err = h2.service.Create(h2.ctx, h2.workspace, CreateWorktreeGroupRequest{Name: "feature-occupied"})
		if err == nil {
			t.Fatalf("retry %d succeeded, want error", i)
		}
		if got := resultFor(t, resp.Results, "api").Status; got != worktreeStatusError {
			t.Fatalf("retry %d status = %q, want error", i, got)
		}
	}
	assertPathExists(t, filepath.Join(occupiedTarget, "file.txt"))
}

func TestWorktreeGroupsBaseOmittedForksLocalHEAD(t *testing.T) {
	h := newWorktreeHarness(t, "api")
	writeFile(t, filepath.Join(h.repos["api"], "local.txt"), "local only\n")
	git(t, h.repos["api"], "add", "local.txt")
	git(t, h.repos["api"], "commit", "-m", "local only")

	resp, err := h.service.Create(h.ctx, h.workspace, CreateWorktreeGroupRequest{Name: "feature-local"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	targetFile := filepath.Join(resp.Group.Root, "api", "local.txt")
	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("read local file in worktree: %v", err)
	}
	if string(data) != "local only\n" {
		t.Fatalf("local file = %q, want local commit content", string(data))
	}
	if resp.Group.Members[0].BaseBranch != "main" {
		t.Fatalf("base branch = %q, want main", resp.Group.Members[0].BaseBranch)
	}
}

func TestWorktreeGroupsBaseProvidedFetchesOriginBase(t *testing.T) {
	h := newWorktreeHarness(t, "api")
	remote := filepath.Join(t.TempDir(), "remote.git")
	git(t, "", "init", "--bare", remote)
	git(t, h.repos["api"], "remote", "add", "origin", remote)
	git(t, h.repos["api"], "push", "origin", "main")

	seed := filepath.Join(t.TempDir(), "seed")
	git(t, "", "clone", "--branch", "main", remote, seed)
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(seed, "remote.txt"), "remote update\n")
	git(t, seed, "add", "remote.txt")
	git(t, seed, "commit", "-m", "remote update")
	git(t, seed, "push", "origin", "main")

	resp, err := h.service.Create(h.ctx, h.workspace, CreateWorktreeGroupRequest{Name: "feature-remote", Base: "main"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(resp.Group.Root, "api", "remote.txt"))
	if err != nil {
		t.Fatalf("read remote file: %v", err)
	}
	if string(data) != "remote update\n" {
		t.Fatalf("remote file = %q, want fetched content", string(data))
	}
}

func TestWorktreeGroupsDetachedHeadRecordsSHA(t *testing.T) {
	h := newWorktreeHarness(t, "api")
	wantSHA := git(t, h.repos["api"], "rev-parse", "--short", "HEAD")
	git(t, h.repos["api"], "checkout", "--detach", "HEAD")

	resp, err := h.service.Create(h.ctx, h.workspace, CreateWorktreeGroupRequest{Name: "feature-detached"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	member := resp.Group.Members[0]
	if !member.BaseDetached || member.BaseBranch != wantSHA {
		t.Fatalf("member = %+v, want detached base %s", member, wantSHA)
	}
}

func TestWorktreeGroupsBaseEqualsNamePerRepoError(t *testing.T) {
	h := newWorktreeHarness(t, "api")

	resp, err := h.service.Create(h.ctx, h.workspace, CreateWorktreeGroupRequest{Name: "feature-same", Base: "feature-same"})
	if err == nil {
		t.Fatal("Create succeeded, want error")
	}
	if kind := serviceKind(t, err); kind != service.KindValidation {
		t.Fatalf("kind = %q, want validation", kind)
	}
	result := resultFor(t, resp.Results, "api")
	if result.Status != worktreeStatusError || !strings.Contains(result.Message, "base branch") {
		t.Fatalf("result = %+v, want base branch error", result)
	}
}
