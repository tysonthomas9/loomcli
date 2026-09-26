package localworkspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// agentWorktreeFixture builds a workspace root with `n` seeded git repos and
// isolates the state cache, so RememberAgentWorktree cannot write to the
// developer's real ~/.loom/state.json.
func agentWorktreeFixture(t *testing.T, names ...string) (string, []Repo) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	wsPath := t.TempDir()
	repos := make([]Repo, 0, len(names))
	for _, name := range names {
		path := filepath.Join(wsPath, name)
		git(t, "", "init", path)
		git(t, path, "config", "user.name", "T")
		git(t, path, "config", "user.email", "t@t")
		writeFile(t, filepath.Join(path, "base.txt"), "base\n")
		git(t, path, "add", "base.txt")
		git(t, path, "commit", "-m", "base")
		repos = append(repos, Repo{Name: name, Path: path})
	}
	return wsPath, repos
}

func crossRepoAgent(name string) domain.Agent {
	return domain.Agent{WorkspaceKey: "TEST", Name: name, RoleName: "task", CrossRepo: true}
}

func TestEnsureAgentWorktrees_CreatesPerRepoWorktrees(t *testing.T) {
	wsPath, repos := agentWorktreeFixture(t, "alpha", "beta")

	created, err := EnsureAgentWorktrees(wsPath, repos, crossRepoAgent("planner"))
	if err != nil {
		t.Fatalf("EnsureAgentWorktrees() error = %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("created = %v, want one worktree per repo", created)
	}
	for _, name := range []string{"alpha", "beta"} {
		want := AgentWorktreePath(wsPath, name, "planner")
		if created[name] != want {
			t.Fatalf("created[%q] = %q, want %q", name, created[name], want)
		}
		if _, err := os.Stat(filepath.Join(want, ".git")); err != nil {
			t.Fatalf("worktree for %q was not created on disk: %v", name, err)
		}
	}

	// The remembered path is what the supervisor and terminal launch read back.
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("LoadStateCache: %v", err)
	}
	if got := sc.Workspaces["TEST"].Agents["planner"].Worktree; got != created["alpha"] {
		t.Fatalf("remembered worktree = %q, want %q", got, created["alpha"])
	}
}

// Re-applying a spec must be a no-op, not a failure: `workspace apply` is
// documented as idempotent and operators re-run it after every edit.
func TestEnsureAgentWorktrees_Idempotent(t *testing.T) {
	wsPath, repos := agentWorktreeFixture(t, "alpha")

	first, err := EnsureAgentWorktrees(wsPath, repos, crossRepoAgent("planner"))
	if err != nil {
		t.Fatalf("first EnsureAgentWorktrees() error = %v", err)
	}
	second, err := EnsureAgentWorktrees(wsPath, repos, crossRepoAgent("planner"))
	if err != nil {
		t.Fatalf("second EnsureAgentWorktrees() error = %v", err)
	}
	if first["alpha"] != second["alpha"] {
		t.Fatalf("second run returned %q, want the same path as %q", second["alpha"], first["alpha"])
	}
}

func TestEnsureAgentWorktrees_ErrorsWhenRepoHasNoLocalPath(t *testing.T) {
	wsPath, repos := agentWorktreeFixture(t, "alpha")
	repos[0].Path = ""

	_, err := EnsureAgentWorktrees(wsPath, repos, crossRepoAgent("planner"))
	if err == nil || !strings.Contains(err.Error(), "no local path") {
		t.Fatalf("error = %v, want a missing-local-path error", err)
	}
	if _, statErr := os.Stat(AgentWorktreePath(wsPath, "alpha", "planner")); !os.IsNotExist(statErr) {
		t.Fatal("a worktree was created despite the hard error")
	}
}

func TestEnsureAgentWorktrees_ErrorsWhenNoReposSelected(t *testing.T) {
	wsPath, _ := agentWorktreeFixture(t)

	_, err := EnsureAgentWorktrees(wsPath, nil, crossRepoAgent("planner"))
	if err == nil || !strings.Contains(err.Error(), "no repos for agent") {
		t.Fatalf("error = %v, want a no-repos error", err)
	}
}

// An agent whose repo affinity names nothing the workspace has must fail here,
// before any store write — not silently pick a repo.
func TestEnsureAgentWorktrees_ErrorsWhenRepoAffinityMatchesNothing(t *testing.T) {
	wsPath, repos := agentWorktreeFixture(t, "alpha")
	agent := domain.Agent{WorkspaceKey: "TEST", Name: "planner", RoleName: "task", Repos: []string{"nonexistent"}}

	if _, err := EnsureAgentWorktrees(wsPath, repos, agent); err == nil {
		t.Fatal("expected an affinity error for a repo the workspace does not have")
	}
}

func TestPlanAgentWorktrees_ReportsExistingAndToCreate_WithoutTouchingDisk(t *testing.T) {
	wsPath, repos := agentWorktreeFixture(t, "alpha", "beta")

	// Provision only alpha, so the plan has one of each.
	if _, err := EnsureAgentWorktrees(wsPath, repos[:1], crossRepoAgent("planner")); err != nil {
		t.Fatalf("seed EnsureAgentWorktrees() error = %v", err)
	}
	betaPath := AgentWorktreePath(wsPath, "beta", "planner")

	plan, err := PlanAgentWorktrees(wsPath, repos, crossRepoAgent("planner"))
	if err != nil {
		t.Fatalf("PlanAgentWorktrees() error = %v", err)
	}
	if plan.Agent != "planner" {
		t.Fatalf("plan.Agent = %q, want planner", plan.Agent)
	}
	if len(plan.Existing) != 1 || plan.Existing["alpha"] != AgentWorktreePath(wsPath, "alpha", "planner") {
		t.Fatalf("plan.Existing = %v, want alpha present", plan.Existing)
	}
	if len(plan.ToCreate) != 1 || plan.ToCreate["beta"] != betaPath {
		t.Fatalf("plan.ToCreate = %v, want beta to create", plan.ToCreate)
	}
	if _, statErr := os.Stat(betaPath); !os.IsNotExist(statErr) {
		t.Fatal("PlanAgentWorktrees created a worktree; a dry run must touch nothing")
	}
}

// The plan path must refuse exactly what the real path refuses, or --dry-run
// reports clean and the apply that follows it fails.
func TestPlanAgentWorktrees_ErrorsOnTheSameHardProblems(t *testing.T) {
	wsPath, repos := agentWorktreeFixture(t, "alpha")
	repos[0].Path = ""

	if _, err := PlanAgentWorktrees(wsPath, repos, crossRepoAgent("planner")); err == nil {
		t.Fatal("expected a missing-local-path error from the plan path too")
	}
	if _, err := PlanAgentWorktrees(wsPath, nil, crossRepoAgent("planner")); err == nil {
		t.Fatal("expected a no-repos error from the plan path too")
	}
}
