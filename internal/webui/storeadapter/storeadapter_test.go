package storeadapter

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestBuildActiveWorkspaceData_FallsBackToFirstWorkspace(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE", "")

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BETA", Name: "Beta"}); err != nil {
		t.Fatalf("create beta: %v", err)
	}
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha"}); err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "ALPHA", Name: "api"}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	got, err := BuildActiveWorkspaceData(ctx, st)
	if err != nil {
		t.Fatalf("BuildActiveWorkspaceData returned error: %v", err)
	}
	if got == nil {
		t.Fatal("BuildActiveWorkspaceData returned nil")
	}
	if got.ID != "ALPHA" {
		t.Fatalf("active workspace ID = %q, want ALPHA", got.ID)
	}
	if len(got.Repos) != 1 || got.Repos[0].Name != "api" {
		t.Fatalf("repos = %+v, want api repo", got.Repos)
	}
}

func TestBuildActiveWorkspaceData_ExposesRepoRemoteURL(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE", "ALPHA")

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey: "ALPHA",
		Name:         "api",
		Remote:       "origin",
		RemoteURL:    "git@github.com:octocat/api.git",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	got, err := BuildActiveWorkspaceData(ctx, st)
	if err != nil {
		t.Fatalf("BuildActiveWorkspaceData returned error: %v", err)
	}
	if len(got.Repos) != 1 {
		t.Fatalf("repos = %+v, want one repo", got.Repos)
	}
	if got.Repos[0].Remote != "origin" {
		t.Fatalf("remote = %q, want origin", got.Repos[0].Remote)
	}
	if got.Repos[0].RemoteURL != "git@github.com:octocat/api.git" {
		t.Fatalf("remote_url = %q, want GitHub clone URL", got.Repos[0].RemoteURL)
	}
}
