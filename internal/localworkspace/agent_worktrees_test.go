package localworkspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func staticWorkspace(view LocalWorkspaceView) ResolveLocalWorkspaceFunc {
	return func(context.Context, string) (LocalWorkspaceView, error) { return view, nil }
}

// TestMaterializeSkipsPathlessWorkspace pins the distributed/cloud shape: a
// workspace with no checkout on this machine materializes nothing and reports
// success.
func TestMaterializeSkipsPathlessWorkspace(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	m := AgentWorktreeMaterializer{
		ResolveWorkspace: staticWorkspace(LocalWorkspaceView{
			Root:  "",
			Repos: []Repo{{Name: "repo", Path: "/nonexistent/repo"}},
		}),
	}
	agent := domain.Agent{WorkspaceKey: "TEST", Name: "worker-1", CrossRepo: true}
	if err := m.Materialize(context.Background(), agent); err != nil {
		t.Fatalf("Materialize() on path-less workspace: %v", err)
	}
	if path, ok := RememberedAgentWorktree("TEST", "worker-1"); ok {
		t.Fatalf("path-less workspace remembered worktree %q, want none", path)
	}
}

// TestMaterializeErrorsOnZeroRepos pins the message text both surfaces render
// (the webui rewrites it, the CLI prints it verbatim).
func TestMaterializeErrorsOnZeroRepos(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	m := AgentWorktreeMaterializer{
		ResolveWorkspace: staticWorkspace(LocalWorkspaceView{Root: t.TempDir()}),
	}
	err := m.Materialize(context.Background(), domain.Agent{WorkspaceKey: "TEST", Name: "worker-1"})
	var merr *MaterializeError
	if !errors.As(err, &merr) {
		t.Fatalf("Materialize() with zero repos error = %v, want *MaterializeError", err)
	}
	if merr.Kind != MaterializeNoRepos {
		t.Fatalf("error kind = %q, want %q", merr.Kind, MaterializeNoRepos)
	}
	if got, want := merr.Error(), `workspace TEST has no repos for agent "worker-1"`; got != want {
		t.Fatalf("error message = %q, want %q", got, want)
	}
}

// TestMaterializeSkipAgentShortCircuits covers the interactive-role skip: the
// workspace is never resolved for an agent the caller says needs no worktrees.
func TestMaterializeSkipAgentShortCircuits(t *testing.T) {
	resolved := false
	m := AgentWorktreeMaterializer{
		SkipAgent: func(context.Context, domain.Agent) (bool, error) { return true, nil },
		ResolveWorkspace: func(context.Context, string) (LocalWorkspaceView, error) {
			resolved = true
			return LocalWorkspaceView{}, nil
		},
	}
	if err := m.Materialize(context.Background(), domain.Agent{WorkspaceKey: "TEST", Name: "lead-1"}); err != nil {
		t.Fatalf("Materialize() on skipped agent: %v", err)
	}
	if resolved {
		t.Fatal("Materialize() resolved the workspace for a skipped agent")
	}
}

// TestMaterializePassesThroughLookupErrors keeps each surface's own error
// vocabulary intact for the lookups it injects.
func TestMaterializePassesThroughLookupErrors(t *testing.T) {
	sentinel := errors.New("lookup exploded")

	skipErr := AgentWorktreeMaterializer{
		SkipAgent:        func(context.Context, domain.Agent) (bool, error) { return false, sentinel },
		ResolveWorkspace: staticWorkspace(LocalWorkspaceView{}),
	}
	if err := skipErr.Materialize(context.Background(), domain.Agent{}); !errors.Is(err, sentinel) {
		t.Fatalf("SkipAgent error = %v, want %v", err, sentinel)
	}

	resolveErr := AgentWorktreeMaterializer{
		ResolveWorkspace: func(context.Context, string) (LocalWorkspaceView, error) {
			return LocalWorkspaceView{}, sentinel
		},
	}
	if err := resolveErr.Materialize(context.Background(), domain.Agent{}); !errors.Is(err, sentinel) {
		t.Fatalf("ResolveWorkspace error = %v, want %v", err, sentinel)
	}
}

// TestMaterializeClassifiesRepoAffinityFailure keeps the selection failure
// distinguishable from an operational one (the webui reports it as a
// validation error) while rendering the underlying message verbatim.
func TestMaterializeClassifiesRepoAffinityFailure(t *testing.T) {
	m := AgentWorktreeMaterializer{
		ResolveWorkspace: staticWorkspace(LocalWorkspaceView{
			Root:  t.TempDir(),
			Repos: []Repo{{Name: "api", Path: "/tmp/api"}},
		}),
	}
	agent := domain.Agent{WorkspaceKey: "TEST", Name: "worker-1", Repos: []string{"web"}}
	err := m.Materialize(context.Background(), agent)
	var merr *MaterializeError
	if !errors.As(err, &merr) || merr.Kind != MaterializeRepoSelection {
		t.Fatalf("Materialize() with unmatched affinity error = %v, want repo_selection MaterializeError", err)
	}
	if got, want := merr.Error(), "agent repo affinity does not match any workspace repo; available repos: api"; got != want {
		t.Fatalf("error message = %q, want %q", got, want)
	}
}

