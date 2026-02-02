package types

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// ShouldSkipDatabase checks if the given beads directory has an exclusive lock file.
// It returns true if the database should be skipped (lock is held by a live process),
// false otherwise. It also returns the lock holder name if available, and any error encountered.
//
// This function uses flock (file locking) to atomically test if the lock is held,
// eliminating the TOCTOU race condition present in read-then-remove approaches.
//
// The function will:
// - Return false if no lock file exists (proceed with database)
// - Return true if lock file exists and flock fails (lock is held, skip database)
// - Remove stale locks (flock succeeds = no process holds it) and return false (proceed with database)
// - Return true on file open errors (fail-safe, skip database)
func ShouldSkipDatabase(beadsDir string) (skip bool, holder string, err error) {
	lockPath := filepath.Join(beadsDir, ".exclusive-lock")

	// Open lock file with O_RDWR (required for flock on some systems)
	// #nosec G304 - controlled path from config
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	if err != nil {
		if os.IsNotExist(err) {
			// No lock file, proceed with database
			return false, "", nil
		}
		// Error opening lock file, fail-safe: skip database
		return true, "", fmt.Errorf("failed to open lock file: %w", err)
	}

	// Try to acquire non-blocking exclusive lock
	flockErr := lockfile.FlockExclusiveNonBlocking(f)
	if flockErr == lockfile.ErrLockHeld {
		// Lock is held by another process - read file to get holder info
		holder = readLockHolder(f)
		_ = f.Close()
		return true, holder, nil
	}
	if flockErr != nil {
		// Other flock error, fail-safe: skip database
		_ = f.Close()
		return true, "", fmt.Errorf("flock error: %w", flockErr)
	}

	// Flock succeeded - no process holds the lock, so it's stale
	// Read holder info before removing (for logging purposes)
	holder = readLockHolder(f)

	// Release flock and close file before removing (required for Windows)
	_ = lockfile.FlockUnlock(f)
	_ = f.Close()

	// Remove stale lock file
	if err := os.Remove(lockPath); err != nil {
		// Failed to remove stale lock, fail-safe: skip database
		return true, holder, fmt.Errorf("failed to remove stale lock: %w", err)
	}

	// Stale lock removed successfully, proceed with database
	return false, holder, nil
}

// readLockHolder reads the lock file and extracts the holder name.
// Returns empty string if the file cannot be read or parsed.
func readLockHolder(f *os.File) string {
	// Seek to beginning of file
	if _, err := f.Seek(0, 0); err != nil {
		return ""
	}

	var lock ExclusiveLock
	if err := json.NewDecoder(f).Decode(&lock); err != nil {
		return ""
	}

	return lock.Holder
}
