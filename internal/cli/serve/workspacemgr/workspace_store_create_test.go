package workspacemgr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/loomgit/mirror"
	loomworkspace "github.com/tysonthomas9/loomcli/internal/loomgit/workspace"
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
		Branch: "main",
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
		Branch:      "main",
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
	if len(repos) != 1 || repos[0].Name != "api" || repos[0].DefaultBranch != "main" {
		t.Fatalf("repos = %#v, want api on main", repos)
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
	attached, err := loomworkspace.Records(context.Background(), "MY-WS")
	if err != nil || len(attached) != 1 {
		t.Fatalf("attached records=%v err=%v", attached, err)
	}
	wantBranch := "loom/ws/MY-WS/interactive/lead"
	if attached[0].Repo != "api" || attached[0].Trunk != "main" || attached[0].WorkspaceBranch != wantBranch || attached[0].BaseSHA != strings.TrimSpace(gitOutput(t, src, "rev-parse", "main")) {
		t.Fatalf("attached record=%+v", attached[0])
	}
	if got := strings.TrimSpace(gitOutput(t, filepath.Join(wsPath, "api"), "branch", "--show-current")); got != wantBranch {
		t.Fatalf("attached branch=%q", got)
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
	attached, err := loomworkspace.Records(context.Background(), "MY-WS")
	if err != nil || len(attached) != 1 || attached[0].Repo != "hello-world" || attached[0].Trunk != "main" || attached[0].WorkspaceBranch != "loom/ws/MY-WS/interactive/lead" {
		t.Fatalf("attached clone record=%v err=%v", attached, err)
	}
	if got := strings.TrimSpace(gitOutput(t, filepath.Join(wsPath, "hello-world"), "branch", "--show-current")); got != attached[0].WorkspaceBranch {
		t.Fatalf("attached clone branch=%q", got)
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
	if got := strings.TrimSpace(gitOutput(t, filepath.Join(wsPath, "app"), "branch", "--show-current")); got != "loom/ws/CLONE-WS/interactive/lead" {
		t.Fatalf("clone branch=%q", got)
	}
	if records, err := loomworkspace.Records(context.Background(), "CLONE-WS"); err != nil || len(records) != 1 || records[0].Trunk != "main" || records[0].WorkspaceBranch != "loom/ws/CLONE-WS/interactive/lead" {
		t.Fatalf("clone records=%v err=%v", records, err)
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

func TestP120MixedCreateUsesOneJournalAndRecordsBothRepos(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "config"))
	api := initTestGitRepo(t, root, "api")
	provider := p120BareProvider(t, root, api)
	web := initTestGitRepo(t, root, "web")
	wsPath := filepath.Join(root, "workspace")
	st := memstore.New()
	_, err := BuildStoreBackedCreateWorkspace(st)(context.Background(), service.WorkspaceCreateRequest{
		Name: "mixed", Type: "empty", Repos: []string{web}, CloneURLs: []string{"file://" + provider}, Branch: "main", Path: wsPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	records, err := loomworkspace.Records(context.Background(), "MIXED")
	if err != nil || len(records) != 2 {
		t.Fatalf("records=%v err=%v", records, err)
	}
	for _, repo := range []string{"api", "web"} {
		path := filepath.Join(wsPath, repo)
		if got := strings.TrimSpace(gitOutput(t, path, "branch", "--show-current")); got != "loom/ws/MIXED/interactive/lead" {
			t.Fatalf("%s branch=%q", repo, got)
		}
	}
	if info, err := os.Stat(filepath.Join(wsPath, "api", ".git")); err != nil || !info.IsDir() {
		t.Fatalf("api is not a clone: %v", err)
	}
	if info, err := os.Stat(filepath.Join(wsPath, "web", ".git")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("web is not a worktree: %v", err)
	}
	p120AssertMixedJournal(t, filepath.Join(root, "config"))
}

func p120AssertMixedJournal(t *testing.T, configDir string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(configDir, "loomgit", "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var data []byte
	if err := db.QueryRow("SELECT plan FROM workspace_creations").Scan(&data); err != nil {
		t.Fatal(err)
	}
	var plan struct{ Repos []struct{ Name, Mode string } }
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	modes := make(map[string]string)
	for _, repo := range plan.Repos {
		modes[repo.Name] = repo.Mode
	}
	if len(plan.Repos) != 2 || modes["api"] != "clone" || modes["web"] != "worktree" {
		t.Fatalf("mixed creation journal modes=%v", modes)
	}
}

func TestP120MixedCreateRollsBackWhenLocalOrCloneCheckoutFails(t *testing.T) {
	for _, failure := range []string{"add-worktree", "adopt-clone"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "config"))
			api := initTestGitRepo(t, root, "api")
			provider := p120BareProvider(t, root, api)
			web := initTestGitRepo(t, root, "web")
			branch := "loom/ws/MIXED/interactive/lead"
			if failure == "add-worktree" {
				runGit(t, web, "branch", branch, "main")
			} else {
				runGit(t, provider, "branch", branch, "main")
				runGit(t, provider, "symbolic-ref", "HEAD", "refs/heads/"+branch)
			}
			wsPath := filepath.Join(root, "workspace")
			st := memstore.New()
			_, err := BuildStoreBackedCreateWorkspace(st)(context.Background(), service.WorkspaceCreateRequest{
				Name: "mixed", Type: "empty", Repos: []string{web}, CloneURLs: []string{"file://" + provider}, Branch: "main", Path: wsPath,
			})
			if err == nil {
				t.Fatal("mixed create succeeded despite checkout conflict")
			}
			p120AssertFailedMixedCreateRolledBack(t, st, wsPath, failure == "add-worktree")
		})
	}
}

func p120AssertFailedMixedCreateRolledBack(t *testing.T, st *memstore.Store, wsPath string, wantRootRemoved bool) {
	t.Helper()
	for _, repo := range []string{"api", "web"} {
		if _, err := os.Stat(filepath.Join(wsPath, repo)); !os.IsNotExist(err) {
			t.Fatalf("%s checkout remains after failed mixed create: %v", repo, err)
		}
	}
	if wantRootRemoved {
		if _, err := os.Stat(wsPath); !os.IsNotExist(err) {
			t.Fatalf("workspace root remains after failed mixed create: %v", err)
		}
	}
	if _, err := st.Workspaces().Get(context.Background(), "MIXED"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("workspace row remains after failed mixed create: %v", err)
	}
	open, err := loomworkspace.OpenCreations(context.Background())
	if err != nil || len(open) != 0 {
		t.Fatalf("open creation journals after rollback: %v, err=%v", open, err)
	}
}

func TestP120AddReposKeepsPreexistingDirtyCloneTarget(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "config"))
	wsPath := filepath.Join(root, "workspace")
	st := memstore.New()
	if _, err := BuildStoreBackedCreateWorkspace(st)(context.Background(), service.WorkspaceCreateRequest{Name: "mixed", Type: "empty", Path: wsPath}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(wsPath, "api")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	precious := filepath.Join(target, "precious.txt")
	if err := os.WriteFile(precious, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	api := initTestGitRepo(t, root, "api")
	provider := p120BareProvider(t, root, api)
	_, err := BuildStoreBackedAddRepos(st)(context.Background(), service.WorkspaceAddReposRequest{WorkspaceID: "MIXED", CloneURLs: []string{"file://" + provider}})
	if err == nil || !strings.Contains(err.Error(), "partial clone retained at "+target) {
		t.Fatalf("add-repos error=%v, want retained-path report", err)
	}
	if data, err := os.ReadFile(precious); err != nil || string(data) != "keep" {
		t.Fatalf("preexisting file changed or removed: %q, %v", data, err)
	}
}

func TestP120CleanupClonedReposKeepsUserWork(t *testing.T) {
	root := t.TempDir()
	api := initTestGitRepo(t, root, "api")
	provider := p120BareProvider(t, root, api)
	clone := filepath.Join(root, "clone")
	runGit(t, root, "clone", "file://"+provider, clone)
	precious := filepath.Join(clone, "precious.txt")
	if err := os.WriteFile(precious, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cleanupClonedRepos([]config.RepoConfig{{Path: clone}}); err == nil || !strings.Contains(err.Error(), clone) {
		t.Fatalf("cleanup error=%v, want retained clone path", err)
	}
	if data, err := os.ReadFile(precious); err != nil || string(data) != "keep" {
		t.Fatalf("user file changed or removed: %q, %v", data, err)
	}
}

func TestP120FailedCloneRetainsPartialFilesAndRecoveryRow(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "config"))
	api := initTestGitRepo(t, root, "api")
	provider := p120BareProvider(t, root, api)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	shimDir := t.TempDir()
	shim := `#!/bin/sh
if [ "$1" = clone ]; then
  for target; do :; done
  mkdir -p "$target"
  printf keep > "$target/precious.txt"
  exit 1
fi
exec "$P120_REAL_GIT" "$@"
`
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("P120_REAL_GIT", realGit)
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	wsPath := filepath.Join(root, "workspace")
	st := memstore.New()
	_, err = BuildStoreBackedCreateWorkspace(st)(context.Background(), service.WorkspaceCreateRequest{
		Name: "mixed", Type: "empty", CloneURLs: []string{"file://" + provider}, Path: wsPath,
	})
	if err == nil || !strings.Contains(err.Error(), "partial clone retained at "+filepath.Join(wsPath, "api")) {
		t.Fatalf("clone error=%v, want retained-path report", err)
	}
	if data, err := os.ReadFile(filepath.Join(wsPath, "api", "precious.txt")); err != nil || string(data) != "keep" {
		t.Fatalf("partial clone file changed or removed: %q, %v", data, err)
	}
	if _, err := st.Workspaces().Get(context.Background(), "MIXED"); err != nil {
		t.Fatalf("recovery workspace row missing: %v", err)
	}
}

