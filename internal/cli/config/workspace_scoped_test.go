package config

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/testutil"
)

// errRateLimited mimics the FleetDB HTTP 429 error surfaced by the remote store.
var errRateLimited = errors.New("fleet-db: HTTP 429 Too Many Requests")

// faultyStore wraps a real store and injects failures for specific workspaces.
type faultyStore struct {
	store.Store
	failRepos      map[string]error // workspace key -> Repos().List error
	failDaemon     map[string]error // workspace key -> Daemon().Get error
	invalidGetKeys map[string]bool  // Workspaces().Get keys rejected as ErrInvalid
}

func (s *faultyStore) Repos() store.RepoStore {
	return &faultyRepoStore{RepoStore: s.Store.Repos(), fail: s.failRepos}
}

func (s *faultyStore) Daemon() store.DaemonProfileStore {
	return &faultyDaemonStore{DaemonProfileStore: s.Store.Daemon(), fail: s.failDaemon}
}

func (s *faultyStore) Workspaces() store.WorkspaceStore {
	return &faultyWorkspaceStore{WorkspaceStore: s.Store.Workspaces(), invalid: s.invalidGetKeys}
}

type faultyRepoStore struct {
	store.RepoStore
	fail map[string]error
}

func (r *faultyRepoStore) List(ctx context.Context, workspaceKey string) ([]*domain.Repo, error) {
	if err := r.fail[workspaceKey]; err != nil {
		return nil, fmt.Errorf("list repos %s: %w", workspaceKey, err)
	}
	return r.RepoStore.List(ctx, workspaceKey)
}

type faultyDaemonStore struct {
	store.DaemonProfileStore
	fail map[string]error
}

func (d *faultyDaemonStore) Get(ctx context.Context, workspaceKey string) (*domain.DaemonProfile, error) {
	if err := d.fail[workspaceKey]; err != nil {
		return nil, fmt.Errorf("get daemon %s: %w", workspaceKey, err)
	}
	return d.DaemonProfileStore.Get(ctx, workspaceKey)
}

type faultyWorkspaceStore struct {
	store.WorkspaceStore
	invalid map[string]bool
}

func (w *faultyWorkspaceStore) Get(ctx context.Context, key string) (*domain.Workspace, error) {
	if w.invalid[key] {
		return nil, fmt.Errorf("workspace key %q: %w", key, domain.ErrInvalid)
	}
	return w.WorkspaceStore.Get(ctx, key)
}

// seedScopedStore creates ACTIVE (one repo, with local paths) and OTHER (one
// repo) workspaces in a memstore and isolates config/state dirs.
func seedScopedStore(t *testing.T) *memstore.Store {
	t.Helper()
	testutil.ClearLoomEnv(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	InvalidateConfigCache()
	t.Cleanup(InvalidateConfigCache)

	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })

	for _, key := range []string{"ACTIVE", "OTHER"} {
		if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: key, Name: strings.ToLower(key)}); err != nil {
			t.Fatalf("create workspace %s: %v", key, err)
		}
		if _, err := st.Repos().Create(ctx, store.RepoCreate{
			WorkspaceKey:  key,
			Name:          strings.ToLower(key) + "-repo",
			DefaultBranch: "main",
			SourceRepoID:  strings.ToLower(key) + "-repo",
		}); err != nil {
			t.Fatalf("create repo for %s: %v", key, err)
		}
	}
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.Workspaces["ACTIVE"] = bootstrap.WorkspaceLocalState{
			Path:  "/tmp/active",
			Repos: map[string]string{"active-repo": "/tmp/active/active-repo"},
		}
		return nil
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}
	return st
}

// overrideOpenScopedStore routes the scoped loaders at st and counts opens.
func overrideOpenScopedStore(t *testing.T, st store.Store) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	old := openConfigStore
	openConfigStore = func(context.Context) (store.Store, func(), error) {
		calls.Add(1)
		return st, func() {}, nil
	}
	t.Cleanup(func() { openConfigStore = old })
	return &calls
}

