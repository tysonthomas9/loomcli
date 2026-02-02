//go:build js && wasm

package lockfile

import (
	"errors"
	"os"
)

// ErrLockHeld is returned when a non-blocking lock attempt fails because
// another process already holds the lock.
var ErrLockHeld = errors.New("lock already held by another process")

// FlockExclusiveNonBlocking attempts to acquire an exclusive non-blocking lock on the file.
// In WASM, this is a no-op since we're single-process.
func FlockExclusiveNonBlocking(f *os.File) error {
	// WASM doesn't support file locking
	// In a WASM environment, we're typically single-process anyway
	return nil // No-op in WASM
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
