package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestResolverWorkspaceNameResolutionAndDefaults(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha")
	nested := filepath.Join(alpha, "repo")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"ALPHA": {Path: alpha, Repos: []config.RepoConfig{{Name: "api", Path: "repo", DefaultBranch: "develop"}}},
		"Beta":  {Path: filepath.Join(root, "beta")},
	}}

	t.Setenv(bootstrap.EnvWorkspace, "beta")
	if got, err := resolveActiveWorkspaceName(cfg); err != nil || got != "Beta" {
		t.Fatalf("env workspace got=%q err=%v, want Beta", got, err)
	}
	t.Setenv(bootstrap.EnvWorkspace, "missing")
	if _, err := resolveActiveWorkspaceName(cfg); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing workspace err = %v", err)
	}
	t.Setenv(bootstrap.EnvWorkspace, "")

	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(nested); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd after chdir: %v", err)
	}
	ws := cfg.Workspaces["ALPHA"]
	ws.Path = filepath.Dir(cwd)
	cfg.Workspaces["ALPHA"] = ws
	if got, err := resolveActiveWorkspaceName(cfg); err != nil || got != "ALPHA" {
		t.Fatalf("cwd workspace got=%q err=%v, want ALPHA", got, err)
	}

	r := &Resolver{Mode: ModeWorkspace, Config: cfg, Workspace: "ALPHA"}
	if r.GetMode() != ModeWorkspace || r.WorkspaceName() != "ALPHA" {
		t.Fatalf("resolver mode/name = %v/%q", r.GetMode(), r.WorkspaceName())
	}
	if err := r.SetWorkspace("Beta"); err != nil || r.Workspace != "Beta" {
		t.Fatalf("SetWorkspace Beta err=%v workspace=%q", err, r.Workspace)
	}
	if err := r.SetWorkspace("alpha"); err != nil || r.Workspace != "ALPHA" {
		t.Fatalf("SetWorkspace alpha err=%v workspace=%q", err, r.Workspace)
	}
	if err := r.SetWorkspace("missing"); err == nil {
		t.Fatal("SetWorkspace missing succeeded")
	}
	if err := (&Resolver{}).SetWorkspace("ALPHA"); err == nil {
		t.Fatal("SetWorkspace without config succeeded")
	}

	r.Workspace = "ALPHA"
	if got := r.GetDefaultBranch(); got != "develop" {
		t.Fatalf("default branch = %q, want develop", got)
	}
	t.Setenv("LOOM_DEFAULT_BRANCH", "release")
	if got := r.GetDefaultBranch(); got != "release" {
		t.Fatalf("env default branch = %q, want release", got)
	}
}

