package usage

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// PurgeOlderThan removes usage records whose EndedAt is older than the given
// age. Records with zero EndedAt fall back to StartedAt; if both are zero the
// record is kept. The file is rewritten atomically under flock to prevent
// concurrent Append from writing to the old file mid-rename.
// Returns the count of purged records.
func (s *Store) PurgeOlderThan(age time.Duration) (int, error) {
	// #nosec G304 — controlled path from NewStore
	f, err := os.OpenFile(s.path, os.O_RDWR, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("open usage file for purge: %w", err)
	}
	defer func() { _ = f.Close() }()

	if err := lockfile.FlockExclusiveBlocking(f); err != nil {
		return 0, fmt.Errorf("flock usage file for purge: %w", err)
	}
	defer func() { _ = lockfile.FlockUnlock(f) }()

	records, err := s.Read(Filter{})
	if err != nil {
		return 0, fmt.Errorf("read usage for purge: %w", err)
	}
	if len(records) == 0 {
		return 0, nil
	}

	keep, purged := partitionUsageRecords(records, time.Now().UTC().Add(-age))
	if purged == 0 {
		return 0, nil
	}

	if err := writeUsageAtomic(s.path, keep); err != nil {
		return 0, err
	}
	return purged, nil
}

// partitionUsageRecords splits records into keep/purge based on cutoff time.
func partitionUsageRecords(records []SessionUsage, cutoff time.Time) ([]SessionUsage, int) {
	var keep []SessionUsage
	purged := 0
	for _, rec := range records {
		ts := rec.EndedAt
		if ts.IsZero() {
			ts = rec.StartedAt
		}
		if ts.IsZero() || !ts.Before(cutoff) {
			keep = append(keep, rec)
		} else {
			purged++
		}
	}
	return keep, purged
}

// writeUsageAtomic writes records to a temp file then atomically renames.
func writeUsageAtomic(path string, records []SessionUsage) error {
	tmpPath := path + ".tmp"
	// #nosec G304 — controlled path
	// #nosec G302 — usage.jsonl contains only token counts and cost estimates
	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create usage purge tmp: %w", err)
	}
	for _, rec := range records {
		data, marshalErr := json.Marshal(rec)
		if marshalErr != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("marshal usage record during purge: %w", marshalErr)
		}
		if _, writeErr := tmp.Write(append(data, '\n')); writeErr != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("write usage purge tmp: %w", writeErr)
		}
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close usage purge tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename usage purge tmp: %w", err)
	}
	return nil
}
