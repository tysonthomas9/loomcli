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
	configCache.RUnlock()

	configCache.Lock()
	defer configCache.Unlock()
	// Double-check after acquiring write lock
	if configCache.path == curPath && time.Now().Before(configCache.expires) {
		return configCache.cfg, configCache.err
	}
	configCache.cfg, configCache.err = LoadConfig()
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
	configCache.Unlock()
}
