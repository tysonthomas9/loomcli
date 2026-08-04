package opsimpl

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestValidateWorkspaceRoot(t *testing.T) {
	dir := t.TempDir()

	got, err := validateWorkspaceRoot("WS1", dir)
	if err != nil {
		t.Fatalf("valid dir: unexpected error: %v", err)
	}
	if got != dir {
		t.Fatalf("got %q, want %q", got, dir)
	}

	if _, err := validateWorkspaceRoot("WS1", ""); err == nil {
		t.Error("empty path: expected error, got nil")
	}
	if _, err := validateWorkspaceRoot("WS1", filepath.Join(dir, "missing")); err == nil {
		t.Error("missing dir: expected error, got nil")
	}

	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := validateWorkspaceRoot("WS1", file); err == nil {
		t.Error("file (not dir): expected error, got nil")
	}
}

func TestResolveWorkspaceRoot_StoreBacked(t *testing.T) {
	ctx := context.Background()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	wsRoot := t.TempDir()

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "One"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.LastWorkspace = "WS1"
		sc.Workspaces["WS1"] = bootstrap.WorkspaceLocalState{Path: wsRoot}
		return nil
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	g := NewGitOps().WithStore(st)
	got, err := g.ResolveWorkspaceRoot("WS1")
	if err != nil {
		t.Fatalf("ResolveWorkspaceRoot: %v", err)
	}
	if got != wsRoot {
		t.Fatalf("got %q, want %q", got, wsRoot)
	}
}

func TestResolveWorkspaceRoot_NotCheckedOut(t *testing.T) {
	ctx := context.Background()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "One"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	// No state-cache entry → workspace has no local path on this machine.
	g := NewGitOps().WithStore(st)
	if _, err := g.ResolveWorkspaceRoot("WS1"); err == nil {
		t.Fatal("expected error when workspace has no local path, got nil")
	}
}

func TestResolveWorkspaceRoot_EmptyID(t *testing.T) {
	if _, err := NewGitOps().ResolveWorkspaceRoot(""); err == nil {
		t.Fatal("expected error for empty workspace id, got nil")
	}
}
