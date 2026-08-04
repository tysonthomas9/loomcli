//go:build !windows

package cli

import (
	"fmt"
	"os"
	"syscall"
)

// ensureSignalDir safely creates or validates the signal directory.
// It verifies the path is not a symlink and is owned by the current user,
// preventing symlink attacks and cross-user interference.
func EnsureSignalDir(dir string) error {
	fi, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		if mkErr := os.MkdirAll(dir, 0700); mkErr != nil {
			return fmt.Errorf("failed to create signal directory: %w", mkErr)
		}
		// Re-check after creation to verify no race condition
		fi, err = os.Lstat(dir)
		if err != nil {
			return fmt.Errorf("failed to stat newly created signal directory: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to check signal directory: %w", err)
	}

	return checkSignalDir(fi, dir)
}

// validateSignalDir performs a read-only validation of the signal directory.
// Used by the consumer side (automode) to verify the directory before trusting signal files.
func ValidateSignalDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("failed to check signal directory: %w", err)
	}

	return checkSignalDir(fi, dir)
}

// checkSignalDir verifies that the file info represents a valid, non-symlink
// directory owned by the current user.
func checkSignalDir(fi os.FileInfo, dir string) error {
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("signal directory is a symlink: %s", dir)
	}

	if !fi.IsDir() {
		return fmt.Errorf("signal path is not a directory: %s", dir)
	}

	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("failed to get ownership info for signal directory: %s", dir)
	}

	if stat.Uid != uint32(os.Getuid()) { //nolint:gosec // G115: uid fits uint32 on supported platforms
		return fmt.Errorf("signal directory owned by uid %d, expected %d: %s", stat.Uid, os.Getuid(), dir)
	}

	return nil
}
