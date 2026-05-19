package workspacemgr

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestStoreBackedBuilderNilAndHelperBranches(t *testing.T) {
	if BuildStoreBackedCreateWorkspace(nil) != nil {
		t.Fatal("BuildStoreBackedCreateWorkspace(nil) returned non-nil")
	}
	if BuildStoreBackedAddRepos(nil) != nil {
		t.Fatal("BuildStoreBackedAddRepos(nil) returned non-nil")
	}

	if _, _, err := resolveWorkspaceForAddRepos(context.Background(), memstore.New(), " "); err == nil {
		t.Fatal("resolveWorkspaceForAddRepos empty workspace returned nil error")
	}
	if got := pickAddReposBranch("request", &domain.Workspace{Name: "Workspace", DefaultBranch: "main"}, "WS"); got != "request" {
		t.Fatalf("request branch = %q", got)
	}
	if got := pickAddReposBranch("", &domain.Workspace{Name: "Workspace", DefaultBranch: "main"}, "WS"); got != "main" {
		t.Fatalf("default branch = %q", got)
	}
	if got := pickAddReposBranch("", &domain.Workspace{Name: "Workspace"}, "WS"); got != "Workspace" {
		t.Fatalf("workspace name branch = %q", got)
	}
	if got := pickAddReposBranch("", &domain.Workspace{}, "WS"); got != "WS" {
		t.Fatalf("workspace key branch = %q", got)
	}

	if created, repos, err := materializeAddReposWorktrees(context.Background(), nil, t.TempDir(), "main"); err != nil || created != nil || repos != nil {
		t.Fatalf("materializeAddReposWorktrees empty = created=%v repos=%v err=%v", created, repos, err)
	}
	if repos, err := materializeAddReposClones(context.Background(), nil, t.TempDir(), map[string]bool{}, nil); err != nil || repos != nil {
		t.Fatalf("materializeAddReposClones empty = repos=%v err=%v", repos, err)
	}
	if got := gitRemoteURL(t.TempDir(), ""); got != "" {
		t.Fatalf("gitRemoteURL non-repo = %q, want empty", got)
	}
}

func TestResolveWorkspaceForAddReposByNameAndDedup(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Friendly", DefaultBranch: "develop"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "WS1", Name: "api"}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	key, ws, err := resolveWorkspaceForAddRepos(ctx, st, "Friendly")
	if err != nil {
		t.Fatalf("resolve by name: %v", err)
	}
	if key != "WS1" || ws.Name != "Friendly" {
		t.Fatalf("resolve by name = %q/%+v", key, ws)
	}

	_, err = dedupAddReposAgainstExisting(ctx, st, "WS1", []resolvedRepo{{name: "api"}})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("dedup collision error = %v", err)
	}
	seen, err := dedupAddReposAgainstExisting(ctx, st, "WS1", []resolvedRepo{{name: "web"}})
	if err != nil {
		t.Fatalf("dedup new repo: %v", err)
	}
	if !seen["api"] || !seen["web"] {
		t.Fatalf("seen = %#v, want existing and new repos", seen)
	}
}

func TestPersistAddReposRecordsAndLocalWorkspacePath(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)
	ctx := context.Background()
	st := memstore.New()
	wsDir := t.TempDir()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Friendly"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	if _, err := localWorkspacePath("WS1"); err == nil {
		t.Fatal("localWorkspacePath without state returned nil error")
	}
	if err := persistAddReposRecords(ctx, st, "WS1", wsDir, "main", []config.RepoConfig{
		{Name: "api", Path: wsDir},
	}, nil, nil); err != nil {
		t.Fatalf("persistAddReposRecords: %v", err)
	}
	path, err := localWorkspacePath("WS1")
	if err != nil {
		t.Fatalf("localWorkspacePath after save: %v", err)
	}
	if path != wsDir {
		t.Fatalf("localWorkspacePath = %q, want %q", path, wsDir)
	}
	repos, err := st.Repos().List(ctx, "WS1")
	if err != nil || len(repos) != 1 || repos[0].Name != "api" {
		t.Fatalf("repos = %+v err=%v", repos, err)
	}

	deleteLocalWorkspaceState("WS1")
	if sc, err := bootstrap.LoadStateCache(); err != nil {
		t.Fatalf("load state: %v", err)
	} else if _, ok := sc.Workspaces["WS1"]; ok {
		t.Fatalf("workspace state was not deleted: %+v", sc.Workspaces["WS1"])
	}
}

