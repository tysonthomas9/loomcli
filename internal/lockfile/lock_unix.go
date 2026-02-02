//go:build unix

package lockfile

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// ErrLockHeld is returned when a non-blocking lock attempt fails because
// another process already holds the lock.
var ErrLockHeld = errors.New("lock already held by another process")

// FlockExclusiveNonBlocking attempts to acquire an exclusive non-blocking lock on the file.
// Returns ErrLockHeld if the lock is already held by another process.
func FlockExclusiveNonBlocking(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == unix.EWOULDBLOCK {
		return ErrLockHeld
	}
	return err
}

// FlockExclusiveBlocking acquires an exclusive blocking lock on the file.
// This will wait until the lock is available.
func FlockExclusiveBlocking(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX)
}

// FlockUnlock releases a lock on the file.
func FlockUnlock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
