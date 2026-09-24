package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/testutil"
)

// errFleetRateLimited mimics the FleetDB HTTP 429 surfaced by the remote store.
var errFleetRateLimited = errors.New("fleet-db: HTTP 429 Too Many Requests")

// rateLimitedStore wraps a real store and fails Repos().List / Daemon().Get
// for selected workspace keys. It records every Repos().List workspace key.
type rateLimitedStore struct {
	store.Store
	fail map[string]error

	mu        sync.Mutex
	repoCalls []string
}

func (s *rateLimitedStore) Repos() store.RepoStore {
	return &rateLimitedRepoStore{RepoStore: s.Store.Repos(), parent: s}
}

func (s *rateLimitedStore) Daemon() store.DaemonProfileStore {
	return &rateLimitedDaemonStore{DaemonProfileStore: s.Store.Daemon(), fail: s.fail}
}

func (s *rateLimitedStore) repoListCalls(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, k := range s.repoCalls {
		if k == key {
			n++
		}
	}
	return n
}

type rateLimitedRepoStore struct {
	store.RepoStore
	parent *rateLimitedStore
}

func (r *rateLimitedRepoStore) List(ctx context.Context, workspaceKey string) ([]*domain.Repo, error) {
	r.parent.mu.Lock()
	r.parent.repoCalls = append(r.parent.repoCalls, workspaceKey)
	r.parent.mu.Unlock()
	if err := r.parent.fail[workspaceKey]; err != nil {
		return nil, fmt.Errorf("list repos %s: %w", workspaceKey, err)
	}
	return r.RepoStore.List(ctx, workspaceKey)
}

type rateLimitedDaemonStore struct {
	store.DaemonProfileStore
	fail map[string]error
}

func (d *rateLimitedDaemonStore) Get(ctx context.Context, workspaceKey string) (*domain.DaemonProfile, error) {
	if err := d.fail[workspaceKey]; err != nil {
		return nil, fmt.Errorf("get daemon %s: %w", workspaceKey, err)
	}
	return d.DaemonProfileStore.Get(ctx, workspaceKey)
}

type scopedStartupFixture struct {
	activeRoot string
	otherRoot  string
	repoPath   string
	store      *rateLimitedStore
}

