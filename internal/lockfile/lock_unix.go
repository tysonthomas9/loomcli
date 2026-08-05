//go:build unix

package lockfile

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// ErrLocked is returned by TryLockExclusive when the lock is already held.
var ErrLocked = errors.New("file lock already held by another process")

// flockFd calls unix.Flock via RawConn to avoid uintptr-to-int conversion.
func flockFd(f *os.File, how int) error {
	rawConn, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var flockErr error
	err = rawConn.Control(func(fd uintptr) {
		flockErr = unix.Flock(int(fd), how) //nolint:gosec // G115 — inside RawConn.Control callback; fd is valid
	})
	if err != nil {
		return err
	}
	return flockErr
}

// flockExclusive acquires an exclusive non-blocking lock on the file
func flockExclusive(f *os.File) error {
	err := flockFd(f, unix.LOCK_EX|unix.LOCK_NB)
	if err == unix.EWOULDBLOCK {
		return ErrLocked
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
	return flockFd(f, unix.LOCK_EX)
}

// FlockUnlock releases a lock on the file.
func FlockUnlock(f *os.File) error {
	return flockFd(f, unix.LOCK_UN)
}
