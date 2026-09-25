package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/testutil"
)

// errNewAgentRateLimited mimics the FleetDB HTTP 429 surfaced by the remote store.
var errNewAgentRateLimited = errors.New("fleet-db: HTTP 429 Too Many Requests")

// newAgentFaultStore fails Repos().List and Daemon().Get for selected
// workspace keys, emulating a rate-limited FleetDB for those workspaces.
type newAgentFaultStore struct {
	store.Store
	fail map[string]error
}

func (s *newAgentFaultStore) Repos() store.RepoStore {
	return &newAgentFaultRepoStore{RepoStore: s.Store.Repos(), fail: s.fail}
}

func (s *newAgentFaultStore) Daemon() store.DaemonProfileStore {
	return &newAgentFaultDaemonStore{DaemonProfileStore: s.Store.Daemon(), fail: s.fail}
}

type newAgentFaultRepoStore struct {
	store.RepoStore
	fail map[string]error
}

func (r *newAgentFaultRepoStore) List(ctx context.Context, workspaceKey string) ([]*domain.Repo, error) {
	if err := r.fail[workspaceKey]; err != nil {
		return nil, fmt.Errorf("list repos %s: %w", workspaceKey, err)
	}
	return r.RepoStore.List(ctx, workspaceKey)
}

type newAgentFaultDaemonStore struct {
	store.DaemonProfileStore
	fail map[string]error
}

func (d *newAgentFaultDaemonStore) Get(ctx context.Context, workspaceKey string) (*domain.DaemonProfile, error) {
	if err := d.fail[workspaceKey]; err != nil {
		return nil, fmt.Errorf("get daemon %s: %w", workspaceKey, err)
	}
	return d.DaemonProfileStore.Get(ctx, workspaceKey)
}

func initNewAgentGitRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "test"},
		{"git", "commit", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command(args[0], args[1:]...) //nolint:gosec //nolint:norawexec
		cmd.Dir = path
		cmd.Env = clitest.GitSafeEnv(
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
}

// setupNewAgentScopedWorkspaces seeds ACTIVE (repo1, a real git repo) and
// OTHER in a memstore routed through config.TestingSetConfigStoreOpener, with
// fail injecting per-workspace FleetDB errors. It returns ACTIVE's root.
func setupNewAgentScopedWorkspaces(t *testing.T, fail map[string]error) string {
	t.Helper()
	testutil.ClearLoomEnv(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_FLEET_DB_ACTOR", "test")
	cfgpkg.InvalidateConfigCache()
	cli.ResetWorkspaceRuntimeDirCache()
	oldResolver := cli.TestingResetDefaultResolver()
	t.Cleanup(func() {
		cli.TestingSetDefaultResolver(oldResolver)
		cli.ResetWorkspaceRuntimeDirCache()
		cfgpkg.InvalidateConfigCache()
	})

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	activeRoot := filepath.Join(root, "active")
	otherRoot := filepath.Join(root, "other")
	repoPath := filepath.Join(activeRoot, "repo1")
	initNewAgentGitRepo(t, repoPath)
	if err := os.MkdirAll(otherRoot, 0o755); err != nil {
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
			Path:  activeRoot,
			Repos: map[string]string{"repo1": repoPath},
		}
		sc.Workspaces["OTHER"] = bootstrap.WorkspaceLocalState{Path: otherRoot}
		return nil
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	st := &newAgentFaultStore{Store: base, fail: fail}
	t.Cleanup(cfgpkg.TestingSetConfigStoreOpener(func(context.Context) (store.Store, func(), error) {
		return st, func() {}, nil
	}))
	t.Setenv(bootstrap.EnvWorkspace, "ACTIVE")
	return activeRoot
}

func newAgentTestSupervisor(t *testing.T) *Supervisor {
	t.Helper()
	return &Supervisor{
		ProjectDir:     t.TempDir(),
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{} },
		FindRepoConfig: func(string) *cfgpkg.RepoConfig { return nil },
	}
}

// Regression: daemon startup must not fail because an unrelated workspace's
// FleetDB load returns HTTP 429.
func TestNewAgent_UnrelatedWorkspaceRateLimited(t *testing.T) {
	activeRoot := setupNewAgentScopedWorkspaces(t, map[string]error{"OTHER": errNewAgentRateLimited})

	// Precondition: the list-all resolver fails in this exact setup.
	if _, err := cli.NewResolver(); err == nil {
		t.Fatal("cli.NewResolver() succeeded; want list-all load to fail on OTHER's 429")
	}
	cfgpkg.InvalidateConfigCache()

	s := newAgentTestSupervisor(t)
	ap, err := s.NewAgent(cfgpkg.AgentEntry{Worktree: "agent1", Repo: "repo1", Role: "task"}, 0)
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}
	want := filepath.Join(activeRoot, "worktrees", "repo1", "agent1")
	if ap.WorktreePath != want {
		t.Fatalf("WorktreePath = %q, want %q", ap.WorktreePath, want)
	}
	if _, err := os.Stat(filepath.Join(want, ".git")); err != nil {
		t.Fatalf("worktree not created at %s: %v", want, err)
	}
	if ap.RoleConfig.TaskFilter != "has_design" {
		t.Fatalf("RoleConfig = %+v, want built-in task role", ap.RoleConfig)
	}
}

func TestNewAgent_ActiveWorkspaceFailureReturnsWorktreeError(t *testing.T) {
	setupNewAgentScopedWorkspaces(t, map[string]error{"ACTIVE": errNewAgentRateLimited})

	s := newAgentTestSupervisor(t)
	_, err := s.NewAgent(cfgpkg.AgentEntry{Worktree: "agent1", Repo: "repo1", Role: "task"}, 3)
	if err == nil {
		t.Fatal("NewAgent() succeeded; want active workspace load error")
	}
	msg := err.Error()
	if !strings.Contains(msg, `agent[3] worktree "agent1"`) || !strings.Contains(msg, "429") {
		t.Fatalf("NewAgent() error = %v, want agent[3] worktree \"agent1\" with 429", err)
	}
}