func assertActiveWorkspace(t *testing.T, key string, ws *WorkspaceConfig) {
	t.Helper()
	if key != "ACTIVE" {
		t.Fatalf("key = %q, want ACTIVE", key)
	}
	if ws == nil {
		t.Fatal("workspace config is nil")
	}
	if ws.ID != "ACTIVE" || ws.Path != "/tmp/active" {
		t.Fatalf("workspace = %+v, want ID ACTIVE path /tmp/active", ws)
	}
	if len(ws.Repos) != 1 || ws.Repos[0].Name != "active-repo" || ws.Repos[0].Path != "/tmp/active/active-repo" {
		t.Fatalf("repos = %+v, want [active-repo at /tmp/active/active-repo]", ws.Repos)
	}
}

func TestLoadWorkspaceConfigFromStore_UnrelatedWorkspaceFailureIgnored(t *testing.T) {
	base := seedScopedStore(t)
	st := &faultyStore{
		Store:      base,
		failRepos:  map[string]error{"OTHER": errRateLimited},
		failDaemon: map[string]error{"OTHER": errRateLimited},
	}
	ctx := context.Background()

	key, ws, err := loadWorkspaceConfigFromStore(ctx, st, "ACTIVE")
	if err != nil {
		t.Fatalf("loadWorkspaceConfigFromStore() error = %v", err)
	}
	assertActiveWorkspace(t, key, ws)

	// The list-all projection still enumerates every workspace, so the
	// unrelated failure breaks it. This documents the preserved behavior.
	if _, err := loadConfigFromStore(ctx, st); !errors.Is(err, errRateLimited) {
		t.Fatalf("loadConfigFromStore() error = %v, want wrapped 429", err)
	}
}

func TestLoadWorkspaceConfigFromStore_ActiveRepoErrorPropagates(t *testing.T) {
	base := seedScopedStore(t)
	st := &faultyStore{Store: base, failRepos: map[string]error{"ACTIVE": errRateLimited}}

	_, ws, err := loadWorkspaceConfigFromStore(context.Background(), st, "ACTIVE")
	if !errors.Is(err, errRateLimited) {
		t.Fatalf("error = %v, want wrapped 429", err)
	}
	if errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("error = %v, must not be ErrWorkspaceNotFound", err)
	}
	if ws != nil {
		t.Fatalf("ws = %+v, want nil", ws)
	}
}

func TestLoadWorkspaceConfigFromStore_MissingWorkspace(t *testing.T) {
	st := seedScopedStore(t)

	_, ws, err := loadWorkspaceConfigFromStore(context.Background(), st, "MISSING")
	if !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("error = %v, want ErrWorkspaceNotFound", err)
	}
	if ws != nil {
		t.Fatalf("ws = %+v, want nil", ws)
	}
}

func TestLoadWorkspaceConfigFromStore_LowerCaseResolvesToUpperKey(t *testing.T) {
	base := seedScopedStore(t)
	ctx := context.Background()

	t.Run("not found for lower-case key", func(t *testing.T) {
		key, ws, err := loadWorkspaceConfigFromStore(ctx, base, "active")
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		assertActiveWorkspace(t, key, ws)
	})

	t.Run("invalid for lower-case key", func(t *testing.T) {
		st := &faultyStore{Store: base, invalidGetKeys: map[string]bool{"active": true}}
		key, ws, err := loadWorkspaceConfigFromStore(ctx, st, "active")
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		assertActiveWorkspace(t, key, ws)
	})
}

