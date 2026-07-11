package local

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestLocalDaemonRunnableWorkspaceStartsForRepoBackedWorkspaceWithoutRunnableAgents(t *testing.T) {
	dataDir := stubLocalDaemonWorkspace(t, true)

	workspaceKey, runnable, err := localDaemonRunnableWorkspace(context.Background(), dataDir, "http://127.0.0.1:1")
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

	workspaceKey, runnable, err := localDaemonRunnableWorkspace(context.Background(), dataDir, "http://127.0.0.1:1")
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
	localDaemonWorkspaceHasRepos = func(_ context.Context, runtimeURL, workspaceKey string) (bool, error) {
		if runtimeURL != "http://127.0.0.1:1" {
			t.Fatalf("runtimeURL = %q, want http://127.0.0.1:1", runtimeURL)
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

func TestWorkspaceHasReposForLocalDaemonUsesRuntimeAPI(t *testing.T) {
	var observedPath atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observedPath.Store(r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"repos":[{"name":"app"}]}`))
	}))
	t.Cleanup(server.Close)

	hasRepos, err := workspaceHasReposForLocalDaemon(context.Background(), server.URL, "WS")
	if err != nil {
		t.Fatalf("workspaceHasReposForLocalDaemon returned error: %v", err)
	}
	if !hasRepos {
		t.Fatal("hasRepos = false, want true")
	}
	if got, _ := observedPath.Load().(string); got != "/api/workspaces/WS/repos" {
		t.Fatalf("server saw path = %q, want /api/workspaces/WS/repos", got)
	}
}
