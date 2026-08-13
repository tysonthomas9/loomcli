package cleanup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// cleanupSessions is the explicit operator cleanup path for the retired local
// session archive. It intentionally has no read, repair, migration, creation,
// or dual-write behavior.
func cleanupSessions(ctx context.Context, runtimeDir string, maxAge time.Duration, dryRun bool) (int, error) {
	if ctx == nil || runtimeDir == "" || maxAge < 0 {
		return 0, fmt.Errorf("legacy archive cleanup requires context, runtime directory, and non-negative age")
	}
	root, err := filepath.Abs(filepath.Join(runtimeDir, "sessions"))
	if err != nil {
		return 0, fmt.Errorf("resolve legacy archive: %w", err)
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read legacy archive: %w", err)
	}

	cutoff := time.Now().UTC().Add(-maxAge)
	purged := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return purged, err
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if filepath.Dir(path) != root {
			return purged, fmt.Errorf("legacy archive entry escaped cleanup root")
		}
		purged++
		if dryRun {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return purged, fmt.Errorf("remove legacy archive entry %q: %w", entry.Name(), err)
		}
	}
	return purged, nil
}
