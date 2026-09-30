package pool

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Linux reflinks are supplied by the filesystem through FICLONE.
func cloneFile(source, target string) error {
	src, err := os.Open(source) //nolint:gosec // Source comes from WalkDir over the admitted repo's .git directory.
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600) //nolint:gosec // Target is inside the new task copy and creation is exclusive.
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()
	return errors.Join(unix.IoctlFileClone(int(dst.Fd()), int(src.Fd())), dst.Sync())
}
