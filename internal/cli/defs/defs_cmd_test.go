package defs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	defspkg "github.com/tysonthomas9/loomcli/internal/defs"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestDefsApplyStartProvisionsSingleRepoAgentWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	ctx := context.Background()
	root := t.TempDir()
	workspaceRoot := filepath.Join(root, "workspace")
	repoPath := filepath.Join(workspaceRoot, "repos", "slack-src")
	initDefsGitRepo(t, repoPath)
	writeDefsFile(t, root, ".loom/agents/nova.ts", `import { defineAgent, runtime } from '@loom/sdk';

export default defineAgent({
  name: 'nova',
  backend: 'echo',
  model: 'local/echo',
  repos: ['slack-src'],
  runtime: runtime.local({ repos: ['slack-src'] }),
  instructions: 'Work on Slack-clone tasks.',
});`)

	st := memstore.New()
	const workspace = "DEFSWT"
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: workspace, Name: "Definitions Worktree"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  workspace,
		Name:          "slack-src",
		DefaultBranch: "main",
		SourceRepoID:  "slack-src",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "loom-config"))
	t.Setenv(bootstrap.EnvWorkspace, workspace)
	t.Setenv("LOOM_FLEET_DB_ACTOR", "test")
	t.Cleanup(cfgpkg.InvalidateConfigCache)
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		LastWorkspace: workspace,
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			workspace: {
				Path: workspaceRoot,
				Repos: map[string]string{
					"slack-src": repoPath,
				},
			},
		},
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}
	if _, err := cfgpkg.TestingPrimeConfigCacheFromStore(ctx, st); err != nil {
		t.Fatalf("prime config cache: %v", err)
	}

	withDefsStore(t, st, workspace)
	withDefsGlobals(t, func() {
		defsDir = root
		defsApplyStart = true
		if err := runDefsApply(nil, nil); err != nil {
			t.Fatalf("runDefsApply() error = %v", err)
		}
	})

	agent, err := st.Agents().Get(ctx, workspace, "nova")
	if err != nil {
		t.Fatalf("get started agent: %v", err)
	}
	if agent.RoleName != "nova" || agent.DesiredState != domain.AgentDesiredRunning || agent.State != domain.AgentStateActive {
		t.Fatalf("agent = %+v, want nova active/running", agent)
	}
	worktreePath := filepath.Join(workspaceRoot, "worktrees", "slack-src", "nova")
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		t.Fatalf("defs apply worktree .git = %v, want provisioned worktree at %s", err, worktreePath)
	}
}

func TestProvisionStartedAgentWorktreesSkipsMultiRepoAgents(t *testing.T) {
	plan := &defspkg.Plan{Agents: []defspkg.AgentModule{{
		Name:  "multi",
		Repos: []string{"repo-a", "repo-b"},
	}}}
	worktrees, err := provisionStartedAgentWorktrees(plan, []*domain.Agent{{
		Name:     "multi",
		RoleName: "multi",
	}})
	if err != nil {
		t.Fatalf("provisionStartedAgentWorktrees() error = %v", err)
	}
	if len(worktrees) != 0 {
		t.Fatalf("worktrees = %+v, want none for multi-repo agent", worktrees)
	}
}

func withDefsStore(t *testing.T, st store.Store, workspace string) {
	t.Helper()
	old := defsWithActiveWorkspace
	defsWithActiveWorkspace = func(fn func(context.Context, *bootstrap.StoreHandle, string) error) error {
		return fn(context.Background(), &bootstrap.StoreHandle{Store: st}, workspace)
	}
	t.Cleanup(func() { defsWithActiveWorkspace = old })
}

func withDefsGlobals(t *testing.T, fn func()) {
	t.Helper()
	oldDefsDir, oldDefsJSON, oldDefsFromWorkspace := defsDir, defsJSON, defsFromWorkspace
	oldDefsExportForce, oldDefsExportState, oldDefsApplyStart := defsExportForce, defsExportState, defsApplyStart
	oldDefsWriteJSON := defsWriteJSON
	t.Cleanup(func() {
		defsDir, defsJSON, defsFromWorkspace = oldDefsDir, oldDefsJSON, oldDefsFromWorkspace
		defsExportForce, defsExportState, defsApplyStart = oldDefsExportForce, oldDefsExportState, oldDefsApplyStart
		defsWriteJSON = oldDefsWriteJSON
	})
	fn()
}

func initDefsGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runDefsGit(t, dir, "init")
	runDefsGit(t, dir, "checkout", "-B", "main")
	runDefsGit(t, dir, "config", "user.name", "Test User")
	runDefsGit(t, dir, "config", "user.email", "test@example.test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test repo\n"), 0o644); err != nil {
		t.Fatalf("write repo seed: %v", err)
	}
	runDefsGit(t, dir, "add", "README.md")
	runDefsGit(t, dir, "commit", "-m", "seed")
}

func runDefsGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func writeDefsFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
