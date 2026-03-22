package sessions

import "time"

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
