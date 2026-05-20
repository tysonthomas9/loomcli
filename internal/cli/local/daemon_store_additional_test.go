package local

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestWorkspaceHasReposForLocalDaemon(t *testing.T) {
	requireLocalFleetDB(t)
	dataDir := t.TempDir()
	t.Setenv(bootstrap.EnvFleetDBActor, "local-daemon-test")
	handle, err := bootstrap.OpenStore(context.Background(), dataDir, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = handle.Close() }()
	if _, err := handle.Store.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	hasRepos, err := workspaceHasReposForLocalDaemon(dataDir, "WS")
	if err != nil {
		t.Fatalf("workspaceHasReposForLocalDaemon empty: %v", err)
	}
	if hasRepos {
		t.Fatal("empty workspace reported repos")
	}

	if _, err := handle.Store.Repos().Create(context.Background(), store.RepoCreate{WorkspaceKey: "WS", Name: "api", RemoteURL: "/tmp/api"}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	hasRepos, err = workspaceHasReposForLocalDaemon(dataDir, "WS")
	if err != nil {
		t.Fatalf("workspaceHasReposForLocalDaemon repo: %v", err)
	}
	if !hasRepos {
		t.Fatal("workspace with repo reported no repos")
	}
}

func requireLocalFleetDB(t *testing.T) {
	t.Helper()
	if bin := os.Getenv(bootstrap.EnvFleetDBBin); bin != "" {
		return
	}
	if _, err := exec.LookPath("fleet-db"); err != nil {
		t.Skip("fleet-db binary not available")
	}
}
