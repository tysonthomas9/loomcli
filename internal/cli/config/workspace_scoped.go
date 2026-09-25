package config

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// ErrWorkspaceNotFound is returned by the single-workspace loaders when the
// requested key does not exist in FleetDB.
var ErrWorkspaceNotFound = errors.New("workspace not found in fleet-db")

// openConfigStore opens the FleetDB store used by LoadConfig and the
// single-workspace loaders. Tests replace it to inject failures without
// starting fleet-db.
var openConfigStore = func(ctx context.Context) (store.Store, func(), error) {
	dataDir := bootstrap.LoomDir()
	if dataDir == "" {
		return nil, nil, errors.New("cannot determine loom data directory")
	}
	handle, err := bootstrap.OpenStore(ctx, dataDir, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("open fleet-db store: %w", err)
	}
	// Apply store-level tracing (this path bypasses cmdstore.OpenStore).
	handle.Store = cmdstore.WrapStoreWithTracing(handle.Store)
	return handle.Store, func() { _ = handle.Close() }, nil
}

var workspaceConfigCache struct {
	sync.Mutex
	dir     string
	entries map[string]workspaceConfigCacheEntry
	// gen is bumped on invalidation so a load that was in flight during an
	// invalidation does not write its (possibly stale) result back.
	gen uint64
}

type workspaceConfigCacheEntry struct {
	key     string
	ws      WorkspaceConfig
	expires time.Time
}

// LoadWorkspaceConfig projects exactly one FleetDB workspace (its row, repos,
// and daemon backend) without enumerating other workspaces. Unrelated
// workspaces therefore cannot fail the load. name is matched exactly, then
// upper-cased. The returned key is the canonical FleetDB workspace key.
func LoadWorkspaceConfig(name string) (string, *WorkspaceConfig, error) {
	ctx := cmdstore.RootContext()
	st, closeFn, err := openConfigStore(ctx)
	if err != nil {
		return "", nil, err
	}
	defer closeFn()
	return loadWorkspaceConfigFromStore(ctx, st, name)
}

// LoadWorkspaceConfigCached is LoadWorkspaceConfig with a short-lived cache.
// A fresh, successful LoadConfigCached projection is reused when it already
// contains the workspace, so callers never trigger a list-all load.
func LoadWorkspaceConfigCached(name string) (string, *WorkspaceConfig, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil, errors.New("workspace name is required")
	}
	dir := GetConfigDir()

	if key, ws, ok := lookupListAllConfigCache(dir, name); ok {
		return key, ws, nil
	}
	key, ws, gen, ok := lookupWorkspaceConfigCache(dir, name)
	if ok {
		return key, ws, nil
	}

	// Load without holding the cache lock so a slow or rate-limited workspace
	// never blocks lookups of other workspaces. Concurrent duplicate loads of
	// the same workspace are harmless reads.
	key, ws, err := LoadWorkspaceConfig(name)
	if err != nil {
		// Errors are not cached so a transient failure (e.g. rate limiting)
		// is retried by the next caller.
		return "", nil, err
	}
	storeWorkspaceConfigCache(dir, name, key, ws, gen)
	return key, ws, nil
}

// lookupListAllConfigCache returns the workspace from a fresh, successful
// LoadConfigCached projection, if one is cached and contains it.
func lookupListAllConfigCache(dir, name string) (string, *WorkspaceConfig, bool) {
	configCache.RLock()
	defer configCache.RUnlock()
	if configCache.dir != dir || configCache.err != nil || configCache.cfg == nil || !time.Now().Before(configCache.expires) {
		return "", nil, false
	}
	for _, key := range workspaceKeyCandidates(name) {
		if ws, ok := configCache.cfg.Workspaces[key]; ok {
			return key, &ws, true
		}
	}
	return "", nil, false
}

// lookupWorkspaceConfigCache returns a fresh scoped cache entry, or the cache
// generation to pass to storeWorkspaceConfigCache after a miss.
func lookupWorkspaceConfigCache(dir, name string) (string, *WorkspaceConfig, uint64, bool) {
	workspaceConfigCache.Lock()
	defer workspaceConfigCache.Unlock()
	if workspaceConfigCache.dir == dir {
		if e, ok := workspaceConfigCache.entries[name]; ok && time.Now().Before(e.expires) {
			ws := e.ws
			return e.key, &ws, 0, true
		}
	}
	return "", nil, workspaceConfigCache.gen, false
}

// storeWorkspaceConfigCache caches a successful scoped load unless the cache
// was invalidated (gen changed) while the load was in flight.
func storeWorkspaceConfigCache(dir, name, key string, ws *WorkspaceConfig, gen uint64) {
	workspaceConfigCache.Lock()
	defer workspaceConfigCache.Unlock()
	if workspaceConfigCache.gen != gen {
		return
	}
	if workspaceConfigCache.dir != dir {
		workspaceConfigCache.dir = dir
		workspaceConfigCache.entries = nil
	}
	if workspaceConfigCache.entries == nil {
		workspaceConfigCache.entries = make(map[string]workspaceConfigCacheEntry)
	}
	workspaceConfigCache.entries[name] = workspaceConfigCacheEntry{
		key:     key,
		ws:      *ws,
		expires: time.Now().Add(configCacheTTL),
	}
}

// TestingSetConfigStoreOpener replaces the FleetDB store used by LoadConfig
// and the single-workspace loaders, and returns a restore func. It lets tests
// in other packages inject per-workspace FleetDB failures.
func TestingSetConfigStoreOpener(open func(ctx context.Context) (store.Store, func(), error)) (restore func()) {
	old := openConfigStore
	openConfigStore = open
	InvalidateConfigCache()
	return func() {
		openConfigStore = old
		InvalidateConfigCache()
	}
}

func invalidateWorkspaceConfigCache() {
	workspaceConfigCache.Lock()
	workspaceConfigCache.dir = ""
	workspaceConfigCache.entries = nil
	workspaceConfigCache.gen++
	workspaceConfigCache.Unlock()
}

func workspaceKeyCandidates(name string) []string {
	if upper := strings.ToUpper(name); upper != name {
		return []string{name, upper}
	}
	return []string{name}
}

func loadWorkspaceConfigFromStore(ctx context.Context, st store.Store, name string) (string, *WorkspaceConfig, error) {
	var ws *domain.Workspace
	var lastErr error
	for _, key := range workspaceKeyCandidates(name) {
		got, err := st.Workspaces().Get(ctx, key)
		if err == nil && got != nil {
			ws = got
			break
		}
		if err == nil {
			continue
		}
		// A non-canonical key (e.g. lower case) is rejected as invalid by
		// fleet-db; fall through to the upper-cased candidate.
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrInvalid) {
			lastErr = err
			continue
		}
		return "", nil, fmt.Errorf("get fleet-db workspace %s: %w", key, err)
	}
	if ws == nil {
		if lastErr != nil && !errors.Is(lastErr, domain.ErrNotFound) {
			return "", nil, fmt.Errorf("get fleet-db workspace %s: %w", name, lastErr)
		}
		return "", nil, fmt.Errorf("workspace %q: %w", name, ErrWorkspaceNotFound)
	}
	local := bootstrap.WorkspaceLocalState{}
	if sc, _ := bootstrap.LoadStateCache(); sc != nil {
		local = sc.Workspaces[ws.Key]
	}
	wsc, err := workspaceConfigFromStore(ctx, st, ws, local)
	if err != nil {
		return "", nil, err
	}
	return ws.Key, &wsc, nil
}