func TestP120MixedCloneDeleteRequiresConfirmationAndCapturesWork(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "config"))
	api := initTestGitRepo(t, root, "api")
	provider := p120BareProvider(t, root, api)
	web := initTestGitRepo(t, root, "web")
	wsPath := filepath.Join(root, "workspace")
	_, err := BuildStoreBackedCreateWorkspace(memstore.New())(context.Background(), service.WorkspaceCreateRequest{
		Name: "mixed", Type: "empty", Repos: []string{web}, CloneURLs: []string{"file://" + provider}, Branch: "main", Path: wsPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(wsPath, "api")
	if err := os.WriteFile(filepath.Join(clone, "user.txt"), []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, clone, "config", "user.name", "Tester")
	runGit(t, clone, "config", "user.email", "tester@example.test")
	runGit(t, clone, "checkout", "-b", "user-branch")
	if err := os.WriteFile(filepath.Join(clone, "branch.txt"), []byte("branch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, clone, "add", "branch.txt")
	runGit(t, clone, "commit", "-m", "user branch")
	ws := config.WorkspaceConfig{ID: "MIXED", Path: wsPath, Repos: []config.RepoConfig{{Name: "api", Path: clone}}}
	preview, err := loomworkspace.DryRun(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	var branch, file bool
	for _, item := range preview.Items {
		branch = branch || item.Kind == "clone_branch"
		file = file || (item.Kind == "file" && strings.Contains(item.Detail, "user.txt"))
	}
	if !branch || !file {
		t.Fatalf("missing branch or file in preview: %+v", preview.Items)
	}
	if err := loomworkspace.DeleteWorkspace(context.Background(), ws, "", func(context.Context, string) error { return nil }); err == nil {
		t.Fatal("delete without confirmation succeeded")
	}
	if _, err := os.Stat(clone); err != nil {
		t.Fatalf("clone removed before confirmation: %v", err)
	}
	if err := loomworkspace.DeleteWorkspace(context.Background(), ws, preview.Fingerprint, func(context.Context, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(clone); !os.IsNotExist(err) {
		t.Fatalf("clone retained after confirmed capture: %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.GetConfigDir(), "loomgit", "deletion-captures", "MIXED", "api.bundle")); err != nil {
		t.Fatalf("missing clone bundle: %v", err)
	}
}

func p120BareProvider(t *testing.T, root, source string) string {
	t.Helper()
	provider := filepath.Join(root, "api.git")
	runGit(t, root, "clone", "--bare", source, provider)
	return provider
}

func TestP120MixedCloneTaskRefsMirrorToProvider(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "config"))
	api := initTestGitRepo(t, root, "api")
	provider := p120BareProvider(t, root, api)
	web := initTestGitRepo(t, root, "web")
	webProvider := filepath.Join(root, "web.git")
	runGit(t, root, "clone", "--bare", web, webProvider)
	runGit(t, web, "remote", "add", "origin", webProvider)
	wsPath := filepath.Join(root, "workspace")
	_, err := BuildStoreBackedCreateWorkspace(memstore.New())(context.Background(), service.WorkspaceCreateRequest{
		Name: "mixed", Type: "empty", Repos: []string{web}, CloneURLs: []string{"file://" + provider}, Branch: "main", Path: wsPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	clone, task := filepath.Join(wsPath, "api"), filepath.Join(root, "task")
	runGit(t, clone, "worktree", "add", "-b", "task", task)
	base := strings.TrimSpace(gitOutput(t, task, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(task, "task.txt"), []byte("task work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	captured, err := agentcapture.Capture(context.Background(), task, "MIXED", "ATTEMPT", "TASK", "task")
	if err != nil || captured.Ref == "" {
		t.Fatalf("capture=%+v err=%v", captured, err)
	}
	runGit(t, task, "add", "task.txt")
	patch := gitOutput(t, task, "diff", "--cached", "--binary") + "\n"
	revision, err := driverfreeze.Freeze(context.Background(), driverfreeze.Request{
		Workspace: "MIXED", Task: "TASK", Repo: "api", Attempt: "ATTEMPT", Worktree: task,
		Base: base, Patch: []byte(patch), Outcome: "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(gitOutput(t, clone, "rev-parse", captured.Ref)); got != captured.SHA {
		t.Fatalf("clone capture=%s", got)
	}
	if err := mirror.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(gitOutput(t, provider, "rev-parse", captured.Ref)); got != captured.SHA {
		t.Fatalf("provider capture=%s", got)
	}
	if refs := gitOutput(t, provider, "for-each-ref", "--format=%(objectname)", "refs/loom/ws/MIXED/change"); !strings.Contains(refs, revision.HeadSHA) {
		t.Fatalf("provider revision absent: %s", refs)
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
	runGit(t, path, "init", "-b", "main")
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