func TestCreateStoreRepoErrorWrapsRepoName(t *testing.T) {
	errBoom := errors.New("boom")
	st := &repoCreateErrorStore{Store: memstore.New(), err: errBoom}
	err := createStoreRepo(context.Background(), st, "WS", "main", config.RepoConfig{Name: "api"})
	if err == nil || !strings.Contains(err.Error(), `create repo "api"`) || !errors.Is(err, errBoom) {
		t.Fatalf("createStoreRepo error = %v", err)
	}

	if _, err := BuildStoreBackedCreateWorkspace(memstore.New())(context.Background(), service.WorkspaceCreateRequest{Type: "unsupported"}); err == nil {
		t.Fatal("unsupported workspace type returned nil error")
	}
}

func TestStoreBackedCreateWorkspaceStoreLookupAndRoleErrors(t *testing.T) {
	ctx := context.Background()
	errBoom := errors.New("boom")

	t.Run("empty name lookup error", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		st := &workspaceLookupErrorStore{Store: memstore.New(), byNameErr: errBoom}
		_, err := BuildStoreBackedCreateWorkspace(st)(ctx, service.WorkspaceCreateRequest{
			Name: "lookup-ws",
			Type: "empty",
			Path: t.TempDir(),
		})
		if err == nil || !strings.Contains(err.Error(), "check workspace name") || !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want check workspace name wrapping boom", err)
		}
	})

	t.Run("empty key lookup error", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		st := &workspaceLookupErrorStore{Store: memstore.New(), getErr: errBoom}
		_, err := BuildStoreBackedCreateWorkspace(st)(ctx, service.WorkspaceCreateRequest{
			Name: "key-ws",
			Type: "empty",
			Path: t.TempDir(),
		})
		if err == nil || !strings.Contains(err.Error(), "check workspace key") || !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want check workspace key wrapping boom", err)
		}
	})

	t.Run("empty role seed error rolls back", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		base := memstore.New()
		st := &roleCreateErrorStore{Store: base, err: errBoom}
		_, err := BuildStoreBackedCreateWorkspace(st)(ctx, service.WorkspaceCreateRequest{
			Name: "role-ws",
			Type: "empty",
			Path: t.TempDir(),
		})
		if err == nil || !strings.Contains(err.Error(), "seed built-in roles") || !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want seed built-in roles wrapping boom", err)
		}
		if _, getErr := base.Workspaces().Get(ctx, "ROLE-WS"); !errors.Is(getErr, domain.ErrNotFound) {
			t.Fatalf("workspace was not rolled back, get err=%v", getErr)
		}
	})

	t.Run("clone role seed error rolls back", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		src := initTestGitRepo(t, t.TempDir(), "app")
		base := memstore.New()
		st := &roleCreateErrorStore{Store: base, err: errBoom}
		_, err := BuildStoreBackedCreateWorkspace(st)(ctx, service.WorkspaceCreateRequest{
			Name:      "clone-role-ws",
			Type:      "clone",
			CloneURLs: []string{src},
			Path:      filepath.Join(t.TempDir(), "clone-role-ws"),
		})
		if err == nil || !strings.Contains(err.Error(), "seed built-in roles") || !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want seed built-in roles wrapping boom", err)
		}
		if _, getErr := base.Workspaces().Get(ctx, "CLONE-ROLE-WS"); !errors.Is(getErr, domain.ErrNotFound) {
			t.Fatalf("workspace was not rolled back, get err=%v", getErr)
		}
	})
}

