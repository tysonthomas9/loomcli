package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

func creationLockPath() string {
	return filepath.Join(config.GetConfigDir(), "loomgit", "creation.lock")
}

func acquireCreationLock() (*os.File, error) {
	return openCreationLock(false)
}

func tryCreationLock() (*os.File, error) {
	return openCreationLock(true)
}

func openCreationLock(nonblocking bool) (*os.File, error) {
	path := creationLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	//nolint:gosec // The path is a fixed lock inode in the configured Loom directory.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	flags := syscall.LOCK_EX
	if nonblocking {
		flags |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(file.Fd()), flags); err != nil {
		_ = file.Close()
		if nonblocking && errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, nil
		}
		return nil, err
	}
	return file, nil
}

func releaseCreationLock(file *os.File) error {
	// Keep this single inode for the lifetime of the Loom config directory.
	// Unlinking it here could let another process lock a different inode.
	return errors.Join(syscall.Flock(int(file.Fd()), syscall.LOCK_UN), file.Close())
}