func TestResolveActiveWorkspace_Scoped(t *testing.T) {
	t.Run("unrelated workspace failure does not block", func(t *testing.T) {
		base := seedScopedStore(t)
		overrideOpenScopedStore(t, &faultyStore{
			Store:      base,
			failRepos:  map[string]error{"OTHER": errRateLimited},
			failDaemon: map[string]error{"OTHER": errRateLimited},
		})
		t.Setenv(bootstrap.EnvWorkspace, "ACTIVE")

		ws, err := ResolveActiveWorkspace()
		if err != nil {
			t.Fatalf("ResolveActiveWorkspace() error = %v", err)
		}
		assertActiveWorkspace(t, "ACTIVE", ws)
	})

	t.Run("active workspace failure propagates", func(t *testing.T) {
		base := seedScopedStore(t)
		overrideOpenScopedStore(t, &faultyStore{Store: base, failRepos: map[string]error{"ACTIVE": errRateLimited}})
		t.Setenv(bootstrap.EnvWorkspace, "ACTIVE")

		ws, err := ResolveActiveWorkspace()
		if !errors.Is(err, errRateLimited) {
			t.Fatalf("ResolveActiveWorkspace() error = %v, want wrapped 429", err)
		}
		if ws != nil {
			t.Fatalf("ws = %+v, want nil", ws)
		}
	})

	t.Run("missing workspace", func(t *testing.T) {
		overrideOpenScopedStore(t, seedScopedStore(t))
		t.Setenv(bootstrap.EnvWorkspace, "MISSING")

		ws, err := ResolveActiveWorkspace()
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("ResolveActiveWorkspace() error = %v, want not found", err)
		}
		if ws != nil {
			t.Fatalf("ws = %+v, want nil", ws)
		}
	})

	t.Run("no active workspace", func(t *testing.T) {
		calls := overrideOpenScopedStore(t, seedScopedStore(t))
		t.Setenv(bootstrap.EnvWorkspace, "")

		ws, err := ResolveActiveWorkspace()
		if err != nil || ws != nil {
			t.Fatalf("ResolveActiveWorkspace() = %+v, %v; want nil, nil", ws, err)
		}
		if n := calls.Load(); n != 0 {
			t.Fatalf("store opened %d times, want 0", n)
		}
	})
}

func TestLoadWorkspaceConfigCached_DoesNotCacheErrors(t *testing.T) {
	base := seedScopedStore(t)
	st := &faultyStore{Store: base, failRepos: map[string]error{"ACTIVE": errRateLimited}}
	calls := overrideOpenScopedStore(t, st)

	if _, _, err := LoadWorkspaceConfigCached("ACTIVE"); !errors.Is(err, errRateLimited) {
		t.Fatalf("first call error = %v, want wrapped 429", err)
	}

	// Recover the store; the next call must retry rather than serve the error.
	st.failRepos = nil
	key, ws, err := LoadWorkspaceConfigCached("ACTIVE")
	if err != nil {
		t.Fatalf("second call error = %v", err)
	}
	assertActiveWorkspace(t, key, ws)
	if n := calls.Load(); n != 2 {
		t.Fatalf("store opened %d times, want 2", n)
	}
}

func TestLoadWorkspaceConfigCached_CachesSuccess(t *testing.T) {
	calls := overrideOpenScopedStore(t, seedScopedStore(t))

	for i := 0; i < 3; i++ {
		key, ws, err := LoadWorkspaceConfigCached("ACTIVE")
		if err != nil {
			t.Fatalf("call %d error = %v", i, err)
		}
		assertActiveWorkspace(t, key, ws)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("store opened %d times, want 1", n)
	}

	// The returned value is a copy; mutating it must not poison the cache.
	_, ws, _ := LoadWorkspaceConfigCached("ACTIVE")
	ws.Path = "/mutated"
	if _, again, _ := LoadWorkspaceConfigCached("ACTIVE"); again.Path != "/tmp/active" {
		t.Fatalf("cached path = %q, want /tmp/active", again.Path)
	}

	InvalidateConfigCache()
	if _, _, err := LoadWorkspaceConfigCached("ACTIVE"); err != nil {
		t.Fatalf("post-invalidate error = %v", err)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("store opened %d times after invalidate, want 2", n)
	}
}

func TestLoadWorkspaceConfigCached_ReusesListAllProjection(t *testing.T) {
	st := seedScopedStore(t)
	if _, err := TestingPrimeConfigCacheFromStore(context.Background(), st); err != nil {
		t.Fatalf("prime config cache: %v", err)
	}
	calls := overrideOpenScopedStore(t, st)

	key, ws, err := LoadWorkspaceConfigCached("active")
	if err != nil {
		t.Fatalf("LoadWorkspaceConfigCached() error = %v", err)
	}
	assertActiveWorkspace(t, key, ws)
	if n := calls.Load(); n != 0 {
		t.Fatalf("store opened %d times, want 0 (served from list-all cache)", n)
	}
}
