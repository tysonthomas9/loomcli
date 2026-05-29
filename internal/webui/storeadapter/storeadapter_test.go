package storeadapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
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

func TestEnsureWorkspacePathCreatesDefaultLocalState(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	got, err := EnsureWorkspacePath("ACME")
	if err != nil {
		t.Fatalf("EnsureWorkspacePath returned error: %v", err)
	}
	want := filepath.Join(loomDir, "workspaces", "ACME")
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if info, err := os.Stat(got); err != nil || !info.IsDir() {
		t.Fatalf("workspace path was not created as directory: info=%v err=%v", info, err)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("LoadStateCache returned error: %v", err)
	}
	if sc.Workspaces["ACME"].Path != want {
		t.Fatalf("cached path = %q, want %q", sc.Workspaces["ACME"].Path, want)
	}
}

func TestListWorkspacePathsSelfHealsMissingLocalState(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ACME", Name: "ACME"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	got, err := ListWorkspacePaths(ctx, st)
	if err != nil {
		t.Fatalf("ListWorkspacePaths returned error: %v", err)
	}
	want := filepath.Join(loomDir, "workspaces", "ACME")
	if got["ACME"] != want {
		t.Fatalf("workspace path = %q, want %q", got["ACME"], want)
	}
}
