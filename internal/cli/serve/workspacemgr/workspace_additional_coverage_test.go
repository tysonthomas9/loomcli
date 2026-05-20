package workspacemgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestWorkspacePathAndRepoValidationAdditionalBranches(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := expandUserPath("~/picked"); got != filepath.Join(home, "picked") {
		t.Fatalf("expandUserPath = %q, want home-expanded path", got)
	}
	if got := expandUserPath("relative"); got != "relative" {
		t.Fatalf("expandUserPath relative = %q", got)
	}

	filePath := filepath.Join(t.TempDir(), "workspace-file")
	if err := os.WriteFile(filePath, []byte("not a dir"), 0600); err != nil {
		t.Fatalf("write file workspace path: %v", err)
	}
	if err := validateWorkspaceCreatePath(filePath); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file workspace err = %v, want not a directory", err)
	}

	gitWorkspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(gitWorkspace, ".git"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := validateWorkspaceCreatePath(gitWorkspace); err == nil || !strings.Contains(err.Error(), "existing git repository") {
		t.Fatalf("git workspace err = %v, want existing git repository", err)
	}

	parentFile := filepath.Join(t.TempDir(), "parent-file")
	if err := os.WriteFile(parentFile, []byte("parent"), 0600); err != nil {
		t.Fatalf("write parent file: %v", err)
	}
	if err := validateWorkspaceCreatePath(filepath.Join(parentFile, "child")); err == nil || !strings.Contains(err.Error(), "cannot inspect workspace path") {
		t.Fatalf("parent file err = %v, want cannot inspect workspace path", err)
	}
	if err := validateWorkspaceCreatePath(filepath.Join(t.TempDir(), "missing", "child")); err == nil || !strings.Contains(err.Error(), "parent directory does not exist") {
		t.Fatalf("missing parent err = %v, want parent directory does not exist", err)
	}

	if err := validateWorkspaceCreatePath(filepath.Join(defaultWorkspaceBase(), "new-ws")); err != nil {
		t.Fatalf("workspace base child should be allowed: %v", err)
	}
	if err := validateWorkspacePath(defaultWorkspaceBase()); err == nil || !strings.Contains(err.Error(), "workspace-specific folder") {
		t.Fatalf("default base err = %v, want workspace-specific folder", err)
	}

	if _, err := resolveRepoPaths([]string{"", "   "}); err == nil || !strings.Contains(err.Error(), "no valid repos") {
		t.Fatalf("empty repo paths err = %v, want no valid repos", err)
	}
	if _, err := resolveRepoPaths([]string{filePath}); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("repo file err = %v, want not a directory", err)
	}
	notRepo := t.TempDir()
	if _, err := resolveRepoPaths([]string{notRepo}); err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("not git repo err = %v, want not a git repository", err)
	}
	repoParent1 := t.TempDir()
	repoParent2 := t.TempDir()
	repo1 := initTestGitRepo(t, repoParent1, "app")
	repo2 := initTestGitRepo(t, repoParent2, "app")
	if _, err := resolveRepoPaths([]string{repo1, repo2}); err == nil || !strings.Contains(err.Error(), "duplicate repo name") {
		t.Fatalf("duplicate repo names err = %v, want duplicate repo name", err)
	}
}

