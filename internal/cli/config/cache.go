package config

import (
	"context"
	"sync"
	"time"
)

// configCache caches the FleetDB workspace projection briefly to avoid repeated
// list calls during command startup.
var configCache struct {
	sync.RWMutex
	cfg     *LoomConfig
	err     error
	expires time.Time
	dir     string
}

const configCacheTTL = 5 * time.Second

// LoadConfigCached returns a cached config or reloads the FleetDB projection if stale.
func LoadConfigCached() (*LoomConfig, error) {
	dir := GetConfigDir()
	configCache.RLock()
	if configCache.dir == dir && time.Now().Before(configCache.expires) {
		cfg, err := configCache.cfg, configCache.err
		configCache.RUnlock()
		return cfg, err
	}
	configCache.RUnlock()

	configCache.Lock()
	defer configCache.Unlock()
	// Double-check after acquiring write lock
	if configCache.dir == dir && time.Now().Before(configCache.expires) {
		return configCache.cfg, configCache.err
	}
	configCache.cfg, configCache.err = LoadConfig()
	configCache.dir = dir
	configCache.expires = time.Now().Add(configCacheTTL)
	return configCache.cfg, configCache.err
}

// InvalidateConfigCache forces the next LoadConfigCached call to re-read FleetDB.
func InvalidateConfigCache() {
	configCache.Lock()
	configCache.cfg = nil
	configCache.err = nil
	configCache.expires = time.Time{}
	configCache.dir = ""
	configCache.Unlock()

}

// TestingPrimeConfigCacheFromStore projects st into the LoadConfigCached cache.
// It lets tests seed workspace config with memstore without starting fleet-db.
func TestingPrimeConfigCacheFromStore(ctx context.Context, st configStore) (*LoomConfig, error) {
	cfg, err := loadConfigFromStore(ctx, st)
	if err != nil {
		return nil, err
	}

	configCache.Lock()
	configCache.cfg = cfg
	configCache.err = nil
	configCache.expires = time.Now().Add(time.Hour)
	configCache.dir = GetConfigDir()
	configCache.Unlock()
	return cfg, nil
}
