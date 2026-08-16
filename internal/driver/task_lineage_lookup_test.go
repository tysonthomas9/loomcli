package driver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	stackstore "github.com/tysonthomas9/loomcli/internal/infra/sourcecontrolstackstore"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

// lineageFixture builds a repo with a `main` branch and a stacked predecessor
// branch loom/stack/epic-E1/task-a (with a commit distinct from main), registers
// workspace local state + a memstore repo, and a stackstore stack epic-E1 with
// nodes task-a (root, published on that branch) and task-b (based on task-a).
// It returns the resolver wired with the lineage lookup plus the two HEAD SHAs.
type lineageFixture struct {
	resolver LocalTaskWorktreeResolver
	mainHead string
	taskAH   string // HEAD of loom/stack/epic-E1/task-a
}

func setupLineageFixture(t *testing.T) lineageFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	workspacePath := filepath.Join(t.TempDir(), "workspace")
	repoPath := filepath.Join(workspacePath, "app")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	gitCmd(t, repoPath, "init")
	gitCmd(t, repoPath, "checkout", "-b", "main")
	gitCmd(t, repoPath, "config", "user.name", "Test User")
	gitCmd(t, repoPath, "config", "user.email", "test@example.test")
	writeTestFile(t, filepath.Join(repoPath, "src", "app.js"), "console.log('base');\n")
	gitCmd(t, repoPath, "add", "src/app.js")
	gitCmd(t, repoPath, "commit", "-m", "base")
	mainHead := strings.TrimSpace(testGitOutput(t, repoPath, "rev-parse", "HEAD"))

	// Predecessor output branch with a distinct commit on top of main.
	const predBranch = "loom/stack/epic-E1/task-a"
	gitCmd(t, repoPath, "checkout", "-b", predBranch)
	writeTestFile(t, filepath.Join(repoPath, "src", "a.js"), "console.log('task-a');\n")
	gitCmd(t, repoPath, "add", "src/a.js")
	gitCmd(t, repoPath, "commit", "-m", "task-a work")
	taskAHead := strings.TrimSpace(testGitOutput(t, repoPath, "rev-parse", "HEAD"))
	gitCmd(t, repoPath, "checkout", "main")

	if err := bootstrap.MutateWorkspaceLocalState("TEST", func(local *bootstrap.WorkspaceLocalState) error {
		local.Path = workspacePath
		local.Repos = map[string]string{"app": repoPath}
		return nil
	}); err != nil {
		t.Fatalf("write local state: %v", err)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, workspaceowner.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, workspaceowner.RepoCreate{
		WorkspaceKey:  "TEST",
		Name:          "app",
		DefaultBranch: "main",
		SourceRepoID:  "frontend",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	stacks := stackstore.New(loomDir)
	if err := stacks.EnsureStackRecord(ctx, sourcecontrol.Stack{
		ID:           "epic-E1",
		WorkspaceKey: "TEST",
		Repository:   "app",
		RootBase:     "main",
	}); err != nil {
		t.Fatalf("ensure stack: %v", err)
	}
	if _, err := stacks.AddStackNodeRecord(ctx, "TEST", "epic-E1", "task-a", "", sourcecontrol.CommitModeAgent); err != nil {
		t.Fatalf("add node task-a: %v", err)
	}
	if _, err := stacks.AddStackNodeRecord(ctx, "TEST", "epic-E1", "task-b", "task-a", sourcecontrol.CommitModeAgent); err != nil {
		t.Fatalf("add node task-b: %v", err)
	}

	return lineageFixture{
		resolver: LocalTaskWorktreeResolver{
			Store: st, Lineage: StackLineageLookup{Bindings: mustTestStackLifecycle(t, stacks)},
			SourceControl: testTaskSourceControl{},
		},
		mainHead: mainHead,
		taskAH:   taskAHead,
	}
}

func resolveHead(t *testing.T, r LocalTaskWorktreeResolver, taskID, taskRunID string) string {
	t.Helper()
	resolved, err := r.ResolveTaskWorktree(context.Background(), TaskExecRequest{
		WorkspaceKey:     "TEST",
		TaskRunID:        taskRunID,
		TaskID:           taskID,
		SandboxPlacement: execution.TaskRunPlacementRecord{RepoRef: "frontend"},
	}, t.TempDir())
	if err != nil {
		t.Fatalf("ResolveTaskWorktree(%s): %v", taskID, err)
	}
	if _, err := os.Stat(filepath.Join(resolved.Path, ".git")); err != nil {
		t.Fatalf("resolved worktree .git missing: %v", err)
	}
	return strings.TrimSpace(testGitOutput(t, resolved.Path, "rev-parse", "HEAD"))
}

// A dependent task's worktree is cut from its predecessor's output branch, not main.
func TestResolveTaskWorktree_DependentBasesOnPredecessorBranch(t *testing.T) {
	f := setupLineageFixture(t)
	got := resolveHead(t, f.resolver, "task-b", "task/run:b")
	if got != f.taskAH {
		t.Fatalf("task-b worktree HEAD = %s, want predecessor task-a branch HEAD %s (got main=%s?)", got, f.taskAH, f.mainHead)
	}
}

// The root unit's worktree is cut from the stack RootBase (main).
func TestResolveTaskWorktree_RootBasesOnRootBase(t *testing.T) {
	f := setupLineageFixture(t)
	got := resolveHead(t, f.resolver, "task-a", "task/run:a")
	if got != f.mainHead {
		t.Fatalf("task-a (root) worktree HEAD = %s, want RootBase main HEAD %s", got, f.mainHead)
	}
}

// A task with no stack binding uses the repository's declared default branch.
func TestResolveTaskWorktree_UnknownTaskUsesDefaultBranch(t *testing.T) {
	f := setupLineageFixture(t)
	got := resolveHead(t, f.resolver, "task-not-in-stack", "task/run:x")
	if got != f.mainHead {
		t.Fatalf("unlisted task worktree HEAD = %s, want default-branch main HEAD %s", got, f.mainHead)
	}
}

// A resolver without a stack lookup uses the repository's declared default branch.
func TestResolveTaskWorktree_NilLineageUsesDefaultBranch(t *testing.T) {
	f := setupLineageFixture(t)
	r := LocalTaskWorktreeResolver{
		Store: f.resolver.Store, SourceControl: testTaskSourceControl{},
	} // no Lineage
	got := resolveHead(t, r, "task-b", "task/run:nil")
	if got != f.mainHead {
		t.Fatalf("nil-lineage worktree HEAD = %s, want default-branch main HEAD %s", got, f.mainHead)
	}
}

func TestStackLineageLookup_BaseRefForTask(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	loomDir := t.TempDir()
	stacks := stackstore.New(loomDir)
	if err := stacks.EnsureStackRecord(ctx, sourcecontrol.Stack{ID: "epic-E1", WorkspaceKey: "TEST", Repository: "app", RootBase: "main"}); err != nil {
		t.Fatalf("ensure stack: %v", err)
	}
	if _, err := stacks.AddStackNodeRecord(ctx, "TEST", "epic-E1", "task-a", "", sourcecontrol.CommitModeAgent); err != nil {
		t.Fatalf("add task-a: %v", err)
	}
	if _, err := stacks.AddStackNodeRecord(ctx, "TEST", "epic-E1", "task-b", "task-a", sourcecontrol.CommitModeAgent); err != nil {
		t.Fatalf("add task-b: %v", err)
	}
	lookup := StackLineageLookup{Bindings: mustTestStackLifecycle(t, stacks)}

	// AddNode assigns a deterministic OutputBranch at registration, so a dependent
	// resolves to its predecessor's branch immediately (the name exists even before
	// the branch is pushed — the Stage-2 finalize barrier governs existence).
	if ref, ok, err := lookup.BaseRefForTask(ctx, "TEST", "app", "task-b"); err != nil || !ok || ref != "loom/stack/epic-E1/task-a" {
		t.Fatalf("task-b = (%q,%v,%v), want (\"loom/stack/epic-E1/task-a\",true,nil)", ref, ok, err)
	}
	// Root task always resolves to RootBase.
	if ref, ok, err := lookup.BaseRefForTask(ctx, "TEST", "app", "task-a"); err != nil || !ok || ref != "main" {
		t.Fatalf("task-a = (%q,%v,%v), want (\"main\",true,nil)", ref, ok, err)
	}
	// Unknown task: no lineage → fall open.
	if ref, ok, err := lookup.BaseRefForTask(ctx, "TEST", "app", "task-zzz"); err != nil || ok || ref != "" {
		t.Fatalf("unknown task = (%q,%v,%v), want (\"\",false,nil)", ref, ok, err)
	}
	// Predecessor with no output branch (empty-diff): Stage 2 slides past it.
	// task-a is the root, so task-b re-parents onto the stack RootBase ("main")
	// rather than falling open to the repo default branch.
	if err := stacks.UpdateStackNodePublicationRecord(ctx, "TEST", "epic-E1", "task-a", sourcecontrol.StackNodePublicationMutation{
		State: sourcecontrol.StackPublicationEmpty,
	}); err != nil {
		t.Fatalf("clear task-a branch: %v", err)
	}
	if ref, ok, err := lookup.BaseRefForTask(ctx, "TEST", "app", "task-b"); err != nil || !ok || ref != "main" {
		t.Fatalf("empty-predecessor task-b = (%q,%v,%v), want (\"main\",true,nil) [Stage-2 slide to RootBase]", ref, ok, err)
	}
	// A repo mismatch never matches the stack.
	if ref, ok, err := lookup.BaseRefForTask(ctx, "TEST", "other-repo", "task-b"); err != nil || ok || ref != "" {
		t.Fatalf("repo-mismatch = (%q,%v,%v), want (\"\",false,nil)", ref, ok, err)
	}
	// A nil store is inert.
	if ref, ok, err := (StackLineageLookup{}).BaseRefForTask(ctx, "TEST", "app", "task-a"); err != nil || ok || ref != "" {
		t.Fatalf("nil-store = (%q,%v,%v), want (\"\",false,nil)", ref, ok, err)
	}
}

// Regression for the review's HIGH finding: a stack with an empty RepoName (only
// reachable via a hand-edited/legacy store) must NOT hijack an unrelated repo's
// base, a stack for a different repo must not match, and a taskID present in more
// than one stack for the same repo is ambiguous and falls open.
func TestStackLineageLookup_RepoScopingAndAmbiguity(t *testing.T) {
	ctx := context.Background()
	stacks := stackstore.New(t.TempDir())
	mustStack := func(id sourcecontrol.StackID, repo, root string) {
		if err := stacks.EnsureStackRecord(ctx, sourcecontrol.Stack{ID: id, WorkspaceKey: "TEST", Repository: repo, RootBase: root}); err != nil {
			t.Fatalf("ensure %s: %v", id, err)
		}
	}
	mustNode := func(id sourcecontrol.StackID, taskID, base string) {
		if _, err := stacks.AddStackNodeRecord(ctx, "TEST", id, taskID, base, sourcecontrol.CommitModeAgent); err != nil {
			t.Fatalf("add %s/%s: %v", id, taskID, err)
		}
	}
	// Legacy stack with NO repo name, containing a node that collides with an
	// epic task id; and a stack for a different repo.
	mustStack("legacy", "", "release-2.0")
	mustNode("legacy", "shared-task", "")
	mustStack("other-repo-stack", "backend", "main")
	mustNode("other-repo-stack", "be-task", "")
	lookup := StackLineageLookup{Bindings: mustTestStackLifecycle(t, stacks)}

	if ref, ok, err := lookup.BaseRefForTask(ctx, "TEST", "app", "shared-task"); err != nil || ok || ref != "" {
		t.Fatalf("empty-RepoName stack hijack = (%q,%v,%v), want (\"\",false,nil)", ref, ok, err)
	}
	if ref, ok, err := lookup.BaseRefForTask(ctx, "TEST", "app", "be-task"); err != nil || ok || ref != "" {
		t.Fatalf("cross-repo match = (%q,%v,%v), want (\"\",false,nil)", ref, ok, err)
	}

	// Two stacks for the SAME repo both containing the same task id → ambiguous.
	mustStack("epicA", "app", "main")
	mustNode("epicA", "dup", "")
	mustStack("epicB", "app", "trunk")
	mustNode("epicB", "dup", "")
	if ref, ok, err := lookup.BaseRefForTask(ctx, "TEST", "app", "dup"); err != nil || ok || ref != "" {
		t.Fatalf("ambiguous task = (%q,%v,%v), want (\"\",false,nil)", ref, ok, err)
	}
}

// Regression for the review's MEDIUM finding: graph-integrity corruption (a
// dependent pointing at a predecessor absent from the stack) must surface as an
// error, NOT be silently treated like an unknown task (which would rebase onto
// the default branch invisibly). Empty and closed predecessors slide to the
// nearest usable ancestor under Source Control's owner policy.
func TestStackLineageLookup_GraphCorruptionSurfaced(t *testing.T) {
	ctx := context.Background()
	loomDir := t.TempDir()
	stacks := stackstore.New(loomDir)
	if err := stacks.EnsureStackRecord(ctx, sourcecontrol.Stack{ID: "epic", WorkspaceKey: "TEST", Repository: "app", RootBase: "main"}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := stacks.AddStackNodeRecord(ctx, "TEST", "epic", "task-a", "", sourcecontrol.CommitModeAgent); err != nil {
		t.Fatalf("add a: %v", err)
	}
	if _, err := stacks.AddStackNodeRecord(ctx, "TEST", "epic", "task-b", "task-a", sourcecontrol.CommitModeAgent); err != nil {
		t.Fatalf("add b: %v", err)
	}
	// Corrupt the graph: point task-b at a predecessor that is not in the stack.
	corruptStoredStackNodeBase(t, loomDir, "TEST", "epic", "task-b", "ghost-task")
	_, ok, err := (StackLineageLookup{Bindings: mustTestStackLifecycle(t, stacks)}).BaseRefForTask(ctx, "TEST", "app", "task-b")
	if ok {
		t.Fatalf("corrupt graph returned ok=true, want surfaced error")
	}
	if !errors.Is(err, sourcecontrol.ErrInvalidMaterialization) {
		t.Fatalf("corrupt graph err = %v, want Source Control invalid materialization surfaced", err)
	}
}

func corruptStoredStackNodeBase(t *testing.T, loomDir, workspace, stackID, taskID, baseTaskID string) {
	t.Helper()
	path := filepath.Join(loomDir, "stacks.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read stack fixture: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode stack fixture: %v", err)
	}
	workspaces := document["workspaces"].(map[string]any)
	workspaceValue := workspaces[workspace].(map[string]any)
	stacks := workspaceValue["stacks"].(map[string]any)
	stack := stacks[stackID].(map[string]any)
	nodes := stack["nodes"].(map[string]any)
	node := nodes[taskID].(map[string]any)
	node["baseTaskId"] = baseTaskID
	data, err = json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatalf("encode corrupt stack fixture: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write corrupt stack fixture: %v", err)
	}
}

