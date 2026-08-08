package serveadapter

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
)

func TestWorkspaceDeleteCleanupFnClearsLocalState(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.LastWorkspace = "ALPHA"
		sc.Workspaces["ALPHA"] = bootstrap.WorkspaceLocalState{Path: "/tmp/alpha"}
		return nil
	}); err != nil {
		t.Fatalf("seed state cache: %v", err)
	}

	cleanup := BuildWorkspaceDeleteCleanupFn()
	if err := cleanup("ALPHA"); err != nil {
		t.Fatalf("cleanup workspace: %v", err)
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
