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

	"go.opentelemetry.io/otel/attribute"
)

// readDedupedIndex reads index.jsonl, applies the filter, and deduplicates
// by SessionID (last-seen record wins). Returns the deduplicated slice.
func (s *Store) readDedupedIndex(f Filter) ([]SessionRecord, error) {
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
		normalizeRecord(&rec)
		if matchesSessionFilter(rec, f) {
			records = append(records, rec)
		}
	}
	if err := scanner.Err(); err != nil {
		return records, fmt.Errorf("read index file: %w", err)
	}

	// Deduplicate by SessionID — keep the last-seen record per ID.
	// Finalized records are appended after running records, so last wins.
	seen := make(map[string]int, len(records))
	deduped := make([]SessionRecord, 0, len(records))
	for _, rec := range records {
		if idx, ok := seen[rec.SessionID]; ok {
			deduped[idx] = rec // overwrite with later (finalized) version
		} else {
			seen[rec.SessionID] = len(deduped)
			deduped = append(deduped, rec)
		}
	}
	return deduped, nil
}

// Query reads index.jsonl and returns all SessionRecords matching the filter.
// If the index file does not exist, it returns an empty slice (not an error).
// Corrupt lines are skipped with a log warning.
func (s *Store) Query(f Filter) ([]SessionRecord, error) {
	// Filter values (TaskID, AgentName, Backend) are caller-supplied but
	// drawn from the same allowlist as direct attrs — agent name, task ID,
	// backend enum. Status is an enum. Time bounds are not attrs.
	attrs := []attribute.KeyValue{}
	if f.AgentName != "" {
		attrs = append(attrs, attrLoomAgent(f.AgentName))
	}
	if f.TaskID != "" {
		attrs = append(attrs, attrLoomTaskID(f.TaskID))
	}
	if f.Backend != "" {
		attrs = append(attrs, attrLoomBackend(f.Backend))
	}
	_, span := startSpan(s.ctx, "service.Sessions.Query", attrs...)
	defer span.End()

	deduped, err := s.readDedupedIndex(f)
	if err != nil {
		recordErr(span, err)
		return deduped, err
	}

	// Staleness detection pass: auto-heal orphaned running sessions.
	deduped, _ = s.healStaleRecords(deduped)

	// Re-filter if a status filter was set, because healed records
	// may no longer match (e.g., filter for "running" but record is now "aborted").
	if f.Status != "" {
		filtered := deduped[:0]
		for _, rec := range deduped {
			if rec.Status == f.Status {
				filtered = append(filtered, rec)
			}
		}
		deduped = filtered
	}

	span.SetAttributes(attrResultCount(len(deduped)))
	return deduped, nil
}

// healStaleSession persists the healed (aborted) session record to disk.
// It writes both metadata.json and appends to index.jsonl so future queries
// see the corrected status without re-healing.
func (s *Store) healStaleSession(rec SessionRecord) {
	sessDir := filepath.Join(s.dir, rec.SessionID)
	meta := SessionMetadata{SessionRecord: rec}
	if err := writeMetadataAtomic(sessDir, meta); err != nil {
		log.Printf("[sessions] heal stale %s: write metadata: %v", rec.SessionID, err)
		return
	}
	if err := s.appendIndex(rec); err != nil {
		log.Printf("[sessions] heal stale %s: append index: %v", rec.SessionID, err)
	}
}

// SessionsByTask is a convenience wrapper that returns all sessions for a task.
func (s *Store) SessionsByTask(taskID string) ([]SessionRecord, error) {
	return s.Query(Filter{TaskID: taskID})
}

// LoadMetadata reads and returns the SessionMetadata from
// sessions/<sessionID>/metadata.json.
func (s *Store) LoadMetadata(sessionID string) (*SessionMetadata, error) {
	_, span := startSpan(s.ctx, "service.Sessions.LoadMetadata",
		attrLoomSessionID(sessionID),
	)
	defer span.End()

	if err := validateSessionID(sessionID); err != nil {
		recordErr(span, err)
		return nil, err
	}

	metaPath := filepath.Join(s.dir, sessionID, "metadata.json")

	// #nosec G304 — sessionID validated above
	data, err := os.ReadFile(metaPath)
	if err != nil {
		recordErr(span, err)
		return nil, fmt.Errorf("read metadata.json: %w", err)
	}

	var meta SessionMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		recordErr(span, err)
		return nil, fmt.Errorf("unmarshal metadata: %w", err)
	}
	meta.NormalizeAfterLoad()
	return &meta, nil
}

// LoadTranscript reads and returns all TranscriptEntries from
// sessions/<sessionID>/transcript.jsonl, sorted by Seq ascending.
func (s *Store) LoadTranscript(sessionID string) ([]TranscriptEntry, error) {
	_, span := startSpan(s.ctx, "service.Sessions.LoadTranscript",
		attrLoomSessionID(sessionID),
	)
	defer span.End()

	if err := validateSessionID(sessionID); err != nil {
		recordErr(span, err)
		return nil, err
	}

	txPath := filepath.Join(s.dir, sessionID, "transcript.jsonl")

	// #nosec G304 — sessionID validated above
	file, err := os.Open(txPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		recordErr(span, err)
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
		recordErr(span, err)
		return entries, fmt.Errorf("read transcript: %w", err)
	}

	// Sort by Seq ascending.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Seq < entries[j].Seq
	})

	span.SetAttributes(attrResultCount(len(entries)))
	return entries, nil
}

// ReadPrompt reads and returns the prompt text from
// sessions/<sessionID>/prompt.txt.
func (s *Store) ReadPrompt(sessionID string) (string, error) {
	_, span := startSpan(s.ctx, "service.Sessions.ReadPrompt",
		attrLoomSessionID(sessionID),
	)
	defer span.End()

	if err := validateSessionID(sessionID); err != nil {
		recordErr(span, err)
		return "", err
	}

	promptPath := filepath.Join(s.dir, sessionID, "prompt.txt")

	// #nosec G304 — sessionID validated above
	data, err := os.ReadFile(promptPath)
	if err != nil {
		recordErr(span, err)
		return "", fmt.Errorf("read prompt.txt: %w", err)
	}
	// Note: prompt content is NOT recorded as a span attribute — see
	// trace contract §6 (forbidden: prompt, prompt_template).
	return string(data), nil
}

// ReadDiff reads and returns the diff.patch content from
// sessions/<sessionID>/diff.patch.
// Returns os.ErrNotExist (wrapped) when no diff.patch exists for the session.
func (s *Store) ReadDiff(sessionID string) (string, error) {
	_, span := startSpan(s.ctx, "service.Sessions.ReadDiff",
		attrLoomSessionID(sessionID),
	)
	defer span.End()

	if err := validateSessionID(sessionID); err != nil {
		recordErr(span, err)
		return "", err
	}

	diffPath := filepath.Join(s.dir, sessionID, "diff.patch")

	// #nosec G304 — sessionID validated above
	data, err := os.ReadFile(diffPath)
	if err != nil {
		recordErr(span, err)
		return "", fmt.Errorf("read diff.patch: %w", err)
	}
	// Note: diff body is forbidden as a span attribute (§6: git.diff).
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
