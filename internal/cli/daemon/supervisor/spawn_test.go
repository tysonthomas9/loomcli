package supervisor

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

func TestBuildCommandOmitsGitHubCredentials(t *testing.T) {
	for name, value := range map[string]string{
		"GITHUB_TOKEN": "github-fixture", "GH_TOKEN": "gh-fixture",
		"GITHUB_TOKEN_FILE": "/tmp/github-fixture", "LOOM_PR_GIT_PASSWORD": "password-fixture",
	} {
		t.Setenv(name, value)
	}
	s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig {
		return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}}
	}, ProjectDir: t.TempDir()}
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "worker", Role: "task"}, WorktreePath: t.TempDir()}
	cmd, err := s.buildCommand(ap)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range cmd.Env {
		for _, name := range []string{"GITHUB_TOKEN=", "GH_TOKEN=", "GITHUB_TOKEN_FILE=", "LOOM_PR_GIT_PASSWORD="} {
			if strings.HasPrefix(entry, name) {
				t.Fatalf("agent env contains %s", name)
			}
		}
	}
	for _, arg := range cmd.Args {
		if strings.Contains(arg, "fixture") {
			t.Fatalf("agent argv contains credential: %q", arg)
		}
	}
}

func TestBuildCommandExportsResolvedRepoForBoundAndUnboundAgents(t *testing.T) {
	for _, entryRepo := range []string{"source-repo", ""} {
		t.Run("entry-repo="+entryRepo, func(t *testing.T) {
			s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig {
				return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}}
			}, ProjectDir: t.TempDir()}
			ap := &AgentProcess{
				Entry:        cfgpkg.AgentEntry{Worktree: "agent", Role: "task", Repo: entryRepo},
				WorktreePath: t.TempDir(), WorktreeRepo: "source-repo",
			}
			cmd, err := s.buildCommand(ap)
			if err != nil {
				t.Fatal(err)
			}
			var resolved, bound string
			for _, item := range cmd.Env {
				if strings.HasPrefix(item, "LOOM_WORKTREE_REPO=") {
					resolved = strings.TrimPrefix(item, "LOOM_WORKTREE_REPO=")
				}
				if strings.HasPrefix(item, "LOOM_AGENT_REPO=") {
					bound = strings.TrimPrefix(item, "LOOM_AGENT_REPO=")
				}
			}
			if resolved != "source-repo" || bound != entryRepo {
				t.Fatalf("repo env: worktree=%q bound=%q, want source-repo/%q", resolved, bound, entryRepo)
			}
		})
	}
}

func TestAppendRoleEnv_MaxBudgetUSD(t *testing.T) {
	t.Parallel()

	t.Run("set when non-nil", func(t *testing.T) {
		t.Parallel()
		budget := 8.50
		ap := &AgentProcess{
			RoleConfig: cfgpkg.RoleConfig{
				MaxBudgetUSD: &budget,
			},
		}

		env := appendRoleEnv(nil, ap)

		found := false
		for _, entry := range env {
			if strings.HasPrefix(entry, "LOOM_MAX_BUDGET_USD=") {
				found = true
				want := fmt.Sprintf("LOOM_MAX_BUDGET_USD=%.2f", budget)
				if entry != want {
					t.Errorf("env entry = %q, want %q", entry, want)
				}
				break
			}
		}
		if !found {
			t.Errorf("expected LOOM_MAX_BUDGET_USD in env, got %v", env)
		}
	})

	t.Run("absent when nil", func(t *testing.T) {
		t.Parallel()
		ap := &AgentProcess{
			RoleConfig: cfgpkg.RoleConfig{
				MaxBudgetUSD: nil,
			},
		}

		env := appendRoleEnv(nil, ap)

		for _, entry := range env {
			if strings.HasPrefix(entry, "LOOM_MAX_BUDGET_USD=") {
				t.Errorf("expected LOOM_MAX_BUDGET_USD to be absent, but found %q", entry)
			}
		}
	})

	t.Run("zero value is formatted", func(t *testing.T) {
		t.Parallel()
		budget := 0.0
		ap := &AgentProcess{
			RoleConfig: cfgpkg.RoleConfig{
				MaxBudgetUSD: &budget,
			},
		}

		env := appendRoleEnv(nil, ap)

		found := false
		for _, entry := range env {
			if strings.HasPrefix(entry, "LOOM_MAX_BUDGET_USD=") {
				found = true
				if entry != "LOOM_MAX_BUDGET_USD=0.00" {
					t.Errorf("env entry = %q, want %q", entry, "LOOM_MAX_BUDGET_USD=0.00")
				}
				break
			}
		}
		if !found {
			t.Errorf("expected LOOM_MAX_BUDGET_USD=0.00 in env, got %v", env)
		}
	})
}

func TestAppendRoleEnv_Effort(t *testing.T) {
	t.Parallel()

	ap := &AgentProcess{
		RoleConfig: cfgpkg.RoleConfig{
			Effort: "max",
		},
	}

	env := appendRoleEnv(nil, ap)

	want := map[string]bool{
		"LOOM_AGENT_EFFORT=max":  false,
		"LOOM_CLAUDE_EFFORT=max": false,
	}
	for _, entry := range env {
		if _, ok := want[entry]; ok {
			want[entry] = true
		}
	}
	for entry, found := range want {
		if !found {
			t.Fatalf("expected %s in env, got %v", entry, env)
		}
	}
}

func TestAppendSessionEnvConcurrentLeaseAccess(t *testing.T) {
	ap := &AgentProcess{}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			ap.Mu.Lock()
			ap.AgentLeaseID = fmt.Sprintf("lease-%d", i)
			ap.AgentLeaseToken = fmt.Sprintf("token-%d", i)
			ap.Mu.Unlock()
			runtime.Gosched()
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			_ = appendSessionEnv(nil, ap)
			runtime.Gosched()
		}
	}()

	wg.Wait()
}
