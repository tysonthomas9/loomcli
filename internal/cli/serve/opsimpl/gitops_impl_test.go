package opsimpl

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// --- resolveWorkspaceConfigName tests ---

func TestResolveWorkspaceConfigName_DirectNameMatch(t *testing.T) {
	cfg := &config.LoomConfig{
		Workspaces: map[string]config.WorkspaceConfig{
			"dev": {ID: "uuid-dev", Path: "/tmp/dev"},
		},
	}

	got := resolveWorkspaceConfigName(cfg, "dev")
	if got != "dev" {
		t.Errorf("resolveWorkspaceConfigName(cfg, 'dev') = %q, want %q", got, "dev")
	}
}

func TestResolveWorkspaceConfigName_UUIDMatch(t *testing.T) {
	cfg := &config.LoomConfig{
		Workspaces: map[string]config.WorkspaceConfig{
			"staging": {ID: "uuid-staging-123", Path: "/tmp/staging"},
		},
	}

	got := resolveWorkspaceConfigName(cfg, "uuid-staging-123")
	if got != "staging" {
		t.Errorf("resolveWorkspaceConfigName(cfg, 'uuid-staging-123') = %q, want %q", got, "staging")
	}
}

func TestResolveWorkspaceConfigName_EmptyString(t *testing.T) {
	cfg := &config.LoomConfig{
		Workspaces: map[string]config.WorkspaceConfig{
			"dev": {ID: "uuid-dev", Path: "/tmp/dev"},
		},
	}

	got := resolveWorkspaceConfigName(cfg, "")
	if got != "" {
		t.Errorf("resolveWorkspaceConfigName(cfg, '') = %q, want empty", got)
	}
}

func TestResolveWorkspaceConfigName_UnknownID(t *testing.T) {
	cfg := &config.LoomConfig{
		Workspaces: map[string]config.WorkspaceConfig{
			"dev": {ID: "uuid-dev", Path: "/tmp/dev"},
		},
	}

	got := resolveWorkspaceConfigName(cfg, "nonexistent-id")
	if got != "" {
		t.Errorf("resolveWorkspaceConfigName(cfg, 'nonexistent-id') = %q, want empty", got)
	}
}

func TestResolveWorkspaceConfigName_NilConfig(t *testing.T) {
	got := resolveWorkspaceConfigName(nil, "anything")
	if got != "" {
		t.Errorf("resolveWorkspaceConfigName(nil, 'anything') = %q, want empty", got)
	}
}

func TestResolveWorkspaceConfigName_NilWorkspacesMap(t *testing.T) {
	cfg := &config.LoomConfig{}

	got := resolveWorkspaceConfigName(cfg, "dev")
	if got != "" {
		t.Errorf("resolveWorkspaceConfigName(cfg, 'dev') = %q, want empty", got)
	}
}

// --- scopeResolverToWorkspace tests ---

func TestScopeResolverToWorkspace_EmptyWorkspaceID(t *testing.T) {
	cfg := &config.LoomConfig{
		Workspaces: map[string]config.WorkspaceConfig{
			"dev": {ID: "uuid-dev", Path: "/tmp/dev"},
		},
	}
	resolver := &cli.Resolver{
		Mode:      cli.ModeWorkspace,
		Config:    cfg,
		Workspace: "dev",
	}

	err := scopeResolverToWorkspace(resolver, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Workspace should remain unchanged (no-op).
	if resolver.WorkspaceName() != "dev" {
		t.Errorf("workspace = %q, want %q (should be unchanged)", resolver.WorkspaceName(), "dev")
	}
}

func TestScopeResolverToWorkspace_ValidWorkspaceName(t *testing.T) {
	cfg := &config.LoomConfig{
		Workspaces: map[string]config.WorkspaceConfig{
			"dev":     {ID: "uuid-dev", Path: "/tmp/dev"},
			"staging": {ID: "uuid-staging", Path: "/tmp/staging"},
		},
	}
	resolver := &cli.Resolver{
		Mode:      cli.ModeWorkspace,
		Config:    cfg,
		Workspace: "dev",
	}

	err := scopeResolverToWorkspace(resolver, "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolver.WorkspaceName() != "staging" {
		t.Errorf("workspace = %q, want %q", resolver.WorkspaceName(), "staging")
	}
}

func TestScopeResolverToWorkspace_ValidUUID(t *testing.T) {
	cfg := &config.LoomConfig{
		Workspaces: map[string]config.WorkspaceConfig{
			"dev":     {ID: "uuid-dev", Path: "/tmp/dev"},
			"staging": {ID: "uuid-staging-456", Path: "/tmp/staging"},
		},
	}
	resolver := &cli.Resolver{
		Mode:      cli.ModeWorkspace,
		Config:    cfg,
		Workspace: "dev",
	}

	err := scopeResolverToWorkspace(resolver, "uuid-staging-456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolver.WorkspaceName() != "staging" {
		t.Errorf("workspace = %q, want %q", resolver.WorkspaceName(), "staging")
	}
}

func TestScopeResolverToWorkspace_UnknownWorkspaceID(t *testing.T) {
	cfg := &config.LoomConfig{
		Workspaces: map[string]config.WorkspaceConfig{
			"dev": {ID: "uuid-dev", Path: "/tmp/dev"},
		},
	}
	resolver := &cli.Resolver{
		Mode:      cli.ModeWorkspace,
		Config:    cfg,
		Workspace: "dev",
	}

	err := scopeResolverToWorkspace(resolver, "nonexistent-ws")
	if err == nil {
		t.Fatal("expected error for unknown workspace ID, got nil")
	}
}

func TestResolveAgentWorktree_StoreBackedFleetDB(t *testing.T) {
	ctx := context.Background()
	loomDir := t.TempDir()
	wsRoot := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Workspace One"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  "WS1",
		Name:          "api",
		DefaultBranch: "main",
		Remote:        "origin",
		Groups:        []string{"backend"},
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS1", Name: "task"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS1",
		Name:         "nova",
		RoleName:     "task",
		RepoGroups:   []string{"backend"},
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	wtPath := filepath.Join(wsRoot, "worktrees", "api", "nova")
	if err := runGit(t, wtPath, "init", "-b", "feature/nova"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.LastWorkspace = "WS1"
		sc.Workspaces["WS1"] = bootstrap.WorkspaceLocalState{
			Path:  wsRoot,
			Repos: map[string]string{"api": filepath.Join(wsRoot, "api")},
		}
		return nil
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	got, err := NewGitOps().WithStore(st).ResolveAgentWorktree("WS1", "nova")
	if err != nil {
		t.Fatalf("ResolveAgentWorktree: %v", err)
	}
	if got.Path != wtPath {
		t.Fatalf("Path = %q, want %q", got.Path, wtPath)
	}
	if got.RepoName != "api" || got.DefaultBranch != "main" || got.Branch != "feature/nova" || !got.IsWorkspace {
		t.Fatalf("unexpected worktree: %+v", got)
	}
}

func runGit(t *testing.T, dir string, args ...string) error {
	t.Helper()
	if args[0] == "init" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	cmd := exec.Command("git", args...) //nolint:norawexec // test helper uses fixed git command with caller-supplied args
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("git %v output: %s", args, out)
	}
	return err
}
