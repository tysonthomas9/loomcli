// Package configlock serializes load-mutate-save sequences over a loom config
// directory by holding a blocking exclusive advisory lock on its config.lock
// file, so two loom processes cannot interleave and clobber each other's
// writes. Thin policy layer over internal/lockfile; callers are internal/bootstrap
// (state cache), internal/stackstore, and epic reconcile.
package configlock

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// ConfigLockFileName is the name of the advisory lock file placed alongside config.yaml.
const ConfigLockFileName = "config.lock"

// WithLock acquires the config lock for the given directory, runs fn, then
// releases the lock. Use this to wrap load-mutate-save sequences so that
// concurrent processes cannot interleave and clobber each other's writes.
func WithLock(configDir string, fn func() error) error {
	unlock, err := ConfigLock(configDir)
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

// ConfigLock acquires an exclusive advisory lock on the config lock file
// in the given directory. Returns an unlock function that must be called
// (typically via defer) to release the lock. The lock file and directory
// are created if they do not exist.
func ConfigLock(configDir string) (unlock func(), err error) {
	if configDir == "" {
		return func() {}, fmt.Errorf("configlock: empty config directory")
	}
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return func() {}, fmt.Errorf("configlock: creating directory %s: %w", configDir, err)
	}
	lockPath := filepath.Join(configDir, ConfigLockFileName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600) //nolint:gosec // lockPath is constructed from configDir + fixed filename
	if err != nil {
		return func() {}, fmt.Errorf("configlock: opening %s: %w", lockPath, err)
	}
	if err := lockfile.FlockExclusiveBlocking(f); err != nil {
		f.Close()
		return func() {}, fmt.Errorf("configlock: acquiring lock on %s: %w", lockPath, err)
	}
	return func() {
		_ = lockfile.FlockUnlock(f)
		_ = f.Close()
	}, nil
}
