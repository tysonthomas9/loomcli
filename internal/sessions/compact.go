package sessions

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// CompactIndex rewrites index.jsonl to remove duplicate entries and entries
// for sessions whose directories no longer exist on disk.
// Must be called AFTER PurgeOlderThan so that purged directories are already gone.
// Returns the count of removed entries (original line count minus surviving records).
func (s *Store) CompactIndex() (int, error) {
	_, span := startSpan(s.ctx, "service.Sessions.CompactIndex")
	defer span.End()

	indexPath := filepath.Join(s.dir, "index.jsonl")

	// Open for read-write to acquire flock.
	// #nosec G304 — controlled path from Store
	f, err := os.OpenFile(indexPath, os.O_RDWR|os.O_CREATE, sessFilePerm)
	if err != nil {
		recordErr(span, err)
		return 0, fmt.Errorf("open index for compaction: %w", err)
	}
	defer func() { _ = f.Close() }()

	if err := lockfile.FlockExclusiveBlocking(f); err != nil {
		recordErr(span, err)
		return 0, fmt.Errorf("flock index for compaction: %w", err)
	}
	defer func() { _ = lockfile.FlockUnlock(f) }()

	origTotal, _, err := countIndexLines(indexPath)
	if err != nil {
		recordErr(span, err)
		return 0, err
	}
	if origTotal == 0 {
		return 0, nil
	}

	surviving, err := s.survivingRecords()
	if err != nil {
		recordErr(span, err)
		return 0, err
	}

	removed := origTotal - len(surviving)
	if removed == 0 {
		return 0, nil
	}

	if werr := writeRecordsAtomic(indexPath, surviving); werr != nil {
		recordErr(span, werr)
		return removed, werr
	}
	span.SetAttributes(attrResultCount(removed))
	return removed, nil
}

// survivingRecords reads the index, deduplicates, and filters to records
// whose session directory still exists on disk.
func (s *Store) survivingRecords() ([]SessionRecord, error) {
	deduped, err := s.readDedupedIndex(Filter{})
	if err != nil {
		return nil, fmt.Errorf("read index for compaction: %w", err)
	}
	surviving := make([]SessionRecord, 0, len(deduped))
	for _, rec := range deduped {
		if _, statErr := os.Stat(filepath.Join(s.dir, rec.SessionID)); statErr == nil {
			surviving = append(surviving, rec)
		}
	}
	return surviving, nil
}

// writeRecordsAtomic writes records to a temp file then atomically renames.
func writeRecordsAtomic(path string, records []SessionRecord) error {
	tmpPath := path + ".tmp"
	// #nosec G304 — controlled path from Store
	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, sessFilePerm)
	if err != nil {
		return fmt.Errorf("create compaction tmp file: %w", err)
	}
	for _, rec := range records {
		data, marshalErr := json.Marshal(rec)
		if marshalErr != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("marshal record during compaction: %w", marshalErr)
		}
		if _, writeErr := tmp.Write(append(data, '\n')); writeErr != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("write compaction tmp: %w", writeErr)
		}
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close compaction tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename compaction tmp: %w", err)
	}
	return nil
}

// CountIndexEntries returns the total line count and unique session count
// in index.jsonl. Used by dry-run to show compaction potential.
func (s *Store) CountIndexEntries() (total int, unique int, err error) {
	_, span := startSpan(s.ctx, "service.Sessions.CountIndexEntries")
	defer span.End()

	indexPath := filepath.Join(s.dir, "index.jsonl")
	total, unique, err = countIndexLines(indexPath)
	if err != nil {
		recordErr(span, err)
	}
	span.SetAttributes(attrResultCount(unique))
	return
}

// countIndexLines reads index.jsonl and returns total non-empty lines
// and unique session IDs.
func countIndexLines(indexPath string) (int, int, error) {
	// #nosec G304 — controlled path
	f, err := os.Open(filepath.Clean(indexPath))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("read index for counting: %w", err)
	}
	defer func() { _ = f.Close() }()

	seen := make(map[string]struct{})
	total := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		total++
		var rec SessionRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue // corrupt line — counted in total but not unique
		}
		seen[rec.SessionID] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return total, len(seen), fmt.Errorf("scan index for counting: %w", err)
	}
	return total, len(seen), nil
}
