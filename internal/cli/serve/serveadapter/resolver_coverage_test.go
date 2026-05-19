package serveadapter

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestBuildWorkspaceIDResolverFnHandlesDirectNameAndMisses(t *testing.T) {
	if BuildWorkspaceIDResolverFn(nil) != nil {
		t.Fatalf("nil store returned resolver")
	}

	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "One"}); err != nil {
		t.Fatalf("create workspace 1: %v", err)
	}
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS2", Name: "Friendly Name"}); err != nil {
		t.Fatalf("create workspace 2: %v", err)
	}

	resolve := BuildWorkspaceIDResolverFn(st)
	if got, err := resolve("WS1"); err != nil || got != "WS1" {
		t.Fatalf("resolve direct = %q, %v; want WS1", got, err)
	}
	if got, err := resolve("Friendly Name"); err != nil || got != "WS2" {
		t.Fatalf("resolve by name = %q, %v; want WS2", got, err)
	}
	if got, err := resolve("missing"); err == nil || got != "" || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("resolve missing = %q, %v; want not found", got, err)
	}
}

func TestResolveInitialAndDefaultWorkspaceFns(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "One"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	if got := ResolveInitialWorkspaceID(nil); got != "" {
		t.Fatalf("nil store initial = %q, want empty", got)
	}
	if got := ResolveInitialWorkspaceID(st); got != "" {
		t.Fatalf("no env initial = %q, want empty", got)
	}
	t.Setenv(bootstrap.EnvWorkspace, "WS1")
	if got := ResolveInitialWorkspaceID(st); got != "WS1" {
		t.Fatalf("env initial = %q, want WS1", got)
	}

	setDefault := BuildSetDefaultWorkspaceFn(st)
	if setDefault == nil {
		t.Fatalf("BuildSetDefaultWorkspaceFn returned nil for store")
	}
	if err := setDefault("WS1"); err != nil {
		t.Fatalf("set default: %v", err)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("LoadStateCache: %v", err)
	}
	if sc.LastWorkspace != "WS1" {
		t.Fatalf("LastWorkspace = %q, want WS1", sc.LastWorkspace)
	}
	if err := BuildClearDefaultWorkspaceFn()(); err != nil {
		t.Fatalf("clear default: %v", err)
	}
	sc, err = bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("LoadStateCache after clear: %v", err)
	}
	if sc.LastWorkspace != "" {
		t.Fatalf("LastWorkspace after clear = %q, want empty", sc.LastWorkspace)
	}
	if err := setDefault("missing"); err == nil {
		t.Fatalf("set default missing succeeded")
	}
	if BuildSetDefaultWorkspaceFn(nil) != nil {
		t.Fatalf("nil store returned set-default func")
	}
}
