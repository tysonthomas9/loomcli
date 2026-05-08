package sessions

import (
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
)

// StaleSessionThreshold is the duration after which a running session
// with no EndedAt is considered orphaned and will be auto-healed to aborted.
const StaleSessionThreshold = 4 * time.Hour

// isStale returns true when a session is still marked as running,
// has no EndedAt, and was started more than StaleSessionThreshold ago.
func isStale(rec SessionRecord) bool {
	return rec.Status == StatusRunning &&
		rec.EndedAt == nil &&
		time.Since(rec.StartedAt) > StaleSessionThreshold
}

// healStaleRecords heals any stale sessions in records, persisting the
// changes to disk. Returns the mutated slice and count of healed sessions.
func (s *Store) healStaleRecords(records []SessionRecord) ([]SessionRecord, int) {
	healed := 0
	for i, rec := range records {
		if !isStale(rec) {
			continue
		}
		ended := rec.StartedAt.Add(StaleSessionThreshold)
		rec.Status = StatusAborted
		rec.EndedAt = &ended
		rec.DurationS = ended.Sub(rec.StartedAt).Seconds()
		records[i] = rec
		s.healStaleSession(rec)
		healed++
	}
	return records, healed
}

// SweepOrphans proactively scans all sessions and heals any that are stale
// (running longer than StaleSessionThreshold with no EndedAt). Returns the
// count of healed sessions.
//
// Call this at application startup to ensure consistent session state before
// new agents launch. During normal operation, Query() performs the same
// healing lazily.
func (s *Store) SweepOrphans() (int, error) {
	_, span := startSpan(cmdstore.RootContext(), "service.Sessions.SweepOrphans")
	defer span.End()

	records, err := s.readDedupedIndex(Filter{})
	if err != nil {
		recordErr(span, err)
		return 0, fmt.Errorf("sweep orphans: %w", err)
	}
	_, healed := s.healStaleRecords(records)
	span.SetAttributes(attrResultCount(healed))
	return healed, nil
}
