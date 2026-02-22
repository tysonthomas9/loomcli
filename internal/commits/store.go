// Package commits provides a store for tracking which git commits are
// associated with beads issues. Records are stored in .beads/commits.jsonl
// as an append-only JSONL file that is git-tracked.
package commits

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Record represents a single commit-to-task mapping.
type Record struct {
	TaskID    string    `json:"task_id"`
	SHA       string    `json:"sha"`
	Subject   string    `json:"subject"`
	Author    string    `json:"author"`
	Timestamp time.Time `json:"timestamp"`
	Worktree  string    `json:"worktree,omitempty"`
}

const commitsFile = "commits.jsonl"

// CommitsPath returns the full path to the commits.jsonl file.
func CommitsPath(beadsDir string) string {
	return filepath.Join(beadsDir, commitsFile)
}

// LoadAll reads all commit records from the JSONL file.
// Returns an empty slice if the file does not exist.
func LoadAll(beadsDir string) ([]Record, error) {
	path := CommitsPath(beadsDir)

	// #nosec G304 - path is derived from beadsDir which is a controlled directory
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open commits file: %w", err)
	}
	defer f.Close()

	var records []Record
	scanner := bufio.NewScanner(f)
	// Allow up to 1MB per line for safety
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec Record
		if err := json.Unmarshal(line, &rec); err != nil {
			// Skip malformed lines rather than failing entirely
			continue
		}
		records = append(records, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read commits file: %w", err)
	}
	return records, nil
}

// LoadForTask reads commit records filtered by task ID.
// Returns at most limit records (0 = unlimited), newest first.
func LoadForTask(beadsDir, taskID string, limit int) ([]Record, error) {
	all, err := LoadAll(beadsDir)
	if err != nil {
		return nil, err
	}

	var filtered []Record
	for _, rec := range all {
		if rec.TaskID == taskID {
			filtered = append(filtered, rec)
		}
	}

	// Sort newest first by timestamp
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Timestamp.After(filtered[j].Timestamp)
	})

	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}

	return filtered, nil
}

// Append writes a single commit record to the JSONL file.
// Creates the file if it does not exist. Uses file locking to
// prevent corruption from concurrent writes by multiple agents.
func Append(beadsDir string, rec Record) error {
	path := CommitsPath(beadsDir)

	// Encode to JSON first to validate
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal commit record: %w", err)
	}
	data = append(data, '\n')

	// Open file for appending, create if needed
	// #nosec G304,G302 - path is derived from beadsDir (controlled); 0644 needed for git tracking
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open commits file for append: %w", err)
	}
	defer f.Close()

	// Acquire file lock to prevent concurrent write corruption
	if err := lockFile(f); err != nil {
		return fmt.Errorf("lock commits file: %w", err)
	}
	defer unlockFile(f) //nolint:errcheck // best-effort unlock on defer

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write commit record: %w", err)
	}

	return nil
}
