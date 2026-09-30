package pool

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Linux reflinks are supplied by the filesystem through FICLONE.
func cloneFile(source, target string) error {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()
	return errors.Join(unix.IoctlFileClone(int(dst.Fd()), int(src.Fd())), dst.Sync())
}
