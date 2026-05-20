package svcimpl

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestAgentServiceLocalWorktreeValidationBranches(t *testing.T) {
	ctx := context.Background()
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "REMOTE", Name: "Remote"}); err != nil {
		t.Fatalf("create remote workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "REMOTE", Name: "task"}); err != nil {
		t.Fatalf("create remote role: %v", err)
	}
	svc := NewAgentService(&fakeGitOps{}, nil, nil, st)
	created, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey:     "REMOTE",
		Name:             "remote-agent",
		RoleName:         "task",
		DesiredState:     domain.AgentDesiredStopped,
		FallbackBackends: []string{"codex"},
	})
	if err != nil {
		t.Fatalf("CreateAgent remote workspace: %v", err)
	}
	if created.Name != "remote-agent" || len(created.FallbackBackends) != 1 {
		t.Fatalf("created remote agent = %+v", created)
	}

	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "LOCAL", Name: "Local"}); err != nil {
		t.Fatalf("create local workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "LOCAL", Name: "task"}); err != nil {
		t.Fatalf("create local role: %v", err)
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"LOCAL": {Path: t.TempDir()},
		},
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}
	_, err = svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "LOCAL",
		Name:         "no-repos",
		RoleName:     "task",
	})
	if err == nil || !strings.Contains(err.Error(), "no repos") {
		t.Fatalf("CreateAgent no repos err = %v", err)
	}
	if _, getErr := st.Agents().Get(ctx, "LOCAL", "no-repos"); !errors.Is(getErr, domain.ErrNotFound) {
		t.Fatalf("failed CreateAgent did not roll back agent: %v", getErr)
	}

	impl := svc.(*agentServiceImpl)
	if err := impl.ensureLocalAgentWorktrees(ctx, domain.Agent{WorkspaceKey: "LOCAL", Name: "lead-1", RoleName: "lead"}); err != nil {
		t.Fatalf("lead worktree ensure should be no-op: %v", err)
	}
	if got := normalizeFirstClassAgentRole(" Orchestrator "); got != "orchestrator" {
		t.Fatalf("normalize orchestrator = %q", got)
	}
	if isLeadAgentRole("task") {
		t.Fatal("task should not be classified as lead role")
	}
}

func TestAgentServiceEnsureLocalAgentWorktreesCreatesGitWorktree(t *testing.T) {
	ctx := context.Background()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	workspaceDir := t.TempDir()
	repoDir := filepath.Join(workspaceDir, "api")
	initAgentServiceGitRepo(t, repoDir)

	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "LOCAL", Name: "Local"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "LOCAL", Name: "task"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "LOCAL", Name: "api", Groups: []string{"backend"}}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"LOCAL": {
				Path:  workspaceDir,
				Repos: map[string]string{"api": repoDir},
			},
		},
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	svc := NewAgentService(&fakeGitOps{}, nil, nil, st)
	created, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "LOCAL",
		Name:         "worker-a",
		RoleName:     "task",
		RepoGroups:   []string{"backend"},
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if created.Name != "worker-a" {
		t.Fatalf("created agent = %+v", created)
	}
	want := filepath.Join(workspaceDir, "worktrees", "api", "worker-a")
	if _, err := os.Stat(filepath.Join(want, ".git")); err != nil {
		t.Fatalf("worktree .git missing: %v", err)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if got := sc.Workspaces["LOCAL"].Agents["worker-a"].Worktree; got != want {
		t.Fatalf("remembered worktree = %q, want %q", got, want)
	}
}

func initAgentServiceGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	agentServiceGit(t, dir, "init")
	agentServiceGit(t, dir, "config", "user.name", "Agent Service Test")
	agentServiceGit(t, dir, "config", "user.email", "agent-service@example.test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	agentServiceGit(t, dir, "add", "README.md")
	agentServiceGit(t, dir, "commit", "-m", "init")
}

func agentServiceGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // tests pass fixed git arguments.
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}