// TestMaterializeErrorsOnRepoWithoutLocalPath covers the defensive guard for a
// selected repo that has no on-disk path on this machine.
func TestMaterializeErrorsOnRepoWithoutLocalPath(t *testing.T) {
	m := AgentWorktreeMaterializer{
		ResolveWorkspace: staticWorkspace(LocalWorkspaceView{
			Root:  t.TempDir(),
			Repos: []Repo{{Name: "api", Path: ""}},
		}),
	}
	err := m.Materialize(context.Background(), domain.Agent{WorkspaceKey: "TEST", Name: "worker-1", CrossRepo: true})
	var merr *MaterializeError
	if !errors.As(err, &merr) || merr.Kind != MaterializeRepoPathMissing {
		t.Fatalf("Materialize() with path-less repo error = %v, want repo_path_missing MaterializeError", err)
	}
	if got, want := merr.Error(), `repo "api" has no local path on this machine`; got != want {
		t.Fatalf("error message = %q, want %q", got, want)
	}
}

// TestMaterializeCreatesWorktreePerRepo is the happy path: one worktree per
// selected repo under the workspace root, with the first (name-sorted) one
// remembered as the agent's local worktree, and re-running it is a no-op.
func TestMaterializeCreatesWorktreePerRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	root := t.TempDir()
	repos := make([]Repo, 0, 2)
	for _, name := range []string{"api", "web"} {
		path := filepath.Join(root, name)
		git(t, "", "init", path)
		git(t, path, "config", "user.name", "Test User")
		git(t, path, "config", "user.email", "test@example.test")
		writeFile(t, filepath.Join(path, "base.txt"), "v1\n")
		git(t, path, "add", "base.txt")
		git(t, path, "commit", "-m", "base")
		repos = append(repos, Repo{Name: name, Path: path})
	}

	m := AgentWorktreeMaterializer{
		ResolveWorkspace: staticWorkspace(LocalWorkspaceView{Root: root, Repos: repos}),
	}
	agent := domain.Agent{WorkspaceKey: "TEST", Name: "worker-1", CrossRepo: true}
	if err := m.Materialize(context.Background(), agent); err != nil {
		t.Fatalf("Materialize(): %v", err)
	}
	for _, name := range []string{"api", "web"} {
		target := AgentWorktreePath(root, name, "worker-1")
		if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
			t.Fatalf("worktree for repo %q was not created: %v", name, err)
		}
	}
	remembered, ok := RememberedAgentWorktree("TEST", "worker-1")
	if !ok {
		t.Fatal("agent worktree was not remembered")
	}
	if want := AgentWorktreePath(root, "api", "worker-1"); remembered != want {
		t.Fatalf("remembered worktree = %q, want %q", remembered, want)
	}

	// Idempotent: a second run leaves the existing worktrees untouched.
	if err := m.Materialize(context.Background(), agent); err != nil {
		t.Fatalf("Materialize() second run: %v", err)
	}
}

func TestAgentBranchName(t *testing.T) {
	if got, want := AgentBranchName("WSA", "dev-1"), "WSA--dev-1"; got != want {
		t.Fatalf("AgentBranchName() = %q, want %q", got, want)
	}
}

func TestMaterializeSharedRepoAcrossWorkspaces(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	repo := filepath.Join(t.TempDir(), "shared")
	git(t, "", "init", "-b", "master", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "base.txt"), "v1\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")

	rootA := t.TempDir()
	rootB := t.TempDir()
	sharedRepo := Repo{Name: "shared", Path: repo}
	materializerA := AgentWorktreeMaterializer{
		ResolveWorkspace: staticWorkspace(LocalWorkspaceView{Root: rootA, Repos: []Repo{sharedRepo}}),
	}
	materializerB := AgentWorktreeMaterializer{
		ResolveWorkspace: staticWorkspace(LocalWorkspaceView{Root: rootB, Repos: []Repo{sharedRepo}}),
	}
	agentA := domain.Agent{WorkspaceKey: "WSA", Name: "dev-1"}
	agentB := domain.Agent{WorkspaceKey: "WSB", Name: "dev-1"}

	if err := materializerA.Materialize(context.Background(), agentA); err != nil {
		t.Fatalf("Materialize() WSA: %v", err)
	}
	if err := materializerB.Materialize(context.Background(), agentB); err != nil {
		t.Fatalf("Materialize() WSB: %v", err)
	}

	worktreeA := AgentWorktreePath(rootA, sharedRepo.Name, agentA.Name)
	worktreeB := AgentWorktreePath(rootB, sharedRepo.Name, agentB.Name)
	if err := materializerA.Materialize(context.Background(), agentA); err != nil {
		t.Fatalf("Materialize() WSA second run: %v", err)
	}
	if got, want := gitOut(t, worktreeA, "rev-parse", "--abbrev-ref", "HEAD"), "WSA--dev-1"; got != want {
		t.Fatalf("WSA worktree branch = %q, want %q", got, want)
	}
	if got, want := gitOut(t, worktreeB, "rev-parse", "--abbrev-ref", "HEAD"), "WSB--dev-1"; got != want {
		t.Fatalf("WSB worktree branch = %q, want %q", got, want)
	}
}
