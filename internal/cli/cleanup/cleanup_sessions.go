package cleanup

import (
	"context"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// cleanupSessions purges old session directories and compacts the sessions index.
// Returns (purged count, compacted entry count, error).
func cleanupSessions(ctx context.Context, runtimeDir string, maxAge time.Duration, dryRun bool) (int, int, error) {
	archive, err := sessions.OpenArchive(ctx, runtimeDir)
	if err != nil {
		return 0, 0, fmt.Errorf("open session store: %w", err)
	}

	result, err := archive.Cleanup(sessions.CleanupOptions{OlderThan: maxAge, DryRun: dryRun, Compact: true})
	if err != nil {
		return result.Purged, result.Compacted, fmt.Errorf("clean sessions: %w", err)
	}
	return result.Purged, result.Compacted, nil
}
