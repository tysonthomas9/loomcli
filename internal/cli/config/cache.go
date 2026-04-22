// Lock-order invariant for this package (see loomcli-rc1s2 for the incident
// this prevents). The workspace-create path and the monitor/agent-poll path
// used to deadlock when the cache took an in-process mutex that was acquired
// in the opposite order relative to the config file flock. The current design
// dissolves the cycle; the rules below keep future edits from reintroducing
// it.
//
//	Rule A (cache has no mutex):
//	  cachedConfig is an atomic.Pointer[cachedSnapshot]. Readers publish and
//	  load snapshots with one atomic op; they never hold a lock across
//	  LoadConfig (which acquires the configlock flock). Keep it that way — do
//	  not wrap the cache in a sync.Mutex / sync.RWMutex without also removing
//	  every flock acquisition that could run while the lock is held.
//
//	Rule B (*Unlocked inside WithConfigLock):
//	  When holding the flock (inside WithConfigLock's fn) use LoadConfigUnlocked
//	  and SaveConfigUnlocked. SaveConfigUnlocked calls InvalidateConfigCache —
//	  that is exactly one atomic.Store(nil), so it is safe under any caller
//	  lock. Never call LoadConfig, SaveConfig, or LoadConfigCached from inside
//	  WithConfigLock: they each re-acquire the flock (LoadConfigCached does so
//	  transitively via the singleflight miss-path) and would self-deadlock —
//	  POSIX flock is not reentrant via distinct fds from the same process.
//
//	Rule C (InvalidateConfigCache stays lock-free):
//	  InvalidateConfigCache must perform a single atomic.Store(nil) and
//	  acquire no other lock. Every atomicfile.WriteFile on the config path
//	  must be followed by InvalidateConfigCache (see config.go
//	  SaveConfigUnlocked and config_migrate.go autoMigrateFile) so cache
//	  staleness is bounded without introducing a second lock that could
//	  re-invert.
//
// The regression tests TestLoadConfigCached_ConcurrentReaderAndWriterNoDeadlock
// and TestLoadConfigCached_NoDeadlockWithWriter guard this contract.
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
