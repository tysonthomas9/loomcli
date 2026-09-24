package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/testutil"
)

var errScopedRateLimited = errors.New("fleet-db: HTTP 429 Too Many Requests")

// fakeScopedLoader replaces loadScopedWorkspace with an in-memory lookup that
// records requested names. Keys are canonical (upper-case); lookups match the
// name exactly or upper-cased, like config.LoadWorkspaceConfig.
type fakeScopedLoader struct {
	workspaces map[string]cfgpkg.WorkspaceConfig
	errs       map[string]error
	calls      []string
}

func installFakeScopedLoader(t *testing.T, f *fakeScopedLoader) {
	t.Helper()
	old := loadScopedWorkspace
	loadScopedWorkspace = func(name string) (string, *cfgpkg.WorkspaceConfig, error) {
		f.calls = append(f.calls, name)
		for _, key := range []string{name, strings.ToUpper(name)} {
			if err := f.errs[key]; err != nil {
				return "", nil, fmt.Errorf("load %s: %w", key, err)
			}
			if ws, ok := f.workspaces[key]; ok {
				return key, &ws, nil
			}
		}
		return "", nil, fmt.Errorf("workspace %q: %w", name, cfgpkg.ErrWorkspaceNotFound)
	}
	t.Cleanup(func() { loadScopedWorkspace = old })
}

