package workspacemgr

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
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
	if len(roles) != 3 || !hasRole(roles, "plan") || !hasRole(roles, "task") || !hasRole(roles, "lead") {
		t.Fatalf("roles = %#v, want plan, task, and lead", roles)
	}
	roleByName := rolesByName(roles)
	if roleByName["plan"].TaskFilter != "needs_plan" {
		t.Fatalf("plan task filter = %q, want needs_plan", roleByName["plan"].TaskFilter)
	}
	if roleByName["task"].TaskFilter != "has_design" {
		t.Fatalf("task task filter = %q, want has_design", roleByName["task"].TaskFilter)
	}
	if roleByName["lead"].Kind != domain.RoleKindInteractive {
		t.Fatalf("lead kind = %q, want interactive", roleByName["lead"].Kind)
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

func TestStoreBackedCreateEmptyWorkspaceAllowsExternalEmptyPath(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	externalPath := filepath.Join(t.TempDir(), "picked-workspace")
	if err := os.MkdirAll(externalPath, 0755); err != nil {
		t.Fatalf("mkdir external path: %v", err)
	}

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)

	result, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "external-ws",
		Type: "empty",
		Path: externalPath,
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if result.WorkspaceID != "EXTERNAL-WS" || result.WorkspacePath != externalPath {
		t.Fatalf("result = %#v, want EXTERNAL-WS at %s", result, externalPath)
	}

	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if sc.Workspaces["EXTERNAL-WS"].Path != externalPath {
		t.Fatalf("local path = %q, want %q", sc.Workspaces["EXTERNAL-WS"].Path, externalPath)
	}
}

func TestStoreBackedCreateWorkspaceRejectsExternalNonEmptyPath(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	externalPath := filepath.Join(t.TempDir(), "documents")
	if err := os.MkdirAll(externalPath, 0755); err != nil {
		t.Fatalf("mkdir external path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(externalPath, "keep.txt"), []byte("do not remove\n"), 0644); err != nil {
		t.Fatalf("write external file: %v", err)
	}

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)

	_, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "external-ws",
		Type: "empty",
		Path: externalPath,
	})
	if err == nil {
		t.Fatal("create workspace succeeded, want non-empty path validation error")
	}
	if _, statErr := os.Stat(filepath.Join(externalPath, "keep.txt")); statErr != nil {
		t.Fatalf("non-empty external path was modified, stat err=%v", statErr)
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

func TestStoreBackedAddReposClonesRemoteRepoToEmptyWorkspace(t *testing.T) {
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

	src := initTestGitRepo(t, t.TempDir(), "Hello-World")
	addFn := BuildStoreBackedAddRepos(st)
	result, err := addFn(context.Background(), service.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		CloneURLs:   []string{src},
	})
	if err != nil {
		t.Fatalf("add clone repo: %v", err)
	}
	if result.WorkspaceID != "MY-WS" || result.WorkspacePath != wsPath {
		t.Fatalf("result = %#v, want MY-WS at %s", result, wsPath)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "hello-world", ".git")); err != nil {
		t.Fatalf("clone checkout not created: %v", err)
	}

	repos, err := st.Repos().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "hello-world" || repos[0].RemoteURL != src || repos[0].SourceRepoID != "hello-world" {
		t.Fatalf("repos = %#v, want cloned hello-world repo", repos)
	}

	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	local := sc.Workspaces["MY-WS"]
	if local.Repos["hello-world"] != filepath.Join(wsPath, "hello-world") {
		t.Fatalf("local repo path = %q", local.Repos["hello-world"])
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

func TestStoreBackedCreateEmptyWorkspaceClassifiesCreateRace(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := &workspaceCreateRaceStore{Store: memstore.New()}
	createFn := BuildStoreBackedCreateWorkspace(st)

	_, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: filepath.Join(loomDir, "workspaces", "my-ws"),
	})
	var createErr *workspaceerrors.CreateError
	if !errors.As(err, &createErr) {
		t.Fatalf("error = %v, want workspace create error", err)
	}
	if createErr.Code != workspaceerrors.AlreadyExists {
		t.Fatalf("error code = %s, want AlreadyExists", createErr.Code)
	}
}

