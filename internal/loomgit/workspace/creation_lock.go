package workspace

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

func creationLockPath(workspace string) string {
	return filepath.Join(config.GetConfigDir(), "loomgit", fmt.Sprintf("creation-%x.lock", sha256.Sum256([]byte(workspace))))
}

func acquireCreationLock(workspace string) (*os.File, error) {
	return openCreationLock(workspace, false)
}

func tryCreationLock(workspace string) (*os.File, error) {
	return openCreationLock(workspace, true)
}

func openCreationLock(workspace string, nonblocking bool) (*os.File, error) {
	path := creationLockPath(workspace)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
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
	return errors.Join(syscall.Flock(int(file.Fd()), syscall.LOCK_UN), file.Close())
}
