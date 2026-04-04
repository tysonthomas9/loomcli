package cli

import (
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

// cleanupUsage purges old records from usage.jsonl.
// Returns (purged count, error).
func cleanupUsage(beadsDir string, maxAge time.Duration, dryRun bool) (int, error) {
	store, err := usage.NewStore(beadsDir)
	if err != nil {
		return 0, fmt.Errorf("open usage store: %w", err)
	}

	if dryRun {
		return cleanupUsageDryRun(store, maxAge)
	}

	purged, err := store.PurgeOlderThan(maxAge)
	if err != nil {
		return 0, fmt.Errorf("purge usage: %w", err)
	}

	return purged, nil
}

// cleanupUsageDryRun counts records that would be purged.
func cleanupUsageDryRun(store *usage.Store, maxAge time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-maxAge)

	records, err := store.Read(usage.Filter{})
	if err != nil {
		return 0, fmt.Errorf("read usage for dry-run: %w", err)
	}

	wouldPurge := 0
	for _, rec := range records {
		ts := rec.EndedAt
		if ts.IsZero() {
			ts = rec.StartedAt
		}
		if !ts.IsZero() && ts.Before(cutoff) {
			wouldPurge++
		}
	}

	return wouldPurge, nil
}