// setupScopedStartup seeds FleetDB (memstore) with ACTIVE (repo1, a real git
// repo under a temp workspace root) and OTHER (whose repo/daemon loads fail
// with a 429-like error), registers both local roots in the state cache,
// sets LOOM_WORKSPACE=ACTIVE, and routes every config loader at the store.
func setupScopedStartup(t *testing.T, fail map[string]error) *scopedStartupFixture {
	t.Helper()
	testutil.ClearLoomEnv(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_FLEET_DB_ACTOR", "test")
	config.InvalidateConfigCache()
	cli.ResetWorkspaceRuntimeDirCache()
	oldResolver := cli.TestingResetDefaultResolver()
	t.Cleanup(func() {
		cli.TestingSetDefaultResolver(oldResolver)
		cli.ResetWorkspaceRuntimeDirCache()
		config.InvalidateConfigCache()
	})

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	f := &scopedStartupFixture{
		activeRoot: filepath.Join(root, "active"),
		otherRoot:  filepath.Join(root, "other"),
	}
	f.repoPath = filepath.Join(f.activeRoot, "repo1")
	createGitRepo(t, f.repoPath)
	if err := os.MkdirAll(f.otherRoot, 0o755); err != nil {
		t.Fatalf("mkdir other root: %v", err)
	}

	ctx := context.Background()
	base := memstore.New()
	t.Cleanup(func() { _ = base.Close() })
	for _, ws := range []struct{ key, repo string }{{"ACTIVE", "repo1"}, {"OTHER", "other-repo"}} {
		if _, err := base.Workspaces().Create(ctx, store.WorkspaceCreate{Key: ws.key, Name: strings.ToLower(ws.key)}); err != nil {
			t.Fatalf("create workspace %s: %v", ws.key, err)
		}
		if _, err := base.Repos().Create(ctx, store.RepoCreate{
			WorkspaceKey:  ws.key,
			Name:          ws.repo,
			DefaultBranch: "main",
			SourceRepoID:  ws.repo,
		}); err != nil {
			t.Fatalf("create repo %s/%s: %v", ws.key, ws.repo, err)
		}
	}
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.Workspaces["ACTIVE"] = bootstrap.WorkspaceLocalState{
			Path:  f.activeRoot,
			Repos: map[string]string{"repo1": f.repoPath},
		}
		sc.Workspaces["OTHER"] = bootstrap.WorkspaceLocalState{
			Path:  f.otherRoot,
			Repos: map[string]string{"other-repo": filepath.Join(f.otherRoot, "other-repo")},
		}
		return nil
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	f.store = &rateLimitedStore{Store: base, fail: fail}
	restore := config.TestingSetConfigStoreOpener(func(context.Context) (store.Store, func(), error) {
		return f.store, func() {}, nil
	})
	t.Cleanup(restore)
	t.Setenv(bootstrap.EnvWorkspace, "ACTIVE")
	return f
}

// Regression: agent/daemon startup (Supervisor.NewAgent -> ResolveAgentTarget)
// must succeed when an unrelated workspace's FleetDB load is rate limited.
func TestResolveAgentTarget_UnrelatedWorkspaceRateLimited(t *testing.T) {
	f := setupScopedStartup(t, map[string]error{"OTHER": errFleetRateLimited})

	// Precondition: the old list-all resolver fails in this exact setup.
	if _, err := cli.NewResolver(); err == nil {
		t.Fatal("cli.NewResolver() succeeded; want list-all load to fail on OTHER's 429")
	} else if !strings.Contains(err.Error(), "429") {
		t.Fatalf("cli.NewResolver() error = %v, want OTHER's 429", err)
	}
	config.InvalidateConfigCache()

	target, err := ResolveAgentTarget("agent1", "repo1")
	if err != nil {
		t.Fatalf("ResolveAgentTarget() error = %v", err)
	}
	wantDir := filepath.Join(f.activeRoot, "worktrees", "repo1", "agent1")
	if target.WorkDir != wantDir || target.AgentName != "agent1" || target.Repo != "repo1" {
		t.Fatalf("target = %+v, want WorkDir %s agent1/repo1", target, wantDir)
	}
	if _, err := os.Stat(filepath.Join(wantDir, ".git")); err != nil {
		t.Fatalf("worktree not created at %s: %v", wantDir, err)
	}
}

func TestResolveAgentTarget_RepoNameWithoutRepoArg(t *testing.T) {
	f := setupScopedStartup(t, map[string]error{"OTHER": errFleetRateLimited})

	target, err := ResolveAgentTarget("repo1", "")
	if err != nil {
		t.Fatalf("ResolveAgentTarget() error = %v", err)
	}
	if target.WorkDir != f.repoPath || target.AgentName != "repo1" {
		t.Fatalf("target = %+v, want repo path %s", target, f.repoPath)
	}
}

func TestResolveAgentTarget_ActiveWorkspaceFailurePropagates(t *testing.T) {
	setupScopedStartup(t, map[string]error{"ACTIVE": errFleetRateLimited})

	_, err := ResolveAgentTarget("agent1", "repo1")
	if err == nil {
		t.Fatal("ResolveAgentTarget() succeeded; want active workspace load error")
	}
	if !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "ACTIVE") {
		t.Fatalf("ResolveAgentTarget() error = %v, want ACTIVE 429", err)
	}
}

func TestResolveAgentTarget_OtherWorkspaceNameUsesStateCache(t *testing.T) {
	f := setupScopedStartup(t, map[string]error{"OTHER": errFleetRateLimited})

	target, err := ResolveAgentTarget("OTHER", "")
	if err != nil {
		t.Fatalf("ResolveAgentTarget() error = %v", err)
	}
	if target.WorkDir != f.otherRoot || target.AgentName != "OTHER" {
		t.Fatalf("target = %+v, want OTHER root %s", target, f.otherRoot)
	}
	if n := f.store.repoListCalls("OTHER"); n != 0 {
		t.Fatalf("OTHER Repos().List calls = %d, want 0", n)
	}
}