func TestCloneRepoHelpersDeduplicateCancelAndCleanup(t *testing.T) {
	wsDir := t.TempDir()
	src := initTestGitRepo(t, t.TempDir(), "Fancy Repo")

	seen := map[string]bool{"fancy-repo": true}
	repos, err := cloneReposWithSeen(context.Background(), []string{src}, wsDir, seen)
	if err != nil {
		t.Fatalf("cloneReposWithSeen: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "fancy-repo-2" {
		t.Fatalf("repos = %#v, want deduplicated fancy-repo-2", repos)
	}
	if !seen["fancy-repo-2"] {
		t.Fatalf("seen did not include cloned repo: %#v", seen)
	}
	if _, err := os.Stat(filepath.Join(wsDir, "fancy-repo-2", ".git")); err != nil {
		t.Fatalf("cloned repo missing: %v", err)
	}
	cleanupClonedRepos(repos)
	if _, err := os.Stat(filepath.Join(wsDir, "fancy-repo-2")); !os.IsNotExist(err) {
		t.Fatalf("cleanupClonedRepos left clone behind: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cloneRepos(ctx, []string{src}, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cloneRepos canceled err = %v, want context.Canceled", err)
	}

	createdRoot := filepath.Join(t.TempDir(), "created-root")
	if err := os.MkdirAll(filepath.Join(createdRoot, "repo"), 0755); err != nil {
		t.Fatalf("mkdir created root: %v", err)
	}
	cleanupCloneWorkspace(workspaceDirPlan{path: createdRoot, removeRootOnRollback: true}, repos)
	if _, err := os.Stat(createdRoot); !os.IsNotExist(err) {
		t.Fatalf("cleanupCloneWorkspace left root behind: %v", err)
	}
}

func TestCreateStoreBackedCloneWorkspaceAdditionalErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("no clone urls", func(t *testing.T) {
		_, err := BuildStoreBackedCreateWorkspace(memstore.New())(ctx, service.WorkspaceCreateRequest{
			Name: "clone-ws",
			Type: "clone",
			Path: filepath.Join(t.TempDir(), "clone-ws"),
		})
		if err == nil || !strings.Contains(err.Error(), "no clone URLs") {
			t.Fatalf("err = %v, want no clone URLs", err)
		}
	})

	t.Run("existing name", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		st := memstore.New()
		if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "EXISTING", Name: "Existing"}); err != nil {
			t.Fatalf("create existing workspace: %v", err)
		}
		_, err := BuildStoreBackedCreateWorkspace(st)(ctx, service.WorkspaceCreateRequest{
			Name:      "Existing",
			Type:      "clone",
			CloneURLs: []string{initTestGitRepo(t, t.TempDir(), "app")},
			Path:      filepath.Join(t.TempDir(), "clone-ws"),
		})
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("err = %v, want already exists", err)
		}
	})

	t.Run("key lookup error", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		errBoom := errors.New("lookup failed")
		st := &workspaceLookupErrorStore{Store: memstore.New(), getErr: errBoom}
		_, err := BuildStoreBackedCreateWorkspace(st)(ctx, service.WorkspaceCreateRequest{
			Name:      "lookup-ws",
			Type:      "clone",
			CloneURLs: []string{initTestGitRepo(t, t.TempDir(), "app")},
			Path:      filepath.Join(t.TempDir(), "clone-ws"),
		})
		if err == nil || !strings.Contains(err.Error(), "check workspace key") || !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want check workspace key wrapping boom", err)
		}
	})

	for _, state := range []domain.WorkspaceState{
		domain.WorkspaceStateCloning,
		domain.WorkspaceStateInitializing,
		domain.WorkspaceStateReady,
	} {
		t.Run("update "+string(state), func(t *testing.T) {
			loomDir := t.TempDir()
			t.Setenv("LOOM_CONFIG_DIR", loomDir)
			src := initTestGitRepo(t, t.TempDir(), "app")
			st := &workspaceStateUpdateFailStore{Store: memstore.New(), failState: state, err: errors.New("state update failed")}
			wsPath := filepath.Join(loomDir, "workspaces", "clone-ws")
			_, err := BuildStoreBackedCreateWorkspace(st)(ctx, service.WorkspaceCreateRequest{
				Name:      "clone-ws",
				Type:      "clone",
				CloneURLs: []string{src},
				Path:      wsPath,
			})
			if err == nil || !strings.Contains(err.Error(), "state update failed") {
				t.Fatalf("err = %v, want state update failed", err)
			}
			if _, getErr := st.Store.Workspaces().Get(ctx, "CLONE-WS"); !errors.Is(getErr, domain.ErrNotFound) {
				t.Fatalf("workspace was not rolled back, get err=%v", getErr)
			}
		})
	}
}

