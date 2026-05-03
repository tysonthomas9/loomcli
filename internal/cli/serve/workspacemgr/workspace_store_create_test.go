package workspacemgr

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

func TestStoreBackedCreateEmptyWorkspaceCreatesStoreAndLocalState(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := initTestGitRepo(t, t.TempDir(), "app")
	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")

	result, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name:   "my-ws",
		Type:   "empty",
		Repos:  []string{src},
		Branch: "feature-work",
		Path:   wsPath,
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if result.WorkspaceID != "MY-WS" {
		t.Fatalf("WorkspaceID = %q, want MY-WS", result.WorkspaceID)
	}
	if result.WorkspacePath != wsPath {
		t.Fatalf("WorkspacePath = %q, want %q", result.WorkspacePath, wsPath)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "app", ".git")); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}

	ws, err := st.Workspaces().Get(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("workspace not stored: %v", err)
	}
	if ws.Name != "my-ws" {
		t.Fatalf("workspace name = %q, want my-ws", ws.Name)
	}
	repos, err := st.Repos().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "app" {
		t.Fatalf("repos = %#v, want app", repos)
	}
	roles, err := st.Roles().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	if len(roles) != 2 || !hasRole(roles, "plan") || !hasRole(roles, "task") {
		t.Fatalf("roles = %#v, want plan and task", roles)
	}

	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if sc.LastWorkspace != "MY-WS" {
		t.Fatalf("LastWorkspace = %q, want MY-WS", sc.LastWorkspace)
	}
	local := sc.Workspaces["MY-WS"]
	if local.Path != wsPath {
		t.Fatalf("local path = %q, want %q", local.Path, wsPath)
	}
	if local.Repos["app"] != filepath.Join(wsPath, "app") {
		t.Fatalf("local repo path = %q", local.Repos["app"])
	}
}

func TestStoreBackedAddReposAttachesLocalRepoToEmptyWorkspace(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")

	if _, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	src := initTestGitRepo(t, t.TempDir(), "api")
	addFn := BuildStoreBackedAddRepos(st)
	result, err := addFn(context.Background(), service.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		Repos:       []string{src},
		Branch:      "feature-work",
	})
	if err != nil {
		t.Fatalf("add repo: %v", err)
	}
	if result.WorkspaceID != "MY-WS" || result.WorkspacePath != wsPath {
		t.Fatalf("result = %#v, want MY-WS at %s", result, wsPath)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "api", ".git")); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}

	repos, err := st.Repos().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "api" || repos[0].DefaultBranch != "feature-work" {
		t.Fatalf("repos = %#v, want api on feature-work", repos)
	}

	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	local := sc.Workspaces["MY-WS"]
	if local.Path != wsPath {
		t.Fatalf("local path = %q, want %q", local.Path, wsPath)
	}
	if local.Repos["api"] != filepath.Join(wsPath, "api") {
		t.Fatalf("local repo path = %q", local.Repos["api"])
	}
}

func TestStoreBackedCreateEmptyWorkspaceRollsBackOnRepoStoreError(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := initTestGitRepo(t, t.TempDir(), "app")
	base := memstore.New()
	st := &repoFailStore{Store: base, err: errors.New("repo create failed")}
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")

	if _, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name:   "my-ws",
		Type:   "empty",
		Repos:  []string{src},
		Branch: "feature-work",
		Path:   wsPath,
	}); err == nil {
		t.Fatal("create workspace succeeded, want repo store error")
	}

	if _, err := base.Workspaces().Get(context.Background(), "MY-WS"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("workspace was not rolled back, err=%v", err)
	}
	if _, err := os.Stat(wsPath); !os.IsNotExist(err) {
		t.Fatalf("workspace path still exists after rollback, stat err=%v", err)
	}
	if out := gitOutput(t, src, "branch", "--list", "feature-work"); out != "" {
		t.Fatalf("rollback left branch feature-work behind: %q", out)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if sc.LastWorkspace != "" || len(sc.Workspaces) != 0 {
		t.Fatalf("state cache was written on rollback: %#v", sc)
	}
}