// isolateActiveResolver clears leaked LOOM_* env, points state at a temp dir,
// and resets resolver/runtime-dir/config caches before and after the test.
func isolateActiveResolver(t *testing.T) {
	t.Helper()
	testutil.ClearLoomEnv(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	cfgpkg.InvalidateConfigCache()
	ResetWorkspaceRuntimeDirCache()
	old := TestingResetDefaultResolver()
	t.Cleanup(func() {
		TestingSetDefaultResolver(old)
		cfgpkg.InvalidateConfigCache()
		ResetWorkspaceRuntimeDirCache()
	})
}

func TestNewActiveWorkspaceResolver_FromEnv(t *testing.T) {
	isolateActiveResolver(t)
	f := &fakeScopedLoader{
		workspaces: map[string]cfgpkg.WorkspaceConfig{
			"ACTIVE": {ID: "ACTIVE", Path: "/tmp/active", Repos: []cfgpkg.RepoConfig{{Name: "api"}}},
		},
		// An unrelated workspace failing must never be touched.
		errs: map[string]error{"OTHER": errScopedRateLimited},
	}
	installFakeScopedLoader(t, f)
	t.Setenv(bootstrap.EnvWorkspace, "active")

	r, err := NewActiveWorkspaceResolver()
	if err != nil {
		t.Fatalf("NewActiveWorkspaceResolver() error = %v", err)
	}
	if r.Workspace != "ACTIVE" || r.Mode != ModeWorkspace {
		t.Fatalf("resolver workspace/mode = %q/%v, want ACTIVE/workspace", r.Workspace, r.Mode)
	}
	if len(r.Config.Workspaces) != 1 {
		t.Fatalf("resolver workspaces = %v, want only ACTIVE", r.Config.Workspaces)
	}
	if got := r.GetWorktreesDir(); got != "/tmp/active" {
		t.Fatalf("GetWorktreesDir() = %q, want /tmp/active", got)
	}
	if len(f.calls) != 1 || f.calls[0] != "active" {
		t.Fatalf("loader calls = %v, want [active]", f.calls)
	}
}

func TestNewActiveWorkspaceResolver_FromCWDStateCache(t *testing.T) {
	isolateActiveResolver(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	activeRoot := filepath.Join(root, "active")
	otherRoot := filepath.Join(root, "other")
	sub := filepath.Join(activeRoot, "worktree", "pkg")
	for _, dir := range []string{sub, otherRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.Workspaces["ACTIVE"] = bootstrap.WorkspaceLocalState{Path: activeRoot}
		sc.Workspaces["OTHER"] = bootstrap.WorkspaceLocalState{Path: otherRoot}
		return nil
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}
	f := &fakeScopedLoader{
		workspaces: map[string]cfgpkg.WorkspaceConfig{"ACTIVE": {ID: "ACTIVE", Path: activeRoot}},
		errs:       map[string]error{"OTHER": errScopedRateLimited},
	}
	installFakeScopedLoader(t, f)
	t.Chdir(sub)

	r, err := NewActiveWorkspaceResolver()
	if err != nil {
		t.Fatalf("NewActiveWorkspaceResolver() error = %v", err)
	}
	if r.Workspace != "ACTIVE" {
		t.Fatalf("resolver workspace = %q, want ACTIVE", r.Workspace)
	}
	if len(f.calls) != 1 || f.calls[0] != "ACTIVE" {
		t.Fatalf("loader calls = %v, want [ACTIVE]", f.calls)
	}

	if got := GetWorkspaceRuntimeDir(); got != activeRoot {
		t.Fatalf("GetWorkspaceRuntimeDir() = %q, want %q", got, activeRoot)
	}
}

func TestNewActiveWorkspaceResolver_NoActiveWorkspace(t *testing.T) {
	isolateActiveResolver(t)
	f := &fakeScopedLoader{}
	installFakeScopedLoader(t, f)
	t.Chdir(t.TempDir())

	if _, err := NewActiveWorkspaceResolver(); !errors.Is(err, bootstrap.ErrNoActiveWorkspace) {
		t.Fatalf("error = %v, want ErrNoActiveWorkspace", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("loader calls = %v, want none", f.calls)
	}
	if got := GetWorkspaceRuntimeDir(); got != "." {
		t.Fatalf("GetWorkspaceRuntimeDir() = %q, want .", got)
	}
}

func TestNewActiveWorkspaceResolver_ActiveLoadErrorPropagates(t *testing.T) {
	isolateActiveResolver(t)
	installFakeScopedLoader(t, &fakeScopedLoader{errs: map[string]error{"ACTIVE": errScopedRateLimited}})
	t.Setenv(bootstrap.EnvWorkspace, "ACTIVE")

	r, err := NewActiveWorkspaceResolver()
	if !errors.Is(err, errScopedRateLimited) {
		t.Fatalf("error = %v, want wrapped 429", err)
	}
	if r != nil {
		t.Fatalf("resolver = %+v, want nil", r)
	}

	// GetDefaultResolver degrades to an empty resolver instead of failing.
	def := GetDefaultResolver()
	if def == nil || def.Config == nil || len(def.Config.Workspaces) != 0 || def.Workspace != "" {
		t.Fatalf("GetDefaultResolver() = %+v, want empty fallback resolver", def)
	}
	if got := GetWorkspaceRuntimeDir(); got != "." {
		t.Fatalf("GetWorkspaceRuntimeDir() = %q, want .", got)
	}
}

func TestNewActiveWorkspaceResolver_MissingWorkspace(t *testing.T) {
	isolateActiveResolver(t)
	installFakeScopedLoader(t, &fakeScopedLoader{})
	t.Setenv(bootstrap.EnvWorkspace, "GHOST")

	_, err := NewActiveWorkspaceResolver()
	if err == nil || !strings.Contains(err.Error(), `workspace "GHOST" not found`) {
		t.Fatalf("error = %v, want workspace not found", err)
	}
}

func TestGetDefaultResolver_UsesScopedActiveWorkspace(t *testing.T) {
	isolateActiveResolver(t)
	f := &fakeScopedLoader{
		workspaces: map[string]cfgpkg.WorkspaceConfig{"ACTIVE": {ID: "ACTIVE", Path: "/tmp/active"}},
		errs:       map[string]error{"OTHER": errScopedRateLimited},
	}
	installFakeScopedLoader(t, f)
	t.Setenv(bootstrap.EnvWorkspace, "ACTIVE")

	r := GetDefaultResolver()
	if r.Workspace != "ACTIVE" || r.GetWorktreesDir() != "/tmp/active" {
		t.Fatalf("GetDefaultResolver() = %+v, want ACTIVE at /tmp/active", r)
	}
	if again := GetDefaultResolver(); again != r {
		t.Fatal("GetDefaultResolver() did not reuse cached resolver")
	}
	if len(f.calls) != 1 {
		t.Fatalf("loader calls = %v, want exactly one", f.calls)
	}
}

func TestScopedResolver_SetWorkspace(t *testing.T) {
	isolateActiveResolver(t)
	f := &fakeScopedLoader{
		workspaces: map[string]cfgpkg.WorkspaceConfig{
			"ACTIVE": {ID: "ACTIVE", Path: "/tmp/active"},
			"SECOND": {ID: "SECOND", Path: "/tmp/second"},
		},
		errs: map[string]error{"BROKEN": errScopedRateLimited},
	}
	installFakeScopedLoader(t, f)
	t.Setenv(bootstrap.EnvWorkspace, "ACTIVE")

	r, err := NewActiveWorkspaceResolver()
	if err != nil {
		t.Fatalf("NewActiveWorkspaceResolver() error = %v", err)
	}

	t.Run("lazy loads another workspace", func(t *testing.T) {
		if err := r.SetWorkspace("second"); err != nil {
			t.Fatalf("SetWorkspace(second) error = %v", err)
		}
		if r.Workspace != "SECOND" || r.GetWorktreesDir() != "/tmp/second" {
			t.Fatalf("resolver = %q at %q, want SECOND at /tmp/second", r.Workspace, r.GetWorktreesDir())
		}
		if _, ok := r.Config.Workspaces["ACTIVE"]; !ok {
			t.Fatal("previously loaded ACTIVE workspace was dropped")
		}
	})

	t.Run("already loaded workspace does not reload", func(t *testing.T) {
		before := len(f.calls)
		if err := r.SetWorkspace("ACTIVE"); err != nil {
			t.Fatalf("SetWorkspace(ACTIVE) error = %v", err)
		}
		if r.Workspace != "ACTIVE" {
			t.Fatalf("resolver workspace = %q, want ACTIVE", r.Workspace)
		}
		if len(f.calls) != before {
			t.Fatalf("loader calls = %v, want no new calls", f.calls)
		}
	})

	t.Run("not found", func(t *testing.T) {
		err := r.SetWorkspace("ghost")
		if err == nil || !strings.Contains(err.Error(), `workspace "ghost" not found in config`) {
			t.Fatalf("SetWorkspace(ghost) error = %v, want not found", err)
		}
		if r.Workspace != "ACTIVE" {
			t.Fatalf("resolver workspace changed to %q on failure", r.Workspace)
		}
	})

	t.Run("load error propagates", func(t *testing.T) {
		err := r.SetWorkspace("BROKEN")
		if !errors.Is(err, errScopedRateLimited) {
			t.Fatalf("SetWorkspace(BROKEN) error = %v, want wrapped 429", err)
		}
		if r.Workspace != "ACTIVE" {
			t.Fatalf("resolver workspace changed to %q on failure", r.Workspace)
		}
	})
}

func TestUnscopedResolver_SetWorkspaceDoesNotLazyLoad(t *testing.T) {
	isolateActiveResolver(t)
	f := &fakeScopedLoader{workspaces: map[string]cfgpkg.WorkspaceConfig{"SECOND": {ID: "SECOND"}}}
	installFakeScopedLoader(t, f)

	r := &Resolver{Mode: ModeWorkspace, Config: &cfgpkg.LoomConfig{Workspaces: map[string]cfgpkg.WorkspaceConfig{"ACTIVE": {}}}}
	if err := r.SetWorkspace("SECOND"); err == nil {
		t.Fatal("SetWorkspace(SECOND) on unscoped resolver succeeded, want not found")
	}
	if len(f.calls) != 0 {
		t.Fatalf("loader calls = %v, want none for unscoped resolver", f.calls)
	}
}
