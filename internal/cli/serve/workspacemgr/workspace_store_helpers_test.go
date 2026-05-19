package workspacemgr

import (
	"context"
	"errors"
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

type repoCreateErrorStore struct {
	store.Store
	err error
}

func (s *repoCreateErrorStore) Repos() store.RepoStore {
	return repoFailer{err: s.err}
}
