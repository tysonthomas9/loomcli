package lockfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// ConfigLockFileName is the fixed advisory-lock filename shared by Loom's
// machine-local read-modify-write stores.
const ConfigLockFileName = "config.lock"

// WithConfigLock serializes a machine-local read-modify-write transaction in
// configDir and releases the process lock after fn returns.
func WithConfigLock(configDir string, fn func() error) error {
	unlock, err := ConfigLock(configDir)
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

// ConfigLock acquires a blocking exclusive advisory lock in configDir.
func ConfigLock(configDir string) (unlock func(), err error) {
	if configDir == "" {
		return func() {}, fmt.Errorf("filelock: empty config directory")
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return func() {}, fmt.Errorf("filelock: creating directory %s: %w", configDir, err)
	}
	lockPath := filepath.Join(configDir, ConfigLockFileName)
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // fixed filename under caller-owned config directory.
	if err != nil {
		return func() {}, fmt.Errorf("filelock: opening %s: %w", lockPath, err)
	}
	if err := FlockExclusiveBlocking(file); err != nil {
		_ = file.Close()
		return func() {}, fmt.Errorf("filelock: acquiring lock on %s: %w", lockPath, err)
	}
	return func() {
		_ = FlockUnlock(file)
		_ = file.Close()
	}, nil
}
