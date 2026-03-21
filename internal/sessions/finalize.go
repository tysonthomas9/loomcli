package sessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// Finalize completes a session by setting final metadata, writing it to disk,
// and appending the finalized SessionRecord to index.jsonl.
func (sess *Session) Finalize(opts FinalizeOptions) error {
	now := time.Now().UTC()

	// Set task and outcome fields.
	sess.Meta.TaskID = opts.TaskID
	sess.Meta.ExitCode = opts.ExitCode
	if opts.ExitCode == 0 {
		sess.Meta.Status = StatusCompleted
	} else {
		sess.Meta.Status = StatusFailed
	}
	sess.Meta.EndedAt = &now
	sess.Meta.DurationS = now.Sub(sess.Meta.StartedAt).Seconds()

	// Set diff stats.
	sess.Meta.FilesChanged = opts.DiffStats.FilesChanged
	sess.Meta.LinesAdded = opts.DiffStats.LinesAdded
	sess.Meta.LinesRemoved = opts.DiffStats.LinesRemoved
	sess.Meta.FilesTouched = opts.FilesTouched

	// Set token usage fields from opts if provided.
	sess.Meta.InputTokens = opts.InputTokens
	sess.Meta.OutputTokens = opts.OutputTokens
	sess.Meta.CacheReadTokens = opts.CacheReadTokens
	sess.Meta.CacheWriteTokens = opts.CacheWriteTokens
	sess.Meta.EstimatedCostUSD = opts.EstimatedCostUSD

	// Set error context.
	sess.Meta.ErrorClass = opts.ErrorClass

	// Write final metadata.json atomically.
	sessDir := filepath.Join(sess.store.dir, sess.Meta.SessionID)
	if err := writeMetadataAtomic(sessDir, sess.Meta); err != nil {
		return fmt.Errorf("write metadata.json: %w", err)
	}

	// Write diff.patch if diff data provided.
	if opts.DiffPatch != "" {
		diffPath := filepath.Join(sessDir, "diff.patch")
		// #nosec G306 — diff data is not sensitive
		if err := os.WriteFile(diffPath, []byte(opts.DiffPatch), 0o644); err != nil {
			return fmt.Errorf("write diff.patch: %w", err)
		}
	}

	// Append finalized record to index.jsonl.
	if err := sess.store.appendIndex(sess.Meta.SessionRecord); err != nil {
		return fmt.Errorf("append index: %w", err)
	}

	return nil
}

// appendIndex appends a SessionRecord as a JSON line to index.jsonl,
// using flock for concurrent safety. Follows the same pattern as
// usage/store.go Append.
func (s *Store) appendIndex(rec SessionRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal session record: %w", err)
	}
	data = append(data, '\n')

	indexPath := filepath.Join(s.dir, "index.jsonl")

	// #nosec G304 — controlled path from Store
	// #nosec G302 — index data is not sensitive
	f, err := os.OpenFile(indexPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open index file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if err := lockfile.FlockExclusiveBlocking(f); err != nil {
		return fmt.Errorf("flock index file: %w", err)
	}
	defer func() { _ = lockfile.FlockUnlock(f) }()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write index entry: %w", err)
	}
	return nil
}
