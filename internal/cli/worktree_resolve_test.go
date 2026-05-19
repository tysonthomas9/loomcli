package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
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