func mustTestStackLifecycle(t *testing.T, store sourcecontrol.StackLifecycleStore) *sourcecontrol.StackLifecycleService {
	t.Helper()
	service, err := sourcecontrol.NewStackLifecycle(store, time.Now)
	if err != nil {
		t.Fatalf("compose stack lifecycle: %v", err)
	}
	return service
}

// errLineage is a TaskLineageLookup that always errors, modeling a corrupt/
// unreadable stack store on the dispatch hot path.
type errLineage struct{}

func (errLineage) BaseRefForTask(context.Context, string, string, string) (string, bool, error) {
	return "", false, errors.New("boom: unreadable stack store")
}

func TestResolveTaskWorktree_LineageErrorFailsClosed(t *testing.T) {
	f := setupLineageFixture(t)
	r := LocalTaskWorktreeResolver{
		Store: f.resolver.Store, Lineage: errLineage{},
		SourceControl: testTaskSourceControl{},
	}
	_, err := r.ResolveTaskWorktree(t.Context(), TaskExecRequest{
		WorkspaceKey:     "TEST",
		TaskRunID:        "task/run:err",
		TaskID:           "task-b",
		SandboxPlacement: execution.TaskRunPlacementRecord{RepoRef: "frontend"},
	}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "resolve task stack lineage") {
		t.Fatalf("lineage-error ResolveTaskWorktree error = %v, want fail-closed lineage error", err)
	}
}