func TestStoreBackedCreateCloneWorkspacePersistsLifecycleAndRepos(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := initTestGitRepo(t, t.TempDir(), "app")
	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "clone-ws")

	result, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name:      "clone-ws",
		Type:      "clone",
		CloneURLs: []string{src},
		Branch:    "main",
		Path:      wsPath,
	})
	if err != nil {
		t.Fatalf("clone workspace: %v", err)
	}
	if result.WorkspaceID != "CLONE-WS" {
		t.Fatalf("WorkspaceID = %q, want CLONE-WS", result.WorkspaceID)
	}
	ws, err := st.Workspaces().Get(context.Background(), "CLONE-WS")
	if err != nil {
		t.Fatalf("workspace not stored: %v", err)
	}
	if ws.State != domain.WorkspaceStateReady {
		t.Fatalf("workspace state = %q, want ready", ws.State)
	}
	repos, err := st.Repos().List(context.Background(), "CLONE-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "app" || repos[0].RemoteURL != src || repos[0].SourceRepoID != "app" {
		t.Fatalf("repos = %#v, want cloned app repo with remote URL", repos)
	}
	roles, err := st.Roles().List(context.Background(), "CLONE-WS")
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	if len(roles) != 2 || !hasRole(roles, "plan") || !hasRole(roles, "task") {
		t.Fatalf("roles = %#v, want plan and task", roles)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "app", ".git")); err != nil {
		t.Fatalf("clone checkout not created: %v", err)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if sc.LastWorkspace != "CLONE-WS" {
		t.Fatalf("LastWorkspace = %q, want CLONE-WS", sc.LastWorkspace)
	}
	if sc.Workspaces["CLONE-WS"].Repos["app"] != filepath.Join(wsPath, "app") {
		t.Fatalf("state repo path = %q", sc.Workspaces["CLONE-WS"].Repos["app"])
	}
}

func TestStoreBackedCreateCloneWorkspaceNormalizesRepoNameForFleetStore(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := initTestGitRepo(t, t.TempDir(), "Hello-World")
	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "clone-ws")

	_, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name:      "clone-ws",
		Type:      "clone",
		CloneURLs: []string{src},
		Branch:    "main",
		Path:      wsPath,
	})
	if err != nil {
		t.Fatalf("clone workspace: %v", err)
	}

	repos, err := st.Repos().List(context.Background(), "CLONE-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "hello-world" || repos[0].SourceRepoID != "hello-world" {
		t.Fatalf("repos = %#v, want normalized hello-world repo", repos)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "hello-world", ".git")); err != nil {
		t.Fatalf("clone checkout not created at normalized path: %v", err)
	}
}

func TestStoreBackedCreateCloneWorkspaceRollsBackStoreOnCloneFailure(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "clone-ws")

	_, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name:      "clone-ws",
		Type:      "clone",
		CloneURLs: []string{filepath.Join(t.TempDir(), "missing")},
		Path:      wsPath,
	})
	if err == nil {
		t.Fatal("clone workspace succeeded, want git clone error")
	}

	if _, getErr := st.Workspaces().Get(context.Background(), "CLONE-WS"); !errors.Is(getErr, domain.ErrNotFound) {
		t.Fatalf("workspace was not rolled back, err=%v", getErr)
	}
	if _, statErr := os.Stat(wsPath); !os.IsNotExist(statErr) {
		t.Fatalf("workspace path still exists after clone failure, stat err=%v", statErr)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if sc.LastWorkspace != "" || len(sc.Workspaces) != 0 {
		t.Fatalf("state cache was written on clone rollback: %#v", sc)
	}
}

func TestLegacyCreateEmptyWorkspaceNoReposFinalizesConfig(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := initTestGitRepo(t, t.TempDir(), "legacy-app")
	cfg := &config.LoomConfig{Workspaces: make(map[string]config.WorkspaceConfig)}
	var saveCount int
	save := func(*config.LoomConfig) error {
		saveCount++
		return nil
	}
	wsPath := filepath.Join(loomDir, "workspaces", "legacy-ws")

	result, repos, agentNames, err := createEmptyWorkspace(context.Background(), cfg, "legacy-ws", wsPath, "feature", []string{src}, save)
	if err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}
	if result.WorkspaceID == "" || result.WorkspacePath != wsPath || !result.DeferDaemonStart {
		t.Fatalf("result = %#v, want deferred workspace at %s", result, wsPath)
	}
	if len(repos) != 1 || repos[0].Name != "legacy-app" {
		t.Fatalf("repos = %#v, want legacy-app", repos)
	}
	if len(agentNames) == 0 {
		t.Fatal("agent names were not generated")
	}
	ws := cfg.Workspaces["legacy-ws"]
	if ws.State != config.WorkspaceStateInitializing {
		t.Fatalf("state = %q, want initializing", ws.State)
	}
	if cfg.DefaultWorkspace != "legacy-ws" {
		t.Fatalf("default workspace = %q, want legacy-ws", cfg.DefaultWorkspace)
	}
	if saveCount < 3 {
		t.Fatalf("saveCount = %d, want at least 3 lifecycle saves", saveCount)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "loom.yaml")); err != nil {
		t.Fatalf("loom.yaml not written: %v", err)
	}
}