func TestStoreBackedCreateEmptyWorkspaceRollsBackLocalStateOnReadyUpdateError(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := &workspaceReadyUpdateFailStore{Store: memstore.New()}
	createFn := BuildStoreBackedCreateWorkspace(st)

	_, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "rollback-ws",
		Type: "empty",
		Path: filepath.Join(loomDir, "workspaces", "rollback-ws"),
	})
	if err == nil {
		t.Fatal("create workspace succeeded, want ready update error")
	}
	if _, getErr := st.Store.Workspaces().Get(context.Background(), "ROLLBACK-WS"); !errors.Is(getErr, domain.ErrNotFound) {
		t.Fatalf("store workspace get err = %v, want ErrNotFound", getErr)
	}
	sc, loadErr := bootstrap.LoadStateCache()
	if loadErr != nil {
		t.Fatalf("load state cache: %v", loadErr)
	}
	if _, ok := sc.Workspaces["ROLLBACK-WS"]; ok {
		t.Fatalf("local state still contains ROLLBACK-WS: %#v", sc.Workspaces["ROLLBACK-WS"])
	}
	if sc.LastWorkspace == "ROLLBACK-WS" {
		t.Fatalf("LastWorkspace = %q, want rollback to clear active workspace", sc.LastWorkspace)
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
	if len(roles) != 3 || !hasRole(roles, "plan") || !hasRole(roles, "task") || !hasRole(roles, "lead") {
		t.Fatalf("roles = %#v, want plan, task, and lead", roles)
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

func TestStoreBackedCreateCloneWorkspaceClassifiesCreateRace(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := &workspaceCreateRaceStore{Store: memstore.New()}
	createFn := BuildStoreBackedCreateWorkspace(st)
	src := initTestGitRepo(t, t.TempDir(), "app")

	_, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name:      "clone-ws",
		Type:      "clone",
		CloneURLs: []string{src},
		Path:      filepath.Join(loomDir, "workspaces", "clone-ws"),
	})
	var createErr *workspaceerrors.CreateError
	if !errors.As(err, &createErr) {
		t.Fatalf("error = %v, want workspace create error", err)
	}
	if createErr.Code != workspaceerrors.AlreadyExists {
		t.Fatalf("error code = %s, want AlreadyExists", createErr.Code)
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

func TestStoreBackedCreateCloneWorkspaceKeepsPreexistingExternalRootOnFailure(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	externalPath := filepath.Join(t.TempDir(), "picked-workspace")
	if err := os.MkdirAll(externalPath, 0755); err != nil {
		t.Fatalf("mkdir external path: %v", err)
	}

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)

	_, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name:      "clone-ws",
		Type:      "clone",
		CloneURLs: []string{filepath.Join(t.TempDir(), "missing")},
		Path:      externalPath,
	})
	if err == nil {
		t.Fatal("clone workspace succeeded, want git clone error")
	}
	if info, statErr := os.Stat(externalPath); statErr != nil || !info.IsDir() {
		t.Fatalf("pre-existing external workspace root was removed, info=%v err=%v", info, statErr)
	}
}

type repoFailStore struct {
	*memstore.Store
	err error
}

func (s *repoFailStore) Repos() store.RepoStore {
	return repoFailer{err: s.err}
}

type workspaceCreateRaceStore struct {
	*memstore.Store
}

func (s *workspaceCreateRaceStore) Workspaces() store.WorkspaceStore {
	return workspaceCreateRaceWorkspaceStore{WorkspaceStore: s.Store.Workspaces()}
}

type workspaceCreateRaceWorkspaceStore struct {
	store.WorkspaceStore
}

func (s workspaceCreateRaceWorkspaceStore) Create(context.Context, store.WorkspaceCreate) (*domain.Workspace, error) {
	return nil, domain.ErrAlreadyExists
}

type workspaceReadyUpdateFailStore struct {
	*memstore.Store
}

func (s *workspaceReadyUpdateFailStore) Workspaces() store.WorkspaceStore {
	return workspaceReadyUpdateFailWorkspaceStore{WorkspaceStore: s.Store.Workspaces()}
}

type workspaceReadyUpdateFailWorkspaceStore struct {
	store.WorkspaceStore
}

func (s workspaceReadyUpdateFailWorkspaceStore) Update(ctx context.Context, key string, patch store.WorkspaceUpdate) (*domain.Workspace, error) {
	if patch.State != nil && *patch.State == domain.WorkspaceStateReady {
		return nil, errors.New("ready update failed")
	}
	return s.WorkspaceStore.Update(ctx, key, patch)
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

func rolesByName(roles []*domain.Role) map[string]*domain.Role {
	out := make(map[string]*domain.Role, len(roles))
	for _, role := range roles {
		out[role.Name] = role
	}
	return out
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