func TestCreateStoreBackedEmptyWorkspaceAdditionalErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("existing name", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		st := memstore.New()
		if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "EXISTING", Name: "Existing"}); err != nil {
			t.Fatalf("create existing: %v", err)
		}
		_, err := BuildStoreBackedCreateWorkspace(st)(ctx, service.WorkspaceCreateRequest{
			Name: "Existing",
			Type: "empty",
			Path: filepath.Join(t.TempDir(), "ws"),
		})
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("err = %v, want already exists", err)
		}
	})

	t.Run("existing key", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		st := memstore.New()
		if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "KEYEDWS", Name: "Other Name"}); err != nil {
			t.Fatalf("create keyed: %v", err)
		}
		_, err := BuildStoreBackedCreateWorkspace(st)(ctx, service.WorkspaceCreateRequest{
			Name: "keyed ws",
			Type: "empty",
			Path: filepath.Join(t.TempDir(), "ws"),
		})
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("err = %v, want already exists", err)
		}
	})

	t.Run("workspace mkdir error", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		parentFile := filepath.Join(t.TempDir(), "parent-file")
		if err := os.WriteFile(parentFile, []byte("x"), 0600); err != nil {
			t.Fatalf("write parent file: %v", err)
		}
		_, err := BuildStoreBackedCreateWorkspace(memstore.New())(ctx, service.WorkspaceCreateRequest{
			Name: "mkdir-ws",
			Type: "empty",
			Path: filepath.Join(parentFile, "child"),
		})
		if err == nil {
			t.Fatal("expected workspace path error, got nil")
		}
	})

	t.Run("store create error", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		errBoom := errors.New("create failed")
		st := &workspaceCreateErrorStore{Store: memstore.New(), err: errBoom}
		_, err := BuildStoreBackedCreateWorkspace(st)(ctx, service.WorkspaceCreateRequest{
			Name: "create-error",
			Type: "empty",
			Path: filepath.Join(t.TempDir(), "ws"),
		})
		if err == nil || !strings.Contains(err.Error(), "create workspace in store") || !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want create failed", err)
		}
	})

	t.Run("save local state error rolls back", func(t *testing.T) {
		loomFile := filepath.Join(t.TempDir(), "loom-file")
		if err := os.WriteFile(loomFile, []byte("x"), 0600); err != nil {
			t.Fatalf("write loom file: %v", err)
		}
		t.Setenv("LOOM_CONFIG_DIR", loomFile)
		st := memstore.New()
		_, err := BuildStoreBackedCreateWorkspace(st)(ctx, service.WorkspaceCreateRequest{
			Name: "state-error",
			Type: "empty",
			Path: filepath.Join(t.TempDir(), "ws"),
		})
		if err == nil || !strings.Contains(err.Error(), "save local workspace state") {
			t.Fatalf("err = %v, want save local state error", err)
		}
		if _, getErr := st.Workspaces().Get(ctx, "STATE-ERROR"); !errors.Is(getErr, domain.ErrNotFound) {
			t.Fatalf("workspace was not rolled back, get err=%v", getErr)
		}
	})
}

type workspaceCreateErrorStore struct {
	*memstore.Store
	err error
}

func (s *workspaceCreateErrorStore) Workspaces() store.WorkspaceStore {
	return workspaceCreateErrorWorkspaceStore{WorkspaceStore: s.Store.Workspaces(), err: s.err}
}

type workspaceCreateErrorWorkspaceStore struct {
	store.WorkspaceStore
	err error
}

func (s workspaceCreateErrorWorkspaceStore) Create(context.Context, store.WorkspaceCreate) (*domain.Workspace, error) {
	return nil, s.err
}

type workspaceStateUpdateFailStore struct {
	*memstore.Store
	failState domain.WorkspaceState
	err       error
}

func (s *workspaceStateUpdateFailStore) Workspaces() store.WorkspaceStore {
	return workspaceStateUpdateFailWorkspaceStore{WorkspaceStore: s.Store.Workspaces(), failState: s.failState, err: s.err}
}

type workspaceStateUpdateFailWorkspaceStore struct {
	store.WorkspaceStore
	failState domain.WorkspaceState
	err       error
}

func (s workspaceStateUpdateFailWorkspaceStore) Update(ctx context.Context, key string, patch store.WorkspaceUpdate) (*domain.Workspace, error) {
	if patch.State != nil && *patch.State == s.failState {
		return nil, s.err
	}
	return s.WorkspaceStore.Update(ctx, key, patch)
}
