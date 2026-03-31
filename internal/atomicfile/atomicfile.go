package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile writes data to path atomically. It writes to a temporary file
// in the same directory, sets the requested permissions, then renames over
// the target. If any step fails, the temp file is cleaned up and the
// original file (if any) is left untouched.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".loom-atomic-*")
	if err != nil {
		return fmt.Errorf("atomicfile: create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	success := false
	defer func() {
		if !success {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("atomicfile: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("atomicfile: close: %w", err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("atomicfile: chmod: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("atomicfile: rename %s -> %s: %w", tmpName, path, err)
	}
	success = true
	return nil
}
