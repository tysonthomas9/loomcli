package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// PurgeOlderThan removes session directories for sessions that are
// not running and whose EndedAt is older than the given age.
// Returns the count of purged sessions.
func (s *Store) PurgeOlderThan(age time.Duration) (int, error) {
	_, span := startSpan(s.ctx, "service.Sessions.PurgeOlderThan")
	defer span.End()

	cutoff := time.Now().UTC().Add(-age)

	// Query all records from index.jsonl (empty filter = match all).
	records, err := s.Query(Filter{})
	if err != nil {
		recordErr(span, err)
		return 0, fmt.Errorf("query index: %w", err)
	}

	purged := 0
	for _, rec := range records {
		// Never purge running sessions.
		if rec.Status == StatusRunning {
			continue
		}
		// Skip sessions without an EndedAt (shouldn't happen for non-running, but be safe).
		if rec.EndedAt == nil {
			continue
		}
		// Only purge if ended before the cutoff.
		if rec.EndedAt.Before(cutoff) {
			sessDir := filepath.Join(s.dir, rec.SessionID)
			if err := os.RemoveAll(sessDir); err != nil {
				recordErr(span, err)
				span.SetAttributes(attrResultCount(purged))
				return purged, fmt.Errorf("remove session %s: %w", rec.SessionID, err)
			}
			purged++
		}
	}

	span.SetAttributes(attrResultCount(purged))
	return purged, nil
}