func TestResolverWorkspaceAndAgentDiscoveryPaths(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dataDir)
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repoPath, ".git"), 0755); err != nil {
		t.Fatalf("mkdir repo git: %v", err)
	}
	nestedAgent := filepath.Join(root, "worktrees", "repo", "falcon")
	if err := os.MkdirAll(filepath.Join(nestedAgent, ".git"), 0755); err != nil {
		t.Fatalf("mkdir nested agent: %v", err)
	}
	stateAgent := filepath.Join(root, "state-agent")
	if err := os.MkdirAll(filepath.Join(stateAgent, ".git"), 0755); err != nil {
		t.Fatalf("mkdir state agent: %v", err)
	}
	linkedAgent := filepath.Join(root, "linked-agent")
	if err := os.MkdirAll(linkedAgent, 0755); err != nil {
		t.Fatalf("mkdir linked agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(linkedAgent, ".git"), []byte("gitdir: ../repo/.git/worktrees/linked-agent\n"), 0600); err != nil {
		t.Fatalf("write linked .git: %v", err)
	}

	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"WS": {
			Path: root,
			Repos: []config.RepoConfig{
				{Name: "repo", Path: "repo", DefaultBranch: "main"},
				{Name: "linked-agent", Path: linkedAgent, DefaultBranch: "main"},
			},
		},
	}}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{Workspaces: map[string]bootstrap.WorkspaceLocalState{
		"WS": {Agents: map[string]bootstrap.AgentLocalState{
			"state": {Worktree: stateAgent},
		}},
	}}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	r := &Resolver{Mode: ModeWorkspace, Config: cfg, Workspace: "WS"}
	repos, err := r.DiscoverWorktrees()
	if err != nil {
		t.Fatalf("DiscoverWorktrees: %v", err)
	}
	if len(repos) != 2 || repos[0].Name != "repo" || repos[0].Branch != "unknown" || repos[1].Name != "linked-agent" {
		t.Fatalf("repos = %+v", repos)
	}

	candidates := r.agentWorktreeCandidates(cfg.Workspaces["WS"])
	if got := candidateNames(candidates); !reflect.DeepEqual(got, []string{"state", "falcon", "linked-agent"}) {
		t.Fatalf("candidate names = %#v", got)
	}
	agents, err := r.DiscoverAgentWorktrees()
	if err != nil {
		t.Fatalf("DiscoverAgentWorktrees: %v", err)
	}
	if got := worktreeNames(agents); !reflect.DeepEqual(got, []string{"state", "falcon", "linked-agent"}) {
		t.Fatalf("agent names = %#v", got)
	}

	byName, err := r.ResolveAgentByName("falcon")
	if err != nil {
		t.Fatalf("ResolveAgentByName nested: %v", err)
	}
	if byName.Path != nestedAgent || byName.Repo == nil || byName.Repo.Name != "repo" {
		t.Fatalf("nested agent = %+v", byName)
	}
	linked, err := r.ResolveAgentByName("linked-agent")
	if err != nil {
		t.Fatalf("ResolveAgentByName linked: %v", err)
	}
	if !linked.IsLinkedWorktree || linked.Path != linkedAgent {
		t.Fatalf("linked agent = %+v", linked)
	}
	if _, err := r.ResolveAgentByName("missing"); err == nil {
		t.Fatal("ResolveAgentByName missing succeeded")
	}
	if _, err := (&Resolver{}).DiscoverAgentWorktrees(); err == nil {
		t.Fatal("DiscoverAgentWorktrees without workspace mode succeeded")
	}
}

func TestResolverWorkspacePathHelpers(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	orphan := filepath.Join(root, "worktrees", "orphan")
	for _, dir := range []string{repoPath, filepath.Join(orphan, ".git")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"WS": {Path: root, Repos: []config.RepoConfig{{Name: "repo", Path: "repo"}}},
	}}
	r := &Resolver{Mode: ModeWorkspace, Config: cfg, Workspace: "WS"}

	if p, ok := r.ResolveWorkspaceByName("WS"); !ok || p != root {
		t.Fatalf("ResolveWorkspaceByName got %q/%t", p, ok)
	}
	if names := r.WorkspaceNames(); !reflect.DeepEqual(names, []string{"WS"}) {
		t.Fatalf("WorkspaceNames = %#v", names)
	}
	if got, err := r.ResolveWorkspacePath("repo"); err != nil || got != repoPath {
		t.Fatalf("ResolveWorkspacePath repo got=%q err=%v", got, err)
	}
	if got, err := r.ResolveWorkspacePath(repoPath); err != nil || got != repoPath {
		t.Fatalf("ResolveWorkspacePath abs got=%q err=%v", got, err)
	}
	if _, err := r.ResolveWorkspacePath(orphan); err != nil {
		t.Fatalf("ResolveWorkspacePath existing abs: %v", err)
	}
	if _, err := r.ResolveWorkspacePath("orphan"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("orphan err = %v", err)
	}
	if _, err := (&Resolver{}).ResolveWorkspacePath("repo"); err == nil {
		t.Fatal("ResolveWorkspacePath without config succeeded")
	}
}

