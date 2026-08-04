package config

import (
	"context"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/store"
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

var daemonConfigCache struct {
	sync.RWMutex
	cfg          *DaemonConfig
	err          error
	expires      time.Time
	dir          string
	workspaceKey string
	projectDir   string
}

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

	daemonConfigCache.Lock()
	daemonConfigCache.cfg = nil
	daemonConfigCache.err = nil
	daemonConfigCache.expires = time.Time{}
	daemonConfigCache.dir = ""
	daemonConfigCache.workspaceKey = ""
	daemonConfigCache.projectDir = ""
	daemonConfigCache.Unlock()
}

// TestingPrimeConfigCacheFromStore projects st into the LoadConfigCached cache.
// It lets tests seed workspace config with memstore without starting fleet-db.
func TestingPrimeConfigCacheFromStore(ctx context.Context, st store.Store) (*LoomConfig, error) {
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

func lookupPrimedDaemonConfig(wsKey, projectDir string) (*DaemonConfig, error, bool) {
	dir := GetConfigDir()
	daemonConfigCache.RLock()
	defer daemonConfigCache.RUnlock()
	if daemonConfigCache.dir == dir &&
		daemonConfigCache.workspaceKey == wsKey &&
		daemonConfigCache.projectDir == projectDir &&
		time.Now().Before(daemonConfigCache.expires) {
		return daemonConfigCache.cfg, daemonConfigCache.err, true
	}
	return nil, nil, false
}

// TestingPrimeDaemonConfigCacheFromStore projects st into the LoadDaemonConfig cache.
// It lets tests seed daemon config with memstore without starting fleet-db.
func TestingPrimeDaemonConfigCacheFromStore(ctx context.Context, st store.Store, wsKey, projectDir string) (*DaemonConfig, error) {
	cfg, err := loadDaemonConfigFromStore(ctx, st, wsKey, newDefaultDaemonConfig(), projectDir)

	daemonConfigCache.Lock()
	daemonConfigCache.cfg = cfg
	daemonConfigCache.err = err
	daemonConfigCache.expires = time.Now().Add(time.Hour)
	daemonConfigCache.dir = GetConfigDir()
	daemonConfigCache.workspaceKey = wsKey
	daemonConfigCache.projectDir = projectDir
	daemonConfigCache.Unlock()
	return cfg, err
}
