package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// validatePathWithinDir resolves existing path components and rejects any
// path whose canonical location escapes the approved checkout root.
func validatePathWithinDir(path, allowedDir string) error {
	resolvedAllowedDir, err := resolvePathForComparison(allowedDir)
	if err != nil {
		return fmt.Errorf("resolve allowed directory: %w", err)
	}
	resolvedPath, err := resolvePathForComparison(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	relative, err := filepath.Rel(resolvedAllowedDir, resolvedPath)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path outside allowed directory")
	}
	return nil
}

func resolvePathForComparison(path string) (string, error) {
	cleaned := filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(cleaned)
	if parent == cleaned {
		return cleaned, nil
	}
	resolvedParent, err := resolvePathForComparison(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(cleaned)), nil
}
