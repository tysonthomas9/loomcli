package sessions

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Query reads index.jsonl and returns all SessionRecords matching the filter.
// If the index file does not exist, it returns an empty slice (not an error).
// Corrupt lines are skipped with a log warning.
func (s *Store) Query(f Filter) ([]SessionRecord, error) {
	indexPath := filepath.Join(s.dir, "index.jsonl")

	// #nosec G304 — controlled path from Store
	file, err := os.Open(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open index file: %w", err)
	}
	defer func() { _ = file.Close() }()

	var records []SessionRecord
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec SessionRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			log.Printf("[sessions] skipping corrupt line %d: %v", lineNum, err)
			continue
		}
		if matchesSessionFilter(rec, f) {
			records = append(records, rec)
		}
	}
	if err := scanner.Err(); err != nil {
		return records, fmt.Errorf("read index file: %w", err)
	}
	return records, nil
}

// SessionsByTask is a convenience wrapper that returns all sessions for a task.
func (s *Store) SessionsByTask(taskID string) ([]SessionRecord, error) {
	return s.Query(Filter{TaskID: taskID})
}

// LoadMetadata reads and returns the SessionMetadata from
// sessions/<sessionID>/metadata.json.
func (s *Store) LoadMetadata(sessionID string) (*SessionMetadata, error) {
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}

	metaPath := filepath.Join(s.dir, sessionID, "metadata.json")

	// #nosec G304 — sessionID validated above
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("read metadata.json: %w", err)
	}

	var meta SessionMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("unmarshal metadata: %w", err)
	}
	return &meta, nil
}

// LoadTranscript reads and returns all TranscriptEntries from
// sessions/<sessionID>/transcript.jsonl, sorted by Seq ascending.
func (s *Store) LoadTranscript(sessionID string) ([]TranscriptEntry, error) {
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}

	txPath := filepath.Join(s.dir, sessionID, "transcript.jsonl")

	// #nosec G304 — sessionID validated above
	file, err := os.Open(txPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer func() { _ = file.Close() }()

	var entries []TranscriptEntry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e TranscriptEntry
		if err := json.Unmarshal(line, &e); err != nil {
			log.Printf("[sessions] skipping corrupt transcript line: %v", err)
			continue
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return entries, fmt.Errorf("read transcript: %w", err)
	}

	// Sort by Seq ascending.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Seq < entries[j].Seq
	})

	return entries, nil
}

// ReadPrompt reads and returns the prompt text from
// sessions/<sessionID>/prompt.txt.
func (s *Store) ReadPrompt(sessionID string) (string, error) {
	if err := validateSessionID(sessionID); err != nil {
		return "", err
	}

	promptPath := filepath.Join(s.dir, sessionID, "prompt.txt")

	// #nosec G304 — sessionID validated above
	data, err := os.ReadFile(promptPath)
	if err != nil {
		return "", fmt.Errorf("read prompt.txt: %w", err)
	}
	return string(data), nil
}

// validateSessionID rejects IDs containing path separators or traversal attempts.
func validateSessionID(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("session ID must not be empty")
	}
	if strings.ContainsAny(sessionID, "/\\") {
		return fmt.Errorf("invalid session ID %q: contains path separator", sessionID)
	}
	if strings.Contains(sessionID, "..") {
		return fmt.Errorf("invalid session ID %q: contains path traversal", sessionID)
	}
	return nil
}

// matchesSessionFilter checks whether a SessionRecord matches the given filter.
func matchesSessionFilter(rec SessionRecord, f Filter) bool {
	if f.TaskID != "" && rec.TaskID != f.TaskID {
		return false
	}
	if f.EpicID != "" && rec.EpicID != f.EpicID {
		return false
	}
	if f.AgentName != "" && rec.AgentName != f.AgentName {
		return false
	}
	if f.Backend != "" && rec.Backend != f.Backend {
		return false
	}
	if f.Status != "" && rec.Status != f.Status {
		return false
	}
	if !f.Since.IsZero() && rec.StartedAt.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && rec.StartedAt.After(f.Until) {
		return false
	}
	return true
}