func TestLegacyCreateCloneWorkspaceFailureMarksError(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	cfg := &config.LoomConfig{Workspaces: make(map[string]config.WorkspaceConfig)}
	save := func(*config.LoomConfig) error { return nil }
	wsPath := filepath.Join(loomDir, "workspaces", "clone-fail")

	_, _, _, err := createCloneWorkspace(context.Background(), cfg, "clone-fail", wsPath, []string{filepath.Join(t.TempDir(), "missing")}, save)
	if err == nil {
		t.Fatal("clone workspace succeeded, want git clone error")
	}
	ws := cfg.Workspaces["clone-fail"]
	if ws.State != config.WorkspaceStateError {
		t.Fatalf("state = %q, want error", ws.State)
	}
	if ws.ErrorMessage == "" {
		t.Fatal("workspace error message was not recorded")
	}
	if _, statErr := os.Stat(wsPath); !os.IsNotExist(statErr) {
		t.Fatalf("workspace path still exists after clone failure, stat err=%v", statErr)
	}
}

func TestBeginWorkspaceCreateSaveFailureRollsBackConfig(t *testing.T) {
	cfg := &config.LoomConfig{Workspaces: make(map[string]config.WorkspaceConfig)}
	saveErr := errors.New("save failed")

	id, err := beginWorkspaceCreate(cfg, "bad-ws", "/tmp/bad-ws", []string{"https://example.invalid/repo.git"}, func(*config.LoomConfig) error {
		return saveErr
	})
	if id != "" {
		t.Fatalf("id = %q, want empty", id)
	}
	if !errors.Is(err, saveErr) {
		t.Fatalf("err = %v, want saveErr", err)
	}
	if _, ok := cfg.Workspaces["bad-ws"]; ok {
		t.Fatalf("workspace was not rolled back: %#v", cfg.Workspaces["bad-ws"])
	}
	var createErr *workspaceerrors.CreateError
	if !errors.As(err, &createErr) || createErr.Code != workspaceerrors.ConfigFailed {
		t.Fatalf("err = %v, want ConfigFailed", err)
	}
}

func TestLegacyDefaultWorkspaceMutations(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"alpha": {ID: "ALPHA", Path: filepath.Join(loomDir, "workspaces", "alpha")},
	}}
	if err := config.SaveConfig(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if err := SetDefaultWorkspace("alpha"); err != nil {
		t.Fatalf("set default: %v", err)
	}
	loaded, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if loaded.DefaultWorkspace != "alpha" {
		t.Fatalf("default workspace = %q, want alpha", loaded.DefaultWorkspace)
	}
	resolved, err := ResolveWorkspaceID("alpha")
	if err != nil {
		t.Fatalf("resolve workspace id: %v", err)
	}
	if resolved != "ALPHA" {
		t.Fatalf("resolved id = %q, want ALPHA", resolved)
	}
}

func TestResolveWorkspaceIDErrors(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{}}
	if err := config.SaveConfig(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if _, err := ResolveWorkspaceID("unknown"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("ResolveWorkspaceID err = %v, want not found", err)
	}
}

func TestWorkspaceHelperBranches(t *testing.T) {
	cfg := &config.LoomConfig{
		DefaultWorkspace: "alpha",
		WorkspaceOrder:   []string{"alpha", "beta", "gamma"},
		Workspaces: map[string]config.WorkspaceConfig{
			"alpha": {ID: "ALPHA"},
			"beta":  {ID: "BETA"},
			"gamma": {ID: "GAMMA"},
		},
	}

	if !isValidStoreKey("ALPHA-1") || isValidStoreKey("bad_name") {
		t.Fatal("store key validation mismatch")
	}
	if !containsString(cfg.WorkspaceOrder, "beta") || containsString(cfg.WorkspaceOrder, "delta") {
		t.Fatal("containsString mismatch")
	}

	removeWorkspaceFromConfig(cfg, "alpha")
	if cfg.DefaultWorkspace != "beta" {
		t.Fatalf("default workspace = %q, want beta", cfg.DefaultWorkspace)
	}
	if _, ok := cfg.Workspaces["alpha"]; ok {
		t.Fatal("alpha workspace was not removed")
	}
	if len(cfg.WorkspaceOrder) != 2 || cfg.WorkspaceOrder[0] != "beta" || cfg.WorkspaceOrder[1] != "gamma" {
		t.Fatalf("workspace order = %#v, want beta,gamma", cfg.WorkspaceOrder)
	}

	var saveCount int
	save := func(*config.LoomConfig) error {
		saveCount++
		return nil
	}
	transitionState(cfg, "beta", config.WorkspaceStateReady, save)
	if cfg.Workspaces["beta"].State != config.WorkspaceStateReady {
		t.Fatalf("beta state = %q, want ready", cfg.Workspaces["beta"].State)
	}
	makeErrorMarker(cfg, "beta", save)("boom")
	if cfg.Workspaces["beta"].State != config.WorkspaceStateError || cfg.Workspaces["beta"].ErrorMessage != "boom" {
		t.Fatalf("beta error state = %#v", cfg.Workspaces["beta"])
	}
	if saveCount < 2 {
		t.Fatalf("saveCount = %d, want at least 2", saveCount)
	}
}

