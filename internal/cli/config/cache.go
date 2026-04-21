package config

import (
	"sync"
	"time"
)

// configCache caches the parsed LoomConfig to avoid re-reading the YAML file
// on every request. The config changes rarely (workspace create/delete).
// Cache is keyed by config path so test isolation works automatically.
var configCache struct {
	sync.RWMutex
	cfg     *LoomConfig
	err     error
	path    string // config path when cached — invalidates on path change
	expires time.Time
	// invalidations is a monotonic counter bumped on every InvalidateConfigCache.
	// LoadConfigCached snapshots it before calling LoadConfig (without holding
	// the cache lock) and refuses to seal a stale result if the counter changed
	// during the load — see LoadConfigCached for the full race rationale.
	invalidations uint64
}

const configCacheTTL = 5 * time.Second

// LoadConfigCached returns a cached config or reloads from disk if stale.
func LoadConfigCached() (*LoomConfig, error) {
	curPath := GetConfigPath()
	configCache.RLock()
	if configCache.path == curPath && time.Now().Before(configCache.expires) {
		cfg, err := configCache.cfg, configCache.err
		configCache.RUnlock()
		return cfg, err
	}
	// Snapshot the invalidations counter under the read lock so we can detect
	// a concurrent InvalidateConfigCache that fires while we're loading.
	invalBefore := configCache.invalidations
	configCache.RUnlock()

	// Load WITHOUT holding the cache lock: LoadConfig may go through
	// autoMigrateFile, which calls InvalidateConfigCache after a successful
	// migration write — re-acquiring this RWMutex would deadlock.
	cfg, err := LoadConfig()

	configCache.Lock()
	defer configCache.Unlock()
	// Double-check: another goroutine may have populated the cache while we
	// were loading. Prefer the already-cached value to keep the
	// "same pointer within TTL" invariant for sequential callers.
	if configCache.path == curPath && time.Now().Before(configCache.expires) {
		return configCache.cfg, configCache.err
	}
	// If anyone invalidated the cache while we were in LoadConfig, our cfg
	// may be stale relative to whatever was just written. Don't seal it —
	// return the value to this caller but let the next caller re-load.
	if configCache.invalidations != invalBefore {
		return cfg, err
	}
	configCache.cfg, configCache.err = cfg, err
	configCache.path = curPath
	configCache.expires = time.Now().Add(configCacheTTL)
	return configCache.cfg, configCache.err
}

// InvalidateConfigCache forces the next LoadConfigCached call to re-read disk.
// Call after config mutations (workspace create/delete/rename) and in tests.
func InvalidateConfigCache() {
	configCache.Lock()
	configCache.cfg = nil
	configCache.err = nil
	configCache.expires = time.Time{}
	configCache.invalidations++
	configCache.Unlock()
}
