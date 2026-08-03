package opsimpl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/gitbranch"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/testutil"
)

type repairFixture struct {
	g         *GitOpsImpl
	wsRoot    string
	repoPath  string
	agentPath string
}

func setupRepairFixture(t *testing.T, createAgentWorktree bool) repairFixture {
	t.Helper()
	ctx := context.Background()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	wsRoot := filepath.Join(t.TempDir(), "workspace")
	repoPath := filepath.Join(wsRoot, "api")
	agentPath := filepath.Join(wsRoot, "worktrees", "api", "nova")

	if err := runGit(t, repoPath, "init", "-b", "main"); err != nil {
		t.Fatalf("git init source: %v", err)
	}
	if err := runGit(t, repoPath, "config", "user.email", "test@example.com"); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if err := runGit(t, repoPath, "config", "user.name", "Test User"); err != nil {
		t.Fatalf("git config name: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "a.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	if err := runGit(t, repoPath, "add", "a.txt"); err != nil {
		t.Fatalf("git add source: %v", err)
	}
	if err := runGit(t, repoPath, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit source: %v", err)
	}
	if createAgentWorktree {
		if err := os.MkdirAll(filepath.Dir(agentPath), 0o755); err != nil {
			t.Fatalf("mkdir worktrees: %v", err)
		}
		if err := runGit(t, repoPath, "worktree", "add", agentPath, "-b", "nova"); err != nil {
			t.Fatalf("git worktree add agent: %v", err)
		}
	}

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
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  "WS1",
		Name:          "docs",
		DefaultBranch: "main",
		Remote:        "origin",
		Groups:        []string{"docs"},
	}); err != nil {
		t.Fatalf("create docs repo: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS1", Name: "task"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	nova := runtimeAgent(t, "WS1", "nova", "task", nil, []string{"backend"})
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.LastWorkspace = "WS1"
		sc.Workspaces["WS1"] = bootstrap.WorkspaceLocalState{
			Path: wsRoot,
			Repos: map[string]string{
				"api":  repoPath,
				"docs": filepath.Join(wsRoot, "docs"),
			},
		}
		return nil
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	return repairFixture{
		g:      NewGitOps().WithStore(st).WithAgentQueries(testutil.StaticAgentQueries{Agents: []*agents.Agent{nova}}),
		wsRoot: wsRoot, repoPath: repoPath, agentPath: agentPath,
	}
}

func TestRepairCheckout_DisallowedRepoRejected(t *testing.T) {
	fx := setupRepairFixture(t, false)

	_, err := fx.g.RepairCheckout("WS1", "agent", "nova", "docs", false)
	if !errors.Is(err, ops.ErrAgentRepoNotAllowed) {
		t.Fatalf("err = %v, want ErrAgentRepoNotAllowed", err)
	}
}

func TestRepairCheckout_SafeRepairSuccess(t *testing.T) {
	fx := setupRepairFixture(t, true)
	moveAdminInsideWorktrees(t, fx.agentPath)
	if repairCheckoutHealthy(fx.agentPath) {
		t.Fatal("test setup did not break checkout status")
	}

	result, err := fx.g.RepairCheckout("WS1", "agent", "nova", "api", false)
	if err != nil {
		t.Fatalf("RepairCheckout: %v", err)
	}
	if !result.Repaired || result.Method != "repair" {
		t.Fatalf("result = %+v, want repaired repair", result)
	}
	if !repairCheckoutHealthy(fx.agentPath) {
		t.Fatal("checkout not healthy after safe repair")
	}
}

func TestRepairCheckout_SafeRepairRequiresForceWithoutMutation(t *testing.T) {
	fx := setupRepairFixture(t, true)
	moveAdminOutsideWorktrees(t, fx.agentPath)

	result, err := fx.g.RepairCheckout("WS1", "agent", "nova", "api", false)
	if err != nil {
		t.Fatalf("RepairCheckout: %v", err)
	}
	if result.Repaired || !result.RequiresForce || result.Method != "none" {
		t.Fatalf("result = %+v, want requires_force without repair", result)
	}
	if _, err := os.Lstat(fx.agentPath); err != nil {
		t.Fatalf("agent path mutated or missing: %v", err)
	}
	matches, err := filepath.Glob(fx.agentPath + ".broken-*")
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("backup paths created without force: %v", matches)
	}
}

func TestRepairCheckout_ForceRecreatePreservesContents(t *testing.T) {
	fx := setupRepairFixture(t, true)
	if err := os.WriteFile(filepath.Join(fx.agentPath, "a.txt"), []byte("modified\n"), 0o644); err != nil {
		t.Fatalf("modify tracked file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fx.agentPath, "note.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}
	moveAdminOutsideWorktrees(t, fx.agentPath)

	result, err := fx.g.RepairCheckout("WS1", "agent", "nova", "api", true)
	if err != nil {
		t.Fatalf("RepairCheckout force: %v", err)
	}
	if !result.Repaired || result.Method != "recreate" || result.BackupPath == "" {
		t.Fatalf("result = %+v, want recreated with backup", result)
	}
	if !repairCheckoutHealthy(fx.agentPath) {
		t.Fatal("recreated checkout is not healthy")
	}
	if got, err := os.ReadFile(filepath.Join(fx.agentPath, "a.txt")); err != nil || string(got) != "modified\n" {
		t.Fatalf("tracked file after recreate = %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(fx.agentPath, "note.txt")); err != nil || string(got) != "untracked\n" {
		t.Fatalf("untracked file after recreate = %q, %v", got, err)
	}
	if _, err := os.Lstat(result.BackupPath); err != nil {
		t.Fatalf("backup path missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(result.BackupPath, "note.txt")); err != nil {
		t.Fatalf("original directory contents not preserved in backup: %v", err)
	}
	status, err := runRepairGit(fx.agentPath, "status", "--porcelain")
	if err != nil {
		t.Fatalf("status after recreate: %v", err)
	}
	if !strings.Contains(status, "M a.txt") || !strings.Contains(status, "?? note.txt") {
		t.Fatalf("status after recreate = %q, want restored modified and untracked files", status)
	}
	if strings.Contains(result.Message, "Branch ref") {
		t.Fatalf("healthy branch message = %q, did not want branch recovery disclosure", result.Message)
	}
}

func TestRepairCheckout_ForceRecreateRecoversBrokenBranchFromDefault(t *testing.T) {
	fx := setupRepairFixture(t, true)
	if err := os.WriteFile(filepath.Join(fx.agentPath, "note.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}
	moveAdminOutsideWorktrees(t, fx.agentPath)
	moveBranchReflogAside(t, fx.repoPath, "nova")
	corruptLooseBranchRef(t, fx.repoPath, "nova")

	result, err := fx.g.RepairCheckout("WS1", "agent", "nova", "api", true)
	if err != nil {
		t.Fatalf("RepairCheckout force: %v", err)
	}
	if !result.Repaired || result.Method != "recreate" || result.BackupPath == "" {
		t.Fatalf("result = %+v, want recreated with backup", result)
	}
	if !strings.Contains(result.Message, "corrupt") || !strings.Contains(result.Message, "default branch main") {
		t.Fatalf("message = %q, want corrupt/default branch recovery disclosure", result.Message)
	}
	if !repairCheckoutHealthy(fx.agentPath) {
		t.Fatal("recreated checkout is not healthy")
	}
	if got, err := os.ReadFile(filepath.Join(fx.agentPath, "note.txt")); err != nil || string(got) != "untracked\n" {
		t.Fatalf("untracked file after recreate = %q, %v", got, err)
	}
	assertBranchCommitExists(t, fx.repoPath, "nova")
	assertPathExists(t, result.BackupPath)
}

func TestRepairCheckout_ForceRecreateRecoversBrokenBranchFromReflog(t *testing.T) {
	fx := setupRepairFixture(t, true)
	if err := os.WriteFile(filepath.Join(fx.agentPath, "a.txt"), []byte("committed\n"), 0o644); err != nil {
		t.Fatalf("modify tracked file: %v", err)
	}
	if err := runGit(t, fx.agentPath, "add", "a.txt"); err != nil {
		t.Fatalf("git add agent: %v", err)
	}
	if err := runGit(t, fx.agentPath, "commit", "-m", "agent commit"); err != nil {
		t.Fatalf("git commit agent: %v", err)
	}
	agentSHA := mustRunRepairGit(t, fx.agentPath, "rev-parse", "HEAD")
	moveAdminOutsideWorktrees(t, fx.agentPath)
	corruptLooseBranchRef(t, fx.repoPath, "nova")

	result, err := fx.g.RepairCheckout("WS1", "agent", "nova", "api", true)
	if err != nil {
		t.Fatalf("RepairCheckout force: %v", err)
	}
	if !strings.Contains(result.Message, "corrupt") || !strings.Contains(result.Message, "reflog") {
		t.Fatalf("message = %q, want corrupt/reflog recovery disclosure", result.Message)
	}
	head := mustRunRepairGit(t, fx.agentPath, "rev-parse", "HEAD")
	if head != agentSHA {
		t.Fatalf("recreated branch HEAD = %s, want recovered reflog SHA %s", head, agentSHA)
	}
}

func TestRepairCheckout_ForceRecreateRollsBackOnProvisionFailure(t *testing.T) {
	fx := setupRepairFixture(t, true)
	if err := os.WriteFile(filepath.Join(fx.agentPath, "note.txt"), []byte("original\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	moveAdminOutsideWorktrees(t, fx.agentPath)
	restoreProvision := stubRepairProvisionFailure(errors.New("stub provision failure"))
	t.Cleanup(restoreProvision)

	_, err := fx.g.RepairCheckout("WS1", "agent", "nova", "api", true)
	if err == nil {
		t.Fatal("RepairCheckout force succeeded, want provision failure")
	}
	if !strings.Contains(err.Error(), "stub provision failure") || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("error = %v, want failure plus rollback message", err)
	}
	if got, err := os.ReadFile(filepath.Join(fx.agentPath, "note.txt")); err != nil || string(got) != "original\n" {
		t.Fatalf("original checkout contents after rollback = %q, %v", got, err)
	}
	if _, err := os.Lstat(fx.agentPath); err != nil {
		t.Fatalf("agent path missing after rollback: %v", err)
	}
}

func TestRepairCheckout_ProvisionMissingAgentCheckout(t *testing.T) {
	fx := setupRepairFixture(t, false)

	result, err := fx.g.RepairCheckout("WS1", "agent", "nova", "api", false)
	if err != nil {
		t.Fatalf("RepairCheckout provision: %v", err)
	}
	if !result.Repaired || result.Method != "provision" {
		t.Fatalf("result = %+v, want provision", result)
	}
	if !repairCheckoutHealthy(fx.agentPath) {
		t.Fatal("provisioned checkout is not healthy")
	}
	branch, err := runRepairGit(fx.agentPath, "branch", "--show-current")
	if err != nil {
		t.Fatalf("branch after provision: %v", err)
	}
	if strings.TrimSpace(branch) != "nova" {
		t.Fatalf("branch = %q, want nova", branch)
	}
	if !strings.Contains(result.Message, "missing") || !strings.Contains(result.Message, "default branch main") {
		t.Fatalf("message = %q, want missing/default branch recovery disclosure", result.Message)
	}
}

func moveAdminInsideWorktrees(t *testing.T, worktreePath string) {
	t.Helper()
	admin := worktreeAdminPath(t, worktreePath)
	target := filepath.Join(filepath.Dir(admin), "moved-admin")
	if err := os.Rename(admin, target); err != nil {
		t.Fatalf("move admin inside worktrees: %v", err)
	}
}

func moveAdminOutsideWorktrees(t *testing.T, worktreePath string) {
	t.Helper()
	admin := worktreeAdminPath(t, worktreePath)
	target := filepath.Join(t.TempDir(), filepath.Base(admin)+".admin-backup")
	if err := os.Rename(admin, target); err != nil {
		t.Fatalf("move admin outside worktrees: %v", err)
	}
}

func worktreeAdminPath(t *testing.T, worktreePath string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(worktreePath, ".git"))
	if err != nil {
		t.Fatalf("read worktree .git file: %v", err)
	}
	admin, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
	if !ok || admin == "" {
		t.Fatalf("unexpected .git file content: %q", data)
	}
	return admin
}

func corruptLooseBranchRef(t *testing.T, repoPath, branch string) {
	t.Helper()
	refPath := filepath.Join(repairTestGitCommonDir(t, repoPath), "refs", "heads", filepath.FromSlash(branch))
	if err := os.MkdirAll(filepath.Dir(refPath), 0o755); err != nil {
		t.Fatalf("mkdir branch ref parent: %v", err)
	}
	if err := os.WriteFile(refPath, nil, 0o644); err != nil {
		t.Fatalf("corrupt branch ref: %v", err)
	}
}

func moveBranchReflogAside(t *testing.T, repoPath, branch string) {
	t.Helper()
	logPath := filepath.Join(repairTestGitCommonDir(t, repoPath), "logs", "refs", "heads", filepath.FromSlash(branch))
	if _, err := os.Lstat(logPath); err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("stat branch reflog: %v", err)
	}
	if err := os.Rename(logPath, uniqueRepairBackupPath(logPath, "test")); err != nil {
		t.Fatalf("move branch reflog aside: %v", err)
	}
}

func repairTestGitCommonDir(t *testing.T, repoPath string) string {
	t.Helper()
	common, err := gitbranch.CommonDir(repoPath)
	if err != nil {
		t.Fatalf("git common dir: %v", err)
	}
	return common
}

func assertBranchCommitExists(t *testing.T, repoPath, branch string) {
	t.Helper()
	if _, err := runRepairGit(repoPath, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}"); err != nil {
		t.Fatalf("branch %q is not a valid commit ref: %v", branch, err)
	}
}

func assertPathExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("path %s missing: %v", path, err)
	}
}

func mustRunRepairGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runRepairGit(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(out)
}

func stubRepairProvisionFailure(stubErr error) func() {
	old := repairProvisionCheckoutWithBranch
	repairProvisionCheckoutWithBranch = func(
		_, _, branch, _ string,
		info gitbranch.Recovery,
	) (gitbranch.Recovery, error) {
		info.Branch = branch
		return info, stubErr
	}
	return func() { repairProvisionCheckoutWithBranch = old }
}
