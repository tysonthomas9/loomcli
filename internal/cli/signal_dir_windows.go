//go:build windows

package cli

import (
	"fmt"
	"os"
)

// ensureSignalDir creates the signal directory on Windows.
// Windows does not support symlink/ownership checks via syscall.Stat_t.
func EnsureSignalDir(dir string) error {
	return os.MkdirAll(dir, 0700)
}

// validateSignalDir validates the signal directory on Windows.
func ValidateSignalDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("signal path is not a directory: %s", dir)
	}
	return nil
}
