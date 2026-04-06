package cleanup

import (
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// cleanupSessions purges old session directories and compacts the sessions index.
// Returns (purged count, compacted entry count, error).
func cleanupSessions(beadsDir string, maxAge time.Duration, dryRun bool) (int, int, error) {
	store, err := sessions.NewStore(beadsDir)
	if err != nil {
		return 0, 0, fmt.Errorf("open session store: %w", err)
	}

	if dryRun {
		return cleanupSessionsDryRun(store, maxAge)
	}

	// Purge old session directories.
	purged, err := store.PurgeOlderThan(maxAge)
	if err != nil {
		return purged, 0, fmt.Errorf("purge sessions: %w", err)
	}

	// Compact index AFTER purge so orphaned entries are detected.
	compacted, err := store.CompactIndex()
	if err != nil {
		return purged, 0, fmt.Errorf("compact sessions index: %w", err)
	}

	return purged, compacted, nil
}

// cleanupSessionsDryRun previews what cleanup would do without modifying disk.
func cleanupSessionsDryRun(store *sessions.Store, maxAge time.Duration) (int, int, error) {
	cutoff := time.Now().UTC().Add(-maxAge)

	// Count sessions that would be purged.
	records, err := store.Query(sessions.Filter{})
	if err != nil {
		return 0, 0, fmt.Errorf("query sessions for dry-run: %w", err)
	}

	wouldPurge := 0
	for _, rec := range records {
		if rec.Status == sessions.StatusRunning {
			continue
		}
		if rec.EndedAt == nil {
			continue
		}
		if rec.EndedAt.Before(cutoff) {
			wouldPurge++
		}
	}

	// Count compaction potential.
	total, unique, err := store.CountIndexEntries()
	if err != nil {
		return wouldPurge, 0, fmt.Errorf("count index entries for dry-run: %w", err)
	}
	wouldCompact := total - unique // duplicates only; orphans require actual purge to detect

	return wouldPurge, wouldCompact, nil
}