func TestResolverAdditionalBranchCoverage(t *testing.T) {
	if _, err := resolveActiveWorkspaceName(nil); err == nil {
		t.Fatal("resolveActiveWorkspaceName nil config succeeded")
	}
	if _, err := normalizeWorkspaceName(&config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{}}, "missing"); err == nil {
		t.Fatal("normalizeWorkspaceName missing succeeded")
	}

	root := t.TempDir()
	relRoot := "relative-ws"
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir root: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.MkdirAll(filepath.Join(root, relRoot, "repo"), 0755); err != nil {
		t.Fatalf("mkdir relative workspace: %v", err)
	}
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"REL":     {Path: relRoot, Repos: []config.RepoConfig{{Name: "repo", Path: "repo"}}},
		"MISSING": {Path: filepath.Join(root, "missing"), Repos: []config.RepoConfig{{Name: "gone", Path: "gone"}}},
	}}
	if err := os.Chdir(filepath.Join(root, relRoot, "repo")); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	relWS := cfg.Workspaces["REL"]
	relWS.Path = ".."
	cfg.Workspaces["REL"] = relWS
	if got := workspaceNameFromCWD(cfg); got != "REL" {
		t.Fatalf("workspaceNameFromCWD relative = %q, want REL", got)
	}
	relWS.Path = relRoot
	cfg.Workspaces["REL"] = relWS

	r := &Resolver{Mode: ModeWorkspace, Config: cfg, Workspace: "REL"}
	if got, err := r.DiscoverWorktrees(); err != nil || len(got) != 0 {
		t.Fatalf("DiscoverWorktrees without .git = %#v, %v", got, err)
	}
	if _, err := (&Resolver{Mode: ModeWorkspace, Config: cfg, Workspace: "UNKNOWN"}).DiscoverWorktrees(); err == nil {
		t.Fatal("DiscoverWorktrees unknown workspace succeeded")
	}
	if got, err := (*Resolver)(nil).DiscoverWorktrees(); err != nil || got != nil {
		t.Fatalf("nil DiscoverWorktrees = %#v, %v", got, err)
	}
	if _, err := (&Resolver{Mode: ModeWorkspace, Config: cfg, Workspace: "UNKNOWN"}).DiscoverAgentWorktrees(); err == nil {
		t.Fatal("DiscoverAgentWorktrees unknown workspace succeeded")
	}

	candidates := appendAgentCandidate(nil, map[string]struct{}{}, agentWorktreeCandidate{name: "one", path: "same"})
	candidates = appendAgentCandidate(candidates, map[string]struct{}{filepath.Clean("same"): {}}, agentWorktreeCandidate{name: "two", path: "same"})
	if len(candidates) != 1 || candidates[0].name != "one" {
		t.Fatalf("duplicate appendAgentCandidate = %#v", candidates)
	}
	if repo := repoForAgentWorktree(config.WorkspaceConfig{}, "agent", "/nowhere"); repo != nil {
		t.Fatalf("repoForAgentWorktree without repos = %#v, want nil", repo)
	}
	if _, err := (&Resolver{}).ResolveAgentByName("agent"); err == nil {
		t.Fatal("ResolveAgentByName without workspace mode succeeded")
	}
	if _, err := (&Resolver{Mode: ModeWorkspace, Config: cfg, Workspace: "UNKNOWN"}).ResolveAgentByName("agent"); err == nil {
		t.Fatal("ResolveAgentByName unknown workspace succeeded")
	}

	if p, ok := (&Resolver{}).ResolveWorkspaceByName("REL"); ok || p != "" {
		t.Fatalf("ResolveWorkspaceByName inactive = %q/%t", p, ok)
	}
	if names := (&Resolver{}).WorkspaceNames(); names != nil {
		t.Fatalf("WorkspaceNames inactive = %#v, want nil", names)
	}
	if got := (&Resolver{}).GetWorktreesDir(); got != "." {
		t.Fatalf("GetWorktreesDir nil = %q, want .", got)
	}
	if got := r.GetWorktreesDir(); got != relRoot {
		t.Fatalf("GetWorktreesDir = %q, want relative root", got)
	}
	if got := (*Resolver)(nil).GetDefaultBranch(); got != "main" {
		t.Fatalf("nil default branch = %q, want main", got)
	}

	if _, err := r.ResolveWorkspacePath(filepath.Join(root, "does-not-exist")); err == nil {
		t.Fatal("ResolveWorkspacePath missing absolute path succeeded")
	}
	if _, err := (&Resolver{Mode: ModeWorkspace, Config: cfg, Workspace: "UNKNOWN"}).ResolveWorkspacePath("repo"); err == nil {
		t.Fatal("ResolveWorkspacePath unknown workspace succeeded")
	}
	if _, err := (&Resolver{Mode: ModeWorkspace, Config: cfg, Workspace: "MISSING"}).ResolveWorkspacePath("gone"); err == nil {
		t.Fatal("ResolveWorkspacePath missing repo path succeeded")
	}

	ResetWorkspaceRuntimeDirCache()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", filepath.Join(root, "runtime"))
	if got := GetWorkspaceRuntimeDir(); got != filepath.Join(root, "runtime") {
		t.Fatalf("GetWorkspaceRuntimeDir env = %q", got)
	}
	ResetWorkspaceRuntimeDirCache()
}

