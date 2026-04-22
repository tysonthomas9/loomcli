package config

import (
	"os"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// cachedSnapshot is immutable once published. Readers atomic.Load it; writers
// atomic.Store a replacement. The cache holds no mutex, so it cannot
// participate in a lock-ordering cycle with the config file flock.
type cachedSnapshot struct {
	cfg   *LoomConfig
	err   error
	path  string    // GetConfigPath() at snapshot time — invalidates on test dir swap
	mtime time.Time // file ModTime at snapshot time — invalidates on any write, flock-free
}

var (
	cachedConfig atomic.Pointer[cachedSnapshot]

	// cacheFlight coalesces concurrent miss-path readers so only one goroutine
	// pays the flock + YAML parse. Others block on the singleflight barrier,
	// never on any lock that a concurrent writer might also want.
	cacheFlight singleflight.Group
)

// LoadConfigCached returns the cached LoomConfig, reloading from disk when the
// cache is empty or the config file has changed since it was cached. The cache
// is keyed by (config path, file ModTime): the path key handles test isolation
// via LOOM_CONFIG_DIR, the mtime key handles writes through SaveConfig AND
// out-of-band edits (e.g., a user editing ~/.loom/config.yaml in a text
// editor).
//
// Fast path is wait-free: one atomic.Load + one os.Stat. Miss path runs under
// singleflight so only one goroutine reloads from disk even under a thundering
// herd.
func LoadConfigCached() (*LoomConfig, error) {
	path := GetConfigPath()
	mtime := configFileMtime(path)

	if snap := cachedConfig.Load(); snap != nil &&
		snap.path == path &&
		snap.mtime.Equal(mtime) {
		return snap.cfg, snap.err
	}

	v, _, _ := cacheFlight.Do(path, func() (any, error) {
		// Another goroutine from the same flight window may already have
		// published a fresh snapshot — honor it.
		if snap := cachedConfig.Load(); snap != nil &&
			snap.path == path &&
			snap.mtime.Equal(configFileMtime(path)) {
			return snap, nil
		}

		cfg, err := LoadConfig()
		snap := &cachedSnapshot{
			cfg:  cfg,
			err:  err,
			path: path,
			// Re-stat after LoadConfig so the snapshot's mtime matches the
			// bytes we actually parsed, even if a writer landed mid-flight.
			mtime: configFileMtime(path),
		}
		cachedConfig.Store(snap)
		return snap, nil
	})

	snap := v.(*cachedSnapshot)
	return snap.cfg, snap.err
}

// InvalidateConfigCache clears the cached snapshot, forcing the next
// LoadConfigCached to reload from disk. Safe to call while holding the config
// file flock — it performs one atomic store and acquires no other lock. The
// mtime key already handles normal writes; this remains useful as a
// belt-and-suspenders against filesystems with coarse (e.g. 1s) mtime
// resolution, and for tests.
func InvalidateConfigCache() {
	cachedConfig.Store(nil)
}

// configFileMtime returns the file ModTime, or the zero time if the file does
// not exist or the stat fails. Mapping both to zero means a transient stat
// error forces the next LoadConfigCached to go through LoadConfig, which
// surfaces the real error instead of hiding it behind a stale cache.
func configFileMtime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// expireCachedConfigForTest is a test hook: it replaces the cached snapshot's
// mtime with a sentinel that cannot match any real file's mtime, forcing the
// next LoadConfigCached to reload from disk. Production code uses
// InvalidateConfigCache.
func expireCachedConfigForTest() {
	snap := cachedConfig.Load()
	if snap == nil {
		return
	}
	stale := *snap
	stale.mtime = time.Unix(0, 1) // distinct from any real or missing-file mtime
	cachedConfig.Store(&stale)
}
