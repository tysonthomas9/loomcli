package serveadapter

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestWorkspaceDeleteFnDeletesStoreAndLocalState(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := bootstrap.WithStateLock(func() error {
		sc, err := bootstrap.LoadStateCache()
		if err != nil {
			return err
		}
		if sc.Workspaces == nil {
			sc.Workspaces = make(map[string]bootstrap.WorkspaceLocalState)
		}
		sc.LastWorkspace = "ALPHA"
		sc.Workspaces["ALPHA"] = bootstrap.WorkspaceLocalState{Path: "/tmp/alpha"}
		return bootstrap.SaveStateCache(sc)
	}); err != nil {
		t.Fatalf("seed state cache: %v", err)
	}

	deleteFn := BuildWorkspaceDeleteFn(st)
	if err := deleteFn("ALPHA"); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}
	if _, err := st.Workspaces().Get(ctx, "ALPHA"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("workspace still exists or unexpected error: %v", err)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if sc.LastWorkspace != "" {
		t.Fatalf("LastWorkspace = %q, want empty", sc.LastWorkspace)
	}
	if _, ok := sc.Workspaces["ALPHA"]; ok {
		t.Fatalf("local workspace state was not removed: %#v", sc.Workspaces["ALPHA"])
	}
}

func TestWorkspaceDeleteFnDoesNotClearStateWhenStoreDeleteFails(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	st := memstore.New()
	if err := bootstrap.WithStateLock(func() error {
		sc, err := bootstrap.LoadStateCache()
		if err != nil {
			return err
		}
		if sc.Workspaces == nil {
			sc.Workspaces = make(map[string]bootstrap.WorkspaceLocalState)
		}
		sc.LastWorkspace = "MISSING"
		sc.Workspaces["MISSING"] = bootstrap.WorkspaceLocalState{Path: "/tmp/missing"}
		return bootstrap.SaveStateCache(sc)
	}); err != nil {
		t.Fatalf("seed state cache: %v", err)
	}

	deleteFn := BuildWorkspaceDeleteFn(st)
	if err := deleteFn("MISSING"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete err = %v, want ErrNotFound", err)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if sc.LastWorkspace != "MISSING" {
		t.Fatalf("LastWorkspace = %q, want MISSING", sc.LastWorkspace)
	}
	if _, ok := sc.Workspaces["MISSING"]; !ok {
		t.Fatal("local workspace state was removed despite store delete failure")
	}
}
