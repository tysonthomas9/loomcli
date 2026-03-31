package configlock

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// ConfigLockFileName is the name of the advisory lock file placed alongside config.yaml.
const ConfigLockFileName = "config.lock"

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
