package usage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// Store provides append-only JSONL storage for session usage records.
// Concurrent appends are serialized via flock.
type Store struct {
	path string // full path to usage.jsonl
}

// NewStore creates a Store that writes to {loomDir}/usage.jsonl.
// It creates the loomDir directory if it does not exist.
func NewStore(loomDir string) (*Store, error) {
	if err := os.MkdirAll(loomDir, 0o755); err != nil {
		return nil, fmt.Errorf("create usage dir: %w", err)
	}
	return &Store{path: filepath.Join(loomDir, "usage.jsonl")}, nil
}

// Append serializes record as a single JSON line and appends it to the store
// file, using flock for concurrent safety.
func (s *Store) Append(record SessionUsage) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal usage record: %w", err)
	}
	data = append(data, '\n')

	// #nosec G304 - controlled path from NewStore
	// #nosec G302 - usage.jsonl contains only token counts and cost estimates, not secrets
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open usage file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if err := lockfile.FlockExclusiveBlocking(f); err != nil {
		return fmt.Errorf("flock usage file: %w", err)
	}
	defer func() { _ = lockfile.FlockUnlock(f) }()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write usage record: %w", err)
	}
	return nil
}

// Filter controls which records Read returns. Zero-value fields are ignored.
type Filter struct {
	AgentName string
	Backend   string
	TaskID    string
	EpicID    string
	Status    string
	Since     time.Time
	Until     time.Time
}

// Read returns all usage records matching the filter. If the file does not
// exist, it returns an empty slice (not an error). Corrupt lines are skipped
// with a log warning.
func (s *Store) Read(filter Filter) ([]SessionUsage, error) {
	// #nosec G304 - controlled path from NewStore
	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open usage file: %w", err)
	}
	defer func() { _ = f.Close() }()

	var records []SessionUsage
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec SessionUsage
		if err := json.Unmarshal(line, &rec); err != nil {
			log.Printf("[usage] skipping corrupt line %d: %v", lineNum, err)
			continue
		}
		if matchesFilter(rec, filter) {
			records = append(records, rec)
		}
	}
	if err := scanner.Err(); err != nil {
		return records, fmt.Errorf("read usage file: %w", err)
	}
	return records, nil
}

func matchesFilter(rec SessionUsage, f Filter) bool {
	if f.AgentName != "" && rec.AgentName != f.AgentName {
		return false
	}
	if f.Backend != "" && rec.Backend != f.Backend {
		return false
	}
	if f.TaskID != "" && rec.TaskID != f.TaskID {
		return false
	}
	if f.EpicID != "" && rec.EpicID != f.EpicID {
		return false
	}
	if f.Status != "" && rec.Status != f.Status {
		return false
	}
	if !f.Since.IsZero() && rec.StartedAt.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && rec.EndedAt.After(f.Until) {
		return false
	}
	return true
}
