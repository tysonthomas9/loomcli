package cli

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/testutil"
)

// resolveWorkspaceName (used by AcquireLock) must read only the bootstrap
// state cache: FleetDB is never opened, so an unrelated workspace failing to
// load cannot affect lock acquisition.
func TestResolveWorkspaceName_UsesStateCacheOnly(t *testing.T) {
	testutil.ClearLoomEnv(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Cleanup(cfgpkg.TestingSetConfigStoreOpener(func(context.Context) (store.Store, func(), error) {
		t.Errorf("FleetDB store opened; resolveWorkspaceName must use only the state cache")
		return nil, nil, errors.New("fleet-db must not be opened")
	}))

	root := t.TempDir()
	activeRoot := filepath.Join(root, "active")
	otherRoot := filepath.Join(root, "other")
	externalRepo := filepath.Join(root, "elsewhere", "ext-repo")
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.Workspaces["ACTIVE"] = bootstrap.WorkspaceLocalState{
			Path: activeRoot,
			Repos: map[string]string{
				"repo1":    "repo1", // relative to the workspace root
				"ext-repo": externalRepo,
			},
		}
		sc.Workspaces["OTHER"] = bootstrap.WorkspaceLocalState{Path: otherRoot}
		return nil
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{"workspace root", activeRoot, "ACTIVE"},
		{"under workspace root", filepath.Join(activeRoot, "worktrees", "repo1", "agent1"), "ACTIVE"},
		{"relative repo under root", filepath.Join(activeRoot, "repo1", "pkg"), "ACTIVE"},
		{"absolute repo outside root", externalRepo, "ACTIVE"},
		{"under absolute repo outside root", filepath.Join(externalRepo, "sub", "dir"), "ACTIVE"},
		{"other workspace", filepath.Join(otherRoot, "x"), "OTHER"},
		{"sibling prefix is not a match", activeRoot + "-sibling", ""},
		{"unrelated path", filepath.Join(root, "unrelated"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveWorkspaceName(tt.path); got != tt.want {
				t.Fatalf("resolveWorkspaceName(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestResolveWorkspaceName_NoStateCache(t *testing.T) {
	testutil.ClearLoomEnv(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Cleanup(cfgpkg.TestingSetConfigStoreOpener(func(context.Context) (store.Store, func(), error) {
		t.Errorf("FleetDB store opened; resolveWorkspaceName must use only the state cache")
		return nil, nil, errors.New("fleet-db must not be opened")
	}))

	if got := resolveWorkspaceName(t.TempDir()); got != "" {
		t.Fatalf("resolveWorkspaceName() = %q, want empty with no state cache", got)
	}
}
