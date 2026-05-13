package sessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
	"github.com/tysonthomas9/loomcli/internal/runtimectx"
)

// Finalize completes a session by setting final metadata, writing it to disk,
// and appending the finalized SessionRecord to index.jsonl.
func (s *Session) Finalize(opts FinalizeOptions) error {
	_, span := startSpan(runtimectx.RootContext(), "service.Sessions.Finalize",
		attrLoomSessionID(s.Meta.SessionID),
		attrLoomAgent(s.Meta.AgentName),
		attrLoomTaskID(opts.TaskID),
	)
	defer span.End()

	now := time.Now().UTC()

	// Set task and outcome fields.
	s.Meta.TaskID = opts.TaskID
	s.Meta.ExitCode = opts.ExitCode
	if opts.ExitCode == 0 {
		s.Meta.Status = StatusCompleted
	} else {
		s.Meta.Status = StatusFailed
	}
	s.Meta.EndedAt = &now
	s.Meta.DurationS = now.Sub(s.Meta.StartedAt).Seconds()

	// Set diff stats.
	s.Meta.FilesChanged = opts.DiffStats.FilesChanged
	s.Meta.LinesAdded = opts.DiffStats.LinesAdded
	s.Meta.LinesRemoved = opts.DiffStats.LinesRemoved
	s.Meta.FilesTouched = opts.FilesTouched

	// Per-field token merge: for each field, use opts value when non-zero,
	// otherwise preserve the existing disk value (hook-captured data).
	var diskInputTokens, diskOutputTokens, diskCacheRead, diskCacheWrite int64
	var diskCost float64
	diskMeta, err := s.store.LoadMetadata(s.Meta.SessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sessions: failed to load metadata for token preservation: %v\n", err)
		// disk* vars remain zero — merge will use opts values
	} else {
		diskInputTokens = diskMeta.InputTokens
		diskOutputTokens = diskMeta.OutputTokens
		diskCacheRead = diskMeta.CacheReadTokens
		diskCacheWrite = diskMeta.CacheWriteTokens
		diskCost = diskMeta.EstimatedCostUSD
	}
	s.Meta.InputTokens = mergeTokenVal(opts.InputTokens, diskInputTokens)
	s.Meta.OutputTokens = mergeTokenVal(opts.OutputTokens, diskOutputTokens)
	s.Meta.CacheReadTokens = mergeTokenVal(opts.CacheReadTokens, diskCacheRead)
	s.Meta.CacheWriteTokens = mergeTokenVal(opts.CacheWriteTokens, diskCacheWrite)
	s.Meta.EstimatedCostUSD = mergeTokenCost(opts.EstimatedCostUSD, diskCost)

	// Set error context.
	s.Meta.ErrorClass = opts.ErrorClass

	// Ensure schema version is current before writing to disk.
	normalizeRecord(&s.Meta.SessionRecord)

	// Write final metadata.json atomically.
	sessDir := filepath.Join(s.store.dir, s.Meta.SessionID)
	if err := writeMetadataAtomic(sessDir, s.Meta); err != nil {
		recordErr(span, err)
		return fmt.Errorf("write metadata.json: %w", err)
	}

	// Write diff.patch if diff data provided.
	if opts.DiffPatch != "" {
		diffPath := filepath.Join(sessDir, "diff.patch")
		if err := os.WriteFile(diffPath, []byte(opts.DiffPatch), sessFilePerm); err != nil {
			recordErr(span, err)
			return fmt.Errorf("write diff.patch: %w", err)
		}
	}

	// Append finalized record to index.jsonl.
	if err := s.store.appendIndex(s.Meta.SessionRecord); err != nil {
		recordErr(span, err)
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
	f, err := os.OpenFile(indexPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, sessFilePerm)
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

// ReIndex appends a SessionRecord to index.jsonl. Used by doctor --fix
// to re-index orphaned session directories that exist on disk but are
// missing from the index.
func (s *Store) ReIndex(rec SessionRecord) error {
	_, span := startSpan(runtimectx.RootContext(), "service.Sessions.ReIndex",
		attrLoomSessionID(rec.SessionID),
	)
	defer span.End()

	if err := s.appendIndex(rec); err != nil {
		recordErr(span, err)
		return err
	}
	return nil
}

// mergeTokenVal returns opts if non-zero, otherwise disk.
func mergeTokenVal(opts, disk int64) int64 {
	if opts != 0 {
		return opts
	}
	return disk
}

// mergeTokenCost returns opts if non-zero, otherwise disk.
func mergeTokenCost(opts, disk float64) float64 {
	if opts != 0 {
		return opts
	}
	return disk
}
