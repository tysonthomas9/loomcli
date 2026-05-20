package opsimpl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestSelectAgentRepoAdditionalBranches(t *testing.T) {
	agent := ops.WorkspaceAgentInfo{Name: "nova"}
	if _, err := selectAgentRepo(nil, agent); err == nil || !strings.Contains(err.Error(), "no repos") {
		t.Fatalf("empty repos err = %v", err)
	}

	repos := []ops.WorkspaceRepo{
		{Name: "api", Remote: "origin", DefaultBranch: "main", Groups: []string{"backend"}},
		{Name: "ui", Remote: "upstream", Groups: []string{"frontend"}},
	}
	got, err := selectAgentRepo(repos, agent)
	if err != nil || got.Name != "api" {
		t.Fatalf("default repo = %+v err=%v, want api", got, err)
	}

	got, err = selectAgentRepo(repos, ops.WorkspaceAgentInfo{Name: "spark", Repos: []string{"ui"}})
	if err != nil || got.Name != "ui" {
		t.Fatalf("repo affinity = %+v err=%v, want ui", got, err)
	}

	got, err = selectAgentRepo(repos, ops.WorkspaceAgentInfo{Name: "ember", RepoGroups: []string{"backend"}})
	if err != nil || got.Name != "api" {
		t.Fatalf("group affinity = %+v err=%v, want api", got, err)
	}

	if _, err := selectAgentRepo(repos, ops.WorkspaceAgentInfo{Name: "bad", Repos: []string{"missing"}}); err == nil ||
		!strings.Contains(err.Error(), "does not match") {
		t.Fatalf("missing affinity err = %v", err)
	}
}

func TestScopeResolverAndConversionAdditionalBranches(t *testing.T) {
	cfg := &cfgpkg.LoomConfig{Workspaces: map[string]cfgpkg.WorkspaceConfig{"dev": {ID: "WS1", Path: "/tmp/dev"}}}
	if err := scopeResolverToWorkspace(&cli.Resolver{Mode: cli.ResolverMode(99), Config: cfg}, "WS1"); err == nil ||
		!strings.Contains(err.Error(), "not workspace-scoped") {
		t.Fatalf("single repo scope err = %v", err)
	}

	plain := toAgentWorktree(cli.WorktreeInfo{Name: "nova", Path: "/tmp/nova", Branch: "feature"})
	if plain.DefaultBranch != "main" || plain.IsWorkspace {
		t.Fatalf("plain worktree conversion = %+v", plain)
	}
	withRepo := toAgentWorktree(cli.WorktreeInfo{
		Name:   "spark",
		Path:   "/tmp/spark",
		Branch: "feature/spark",
		Repo:   &cfgpkg.RepoConfig{Name: "api", Remote: "upstream", DefaultBranch: "trunk"},
	})
	if withRepo.DefaultBranch != "trunk" || withRepo.Remote != "upstream" || withRepo.RepoName != "api" || !withRepo.IsWorkspace {
		t.Fatalf("repo worktree conversion = %+v", withRepo)
	}

	opsImpl := NewGitOps()
	if opsImpl == nil || opsImpl.WithStore(nil) != opsImpl {
		t.Fatalf("NewGitOps/WithStore returned unexpected value")
	}
}

func TestGitOpsConfigBackedResolveAndList(t *testing.T) {
	ctx := context.Background()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv(bootstrap.EnvWorkspace, "WS")
	t.Cleanup(cfgpkg.InvalidateConfigCache)

	root := t.TempDir()
	repoPath := filepath.Join(root, "api")
	agentPath := filepath.Join(root, "worktrees", "api", "nova")
	for _, dir := range []string{
		filepath.Join(repoPath, ".git"),
		filepath.Join(agentPath, ".git"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  "WS",
		Name:          "api",
		Remote:        "upstream",
		DefaultBranch: "trunk",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{Workspaces: map[string]bootstrap.WorkspaceLocalState{
		"WS": {Path: root, Repos: map[string]string{"api": repoPath}},
	}}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}
	if _, err := cfgpkg.TestingPrimeConfigCacheFromStore(ctx, st); err != nil {
		t.Fatalf("prime config cache: %v", err)
	}

	got, err := NewGitOps().ResolveAgentWorktree("WS", "nova")
	if err != nil {
		t.Fatalf("ResolveAgentWorktree: %v", err)
	}
	if got.Path != agentPath || got.DefaultBranch != "trunk" || got.Remote != "upstream" || !got.IsWorkspace {
		t.Fatalf("resolved worktree = %+v", got)
	}
	list, err := NewGitOps().ListAgentWorktrees("WS")
	if err != nil {
		t.Fatalf("ListAgentWorktrees: %v", err)
	}
	if len(list) != 1 || list[0].Name != "nova" || list[0].Path != agentPath {
		t.Fatalf("worktree list = %+v", list)
	}
	if _, err := NewGitOps().ResolveAgentWorktree("MISSING", "nova"); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing workspace err = %v", err)
	}
}