func TestStoreBackedAddReposStoreErrorBranches(t *testing.T) {
	ctx := context.Background()
	errBoom := errors.New("boom")

	t.Run("workspace name fallback failure reports original get error", func(t *testing.T) {
		st := &workspaceLookupErrorStore{
			Store:     memstore.New(),
			getErr:    errBoom,
			byNameErr: errors.New("name lookup failed"),
		}
		_, _, err := resolveWorkspaceForAddRepos(ctx, st, "missing")
		if err == nil || !strings.Contains(err.Error(), `load workspace "missing"`) || !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want load workspace wrapping original get error", err)
		}
	})

	t.Run("list repos error", func(t *testing.T) {
		st := &repoListErrorStore{Store: memstore.New(), err: errBoom}
		_, err := dedupAddReposAgainstExisting(ctx, st, "WS", []resolvedRepo{{name: "api"}})
		if err == nil || !strings.Contains(err.Error(), "list workspace repos") || !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want list workspace repos wrapping boom", err)
		}
	})

	t.Run("persist repo create rolls back previous repos", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		st := &repoCreateAfterFirstErrorStore{Store: memstore.New(), err: errBoom}
		if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
			t.Fatalf("create workspace: %v", err)
		}
		err := persistAddReposRecords(ctx, st, "WS", t.TempDir(), "main", []config.RepoConfig{
			{Name: "api"},
			{Name: "web"},
		}, nil, nil)
		if err == nil || !strings.Contains(err.Error(), `create repo "web"`) || !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want create repo web wrapping boom", err)
		}
		repos, listErr := st.Store.Repos().List(ctx, "WS")
		if listErr != nil {
			t.Fatalf("list repos: %v", listErr)
		}
		if len(repos) != 0 {
			t.Fatalf("rollback left repos: %+v", repos)
		}
	})

	if err := saveLocalWorkspaceState("", t.TempDir(), nil, false); err == nil || !strings.Contains(err.Error(), "key must not be empty") {
		t.Fatalf("saveLocalWorkspaceState empty-key err = %v", err)
	}
	deleteLocalWorkspaceState("")
}

type repoCreateErrorStore struct {
	store.Store
	err error
}

func (s *repoCreateErrorStore) Repos() store.RepoStore {
	return repoFailer{err: s.err}
}

type workspaceLookupErrorStore struct {
	*memstore.Store
	getErr    error
	byNameErr error
}

func (s *workspaceLookupErrorStore) Workspaces() store.WorkspaceStore {
	return workspaceLookupErrorWorkspaceStore{
		WorkspaceStore: s.Store.Workspaces(),
		getErr:         s.getErr,
		byNameErr:      s.byNameErr,
	}
}

type workspaceLookupErrorWorkspaceStore struct {
	store.WorkspaceStore
	getErr    error
	byNameErr error
}

func (s workspaceLookupErrorWorkspaceStore) Get(ctx context.Context, key string) (*domain.Workspace, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.WorkspaceStore.Get(ctx, key)
}

func (s workspaceLookupErrorWorkspaceStore) GetByName(ctx context.Context, name string) (*domain.Workspace, error) {
	if s.byNameErr != nil {
		return nil, s.byNameErr
	}
	return s.WorkspaceStore.GetByName(ctx, name)
}

type roleCreateErrorStore struct {
	*memstore.Store
	err error
}

func (s *roleCreateErrorStore) Roles() store.RoleStore {
	return roleCreateErrorRoleStore{RoleStore: s.Store.Roles(), err: s.err}
}

type roleCreateErrorRoleStore struct {
	store.RoleStore
	err error
}

func (s roleCreateErrorRoleStore) Create(context.Context, store.RoleCreate) (*domain.Role, error) {
	return nil, s.err
}

type repoListErrorStore struct {
	*memstore.Store
	err error
}

func (s *repoListErrorStore) Repos() store.RepoStore {
	return repoListErrorRepoStore{RepoStore: s.Store.Repos(), err: s.err}
}

type repoListErrorRepoStore struct {
	store.RepoStore
	err error
}

func (s repoListErrorRepoStore) List(context.Context, string) ([]*domain.Repo, error) {
	return nil, s.err
}

type repoCreateAfterFirstErrorStore struct {
	*memstore.Store
	err   error
	calls int
}

func (s *repoCreateAfterFirstErrorStore) Repos() store.RepoStore {
	return &repoCreateAfterFirstErrorRepoStore{RepoStore: s.Store.Repos(), parent: s}
}

type repoCreateAfterFirstErrorRepoStore struct {
	store.RepoStore
	parent *repoCreateAfterFirstErrorStore
}

func (s *repoCreateAfterFirstErrorRepoStore) Create(ctx context.Context, in store.RepoCreate) (*domain.Repo, error) {
	s.parent.calls++
	if s.parent.calls > 1 {
		return nil, s.parent.err
	}
	return s.RepoStore.Create(ctx, in)
}