func TestResolverSetRepoDefaultBranchAgainstFleetStore(t *testing.T) {
	requireLockFleetDB(t)

	ctx := context.Background()
	dataDir := t.TempDir()
	workspaceDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dataDir)
	t.Setenv(bootstrap.EnvWorkspace, "WS")
	t.Setenv(bootstrap.EnvFleetDBActor, "worktree-resolver-test")
	t.Setenv(bootstrap.EnvFleetDBURL, "")

	handle, err := bootstrap.OpenStore(ctx, dataDir, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if _, err := handle.Store.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := handle.Store.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  "WS",
		Name:          "api",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		LastWorkspace: "WS",
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"WS": {Path: workspaceDir, Repos: map[string]string{"api": filepath.Join(workspaceDir, "api")}},
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"WS": {
			ID:   "WS",
			Path: workspaceDir,
			Repos: []config.RepoConfig{{
				Name:          "api",
				Path:          filepath.Join(workspaceDir, "api"),
				DefaultBranch: "main",
			}},
		},
	}}
	resolver := &Resolver{Mode: ModeWorkspace, Config: cfg, Workspace: "WS"}
	if err := resolver.SetRepoDefaultBranch("api", "trunk"); err != nil {
		t.Fatalf("SetRepoDefaultBranch: %v", err)
	}
	if got := resolver.Config.Workspaces["WS"].Repos[0].DefaultBranch; got != "trunk" {
		t.Fatalf("resolver config branch = %q, want trunk", got)
	}
	repo, err := handle.Store.Repos().Get(ctx, "WS", "api")
	if err != nil {
		t.Fatalf("get repo: %v", err)
	}
	if repo.DefaultBranch != "trunk" {
		t.Fatalf("store branch = %q, want trunk", repo.DefaultBranch)
	}

	if err := (&Resolver{}).SetRepoDefaultBranch("api", "main"); err == nil ||
		!strings.Contains(err.Error(), "workspace mode") {
		t.Fatalf("SetRepoDefaultBranch non-workspace err = %v", err)
	}
	if err := resolver.SetRepoDefaultBranch("missing", "main"); err == nil ||
		!strings.Contains(err.Error(), "update repo") {
		t.Fatalf("SetRepoDefaultBranch missing err = %v", err)
	}
}

func candidateNames(candidates []agentWorktreeCandidate) []string {
	names := make([]string, len(candidates))
	for i, candidate := range candidates {
		names[i] = candidate.name
	}
	return names
}

func worktreeNames(worktrees []WorktreeInfo) []string {
	names := make([]string, len(worktrees))
	for i, wt := range worktrees {
		names[i] = wt.Name
	}
	return names
}
