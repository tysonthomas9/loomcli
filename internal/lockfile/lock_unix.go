//go:build unix

package lockfile

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var errDaemonLocked = errors.New("daemon lock already held by another process")

// ErrLocked is returned by TryLockExclusive when the lock is already held.
var ErrLocked = errDaemonLocked

// flockExclusive acquires an exclusive non-blocking lock on the file
func flockExclusive(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == unix.EWOULDBLOCK {
		return errDaemonLocked
	}
	return err
}

// TryLockExclusive attempts to acquire an exclusive non-blocking lock on the file.
// Returns ErrLocked if the lock is already held by another process.
func TryLockExclusive(f *os.File) error {
	return flockExclusive(f)
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
