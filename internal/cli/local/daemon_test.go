package local

import (
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestLocalDaemonRunnableWorkspaceStartsForRepoBackedWorkspaceWithoutRunnableAgents(t *testing.T) {
	dataDir := stubLocalDaemonWorkspace(t, true)

	workspaceKey, runnable, err := localDaemonRunnableWorkspace(dataDir)
	if err != nil {
		t.Fatalf("localDaemonRunnableWorkspace returned error: %v", err)
	}
	if workspaceKey != "WS" {
		t.Fatalf("workspaceKey = %q, want WS", workspaceKey)
	}
	if !runnable {
		t.Fatal("runnable = false, want true for repo-backed workspace so epic runner commands can dispatch")
	}
}

func TestLocalDaemonRunnableWorkspaceStaysIdleWithoutReposOrRunnableAgents(t *testing.T) {
	dataDir := stubLocalDaemonWorkspace(t, false)

	workspaceKey, runnable, err := localDaemonRunnableWorkspace(dataDir)
	if err != nil {
		t.Fatalf("localDaemonRunnableWorkspace returned error: %v", err)
	}
	if workspaceKey != "WS" {
		t.Fatalf("workspaceKey = %q, want WS", workspaceKey)
	}
	if runnable {
		t.Fatal("runnable = true, want false without repos or runnable agents")
	}
}

func stubLocalDaemonWorkspace(t *testing.T, withRepo bool) string {
	t.Helper()

	dataDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dataDir)
	t.Setenv("LOOM_DESKTOP_DATA_DIR", dataDir)
	t.Setenv("LOOM_WORKSPACE", "WS")

	previousHasRepos := localDaemonWorkspaceHasRepos
	previousLoadConfig := localDaemonLoadConfig
	localDaemonWorkspaceHasRepos = func(gotDataDir, workspaceKey string) (bool, error) {
		if gotDataDir != dataDir {
			t.Fatalf("dataDir = %q, want %q", gotDataDir, dataDir)
		}
		if workspaceKey != "WS" {
			t.Fatalf("workspaceKey = %q, want WS", workspaceKey)
		}
		return withRepo, nil
	}
	localDaemonLoadConfig = func(gotDataDir, workspaceKey string) (*cfgpkg.DaemonConfig, error) {
		if gotDataDir != dataDir {
			t.Fatalf("config dataDir = %q, want %q", gotDataDir, dataDir)
		}
		if workspaceKey != "WS" {
			t.Fatalf("config workspaceKey = %q, want WS", workspaceKey)
		}
		return &cfgpkg.DaemonConfig{
			Agents: []cfgpkg.AgentEntry{{
				Worktree:     "nova",
				Role:         "lead",
				DesiredState: domain.AgentDesiredStopped,
			}},
		}, nil
	}
	t.Cleanup(func() {
		localDaemonWorkspaceHasRepos = previousHasRepos
		localDaemonLoadConfig = previousLoadConfig
	})

	return dataDir
}
