//go:build !windows && !wasm

package misc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// OpenLogFileSecure opens a log file with O_NOFOLLOW to prevent symlink attacks,
// and performs post-open fd-based path verification on Linux.
func OpenLogFileSecure(path, allowedDir string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0) //nolint:gosec
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("refusing to follow symlink: %s", path)
		}
		return nil, err
	}

	if err := verifyOpenedFilePath(f, allowedDir); err != nil {
		f.Close()
		return nil, err
	}

	return f, nil
}

// verifyOpenedFilePath reads /proc/self/fd/<n> to verify the opened file's
// real path is within the allowed directory.
func verifyOpenedFilePath(f *os.File, allowedDir string) error {
	fdPath := fmt.Sprintf("/proc/self/fd/%d", f.Fd())
	realPath, err := os.Readlink(fdPath)
	if err != nil {
		// /proc not available (macOS, FreeBSD) — O_NOFOLLOW is sufficient
		return nil
	}

	realPath = filepath.Clean(realPath)
	resolvedDir, resolveErr := filepath.EvalSymlinks(allowedDir)
	if resolveErr != nil {
		return fmt.Errorf("failed to resolve allowed directory: %w", resolveErr)
	}
	resolvedDir = filepath.Clean(resolvedDir)

	if !strings.HasPrefix(realPath, resolvedDir+string(filepath.Separator)) && realPath != resolvedDir {
		return fmt.Errorf("opened file path %s escapes allowed directory", realPath)
	}

	return nil
}