func TestLoadOrCreateConfigAndDispatchErrors(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	cfg, err := loadOrCreateConfig()
	if err != nil {
		t.Fatalf("load or create config: %v", err)
	}
	if cfg.Workspaces == nil {
		t.Fatal("workspaces map was not initialized")
	}

	_, repos, agents, err := dispatchWorkspaceCreate(context.Background(), cfg, service.WorkspaceCreateRequest{
		Name: "bad",
		Type: "unknown",
	}, filepath.Join(loomDir, "workspaces", "bad"), "bad")
	if err == nil || !strings.Contains(err.Error(), "unsupported workspace type") {
		t.Fatalf("dispatch err = %v, want unsupported type", err)
	}
	if repos != nil || agents != nil {
		t.Fatalf("repos=%#v agents=%#v, want nil", repos, agents)
	}
}

func TestDeleteWorkspaceRejectsRunningAgentsThenRemovesConfig(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)
	alphaPath := filepath.Join(loomDir, "workspaces", "alpha")
	repoPath := filepath.Join(alphaPath, "repo")
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	lockPath := filepath.Join(repoPath, cli.LockFileName)
	if err := os.WriteFile(lockPath, []byte("locked"), 0644); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	cfg := &config.LoomConfig{
		DefaultWorkspace: "alpha",
		WorkspaceOrder:   []string{"alpha", "beta"},
		Workspaces: map[string]config.WorkspaceConfig{
			"alpha": {
				ID:    "ALPHA",
				Path:  alphaPath,
				Repos: []config.RepoConfig{{Name: "repo", Path: "repo"}},
			},
			"beta": {ID: "BETA", Path: filepath.Join(loomDir, "workspaces", "beta")},
		},
	}
	if err := config.SaveConfig(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if err := DeleteWorkspace("alpha"); err == nil || !strings.Contains(err.Error(), "running agents") {
		t.Fatalf("DeleteWorkspace with lock err = %v, want running agents", err)
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("remove lock: %v", err)
	}
	if err := DeleteWorkspace("alpha"); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}
	loaded, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if _, ok := loaded.Workspaces["alpha"]; ok {
		t.Fatal("alpha workspace still present")
	}
	if loaded.DefaultWorkspace != "beta" {
		t.Fatalf("default workspace = %q, want beta", loaded.DefaultWorkspace)
	}
	if len(loaded.WorkspaceOrder) != 1 || loaded.WorkspaceOrder[0] != "beta" {
		t.Fatalf("workspace order = %#v, want beta", loaded.WorkspaceOrder)
	}
	if err := DeleteWorkspace("missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("DeleteWorkspace missing err = %v, want not found", err)
	}
}

type repoFailStore struct {
	*memstore.Store
	err error
}

func (s *repoFailStore) Repos() store.RepoStore {
	return repoFailer{err: s.err}
}

type repoFailer struct {
	err error
}

func hasRole(roles []*domain.Role, name string) bool {
	for _, role := range roles {
		if role.Name == name {
			return true
		}
	}
	return false
}

func (r repoFailer) Create(context.Context, store.RepoCreate) (*domain.Repo, error) {
	return nil, r.err
}

func (r repoFailer) Get(context.Context, string, string) (*domain.Repo, error) {
	return nil, domain.ErrNotFound
}

func (r repoFailer) List(context.Context, string) ([]*domain.Repo, error) {
	return nil, nil
}

func (r repoFailer) Update(context.Context, string, string, store.RepoUpdate) (*domain.Repo, error) {
	return nil, r.err
}

func (r repoFailer) Delete(context.Context, string, string) error {
	return nil
}

func initTestGitRepo(t *testing.T, parent, name string) string {
	t.Helper()
	path := filepath.Join(parent, name)
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGit(t, path, "init")
	runGit(t, path, "config", "user.email", "test@example.com")
	runGit(t, path, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("test\n"), 0644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGit(t, path, "add", "README.md")
	runGit(t, path, "commit", "-m", "init")
	return path
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // Test helper creates real git repos for workspace lifecycle coverage.
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // Test helper creates real git repos for workspace lifecycle coverage.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}
