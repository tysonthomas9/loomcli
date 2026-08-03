//go:build js && wasm

package lockfile

import (
	"errors"
	"os"
)

// ErrLocked is returned by TryLockExclusive when the lock is already held.
var ErrLocked = errors.New("file lock already held by another process")

func flockExclusive(f *os.File) error {
	// WASM doesn't support file locking
	// In a WASM environment, we're typically single-process anyway
	return nil // No-op in WASM
}

// TryLockExclusive attempts to acquire an exclusive non-blocking lock on the file.
// In WASM, this is a no-op since we're single-process.
func TryLockExclusive(f *os.File) error {
	return flockExclusive(f)
}

// FlockExclusiveBlocking acquires an exclusive blocking lock on the file.
// In WASM, this is a no-op since we're single-process.
func FlockExclusiveBlocking(f *os.File) error {
	return nil
}

// FlockUnlock releases a lock on the file.
// In WASM, this is a no-op.
func FlockUnlock(f *os.File) error {
	return nil
}
