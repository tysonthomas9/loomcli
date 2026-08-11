package sessions

import (
	"context"
	"fmt"
	"time"

	hwtranscript "github.com/olesho/harness-wrapper/pkg/transcript"
)

// Archive is Interaction's local filesystem adapter for runtime session
// evidence. Its interface exposes caller intents rather than the underlying
// Store's persistence primitives.
type Archive struct {
	store *Store
}

// OpenArchive opens the local session archive rooted beneath runtimeDir.
func OpenArchive(ctx context.Context, runtimeDir string) (*Archive, error) {
	store, err := NewStore(ctx, runtimeDir)
	if err != nil {
		return nil, err
	}
	return &Archive{store: store}, nil
}

// Begin records one running local session and returns its active handle.
func (archive *Archive) Begin(options CreateOptions) (*Session, error) {
	if archive == nil || archive.store == nil {
		return nil, fmt.Errorf("local session archive is unavailable")
	}
	return archive.store.CreateSession(options)
}

// TranscriptCapture describes one complete native transcript snapshot. A
// non-empty SubagentID selects the session's subagent transcript location.
type TranscriptCapture struct {
	SessionID  string
	SubagentID string
	SourcePath string
	Format     string
}

// Capture mirrors a backend-owned transcript snapshot into the archive.
func (archive *Archive) Capture(command TranscriptCapture) error {
	if archive == nil || archive.store == nil {
		return fmt.Errorf("local session archive is unavailable")
	}
	if command.SubagentID != "" {
		return archive.store.SyncSubagentTranscript(command.SessionID, command.SubagentID, command.SourcePath)
	}
	return archive.store.SyncNativeTranscript(command.SessionID, command.SourcePath, command.Format)
}

// MetadataUpdate replaces one session's metadata and optionally restores its
// index entry. Repair callers no longer coordinate those writes separately.
type MetadataUpdate struct {
	SessionID string
	Metadata  *SessionMetadata
	ReIndex   bool
}

// UpdateMetadata persists a complete metadata repair as one archive intent.
func (archive *Archive) UpdateMetadata(command MetadataUpdate) error {
	if archive == nil || archive.store == nil {
		return fmt.Errorf("local session archive is unavailable")
	}
	if err := archive.store.SaveMetadata(command.SessionID, command.Metadata); err != nil {
		return err
	}
	if command.ReIndex {
		return archive.store.ReIndex(command.Metadata.SessionRecord)
	}
	return nil
}

// RepairIndex restores one session's normalized metadata and index entry.
func (archive *Archive) RepairIndex(sessionID string) error {
	if archive == nil || archive.store == nil {
		return fmt.Errorf("local session archive is unavailable")
	}
	metadata, err := archive.store.LoadMetadata(sessionID)
	if err != nil {
		return err
	}
	metadata.NormalizeAfterLoad()
	return archive.UpdateMetadata(MetadataUpdate{
		SessionID: sessionID,
		Metadata:  metadata,
		ReIndex:   true,
	})
}

// CleanupOptions defines one archive retention operation. DryRun returns the
// exact purge and duplicate-index counts without modifying the archive.
type CleanupOptions struct {
	OlderThan time.Duration
	DryRun    bool
	Compact   bool
}

// CleanupResult reports the observable effects of archive maintenance.
type CleanupResult struct {
	Purged    int
	Compacted int
}

// Cleanup applies or previews session retention and optional index compaction.
func (archive *Archive) Cleanup(options CleanupOptions) (CleanupResult, error) {
	if archive == nil || archive.store == nil {
		return CleanupResult{}, fmt.Errorf("local session archive is unavailable")
	}
	if options.DryRun {
		return archive.previewCleanup(options.OlderThan)
	}
	purged, err := archive.store.PurgeOlderThan(options.OlderThan)
	if err != nil {
		return CleanupResult{Purged: purged}, err
	}
	result := CleanupResult{Purged: purged}
	if !options.Compact {
		return result, nil
	}
	result.Compacted, err = archive.store.CompactIndex()
	return result, err
}

func (archive *Archive) previewCleanup(age time.Duration) (CleanupResult, error) {
	records, err := archive.store.Query(Filter{})
	if err != nil {
		return CleanupResult{}, err
	}
	cutoff := time.Now().UTC().Add(-age)
	result := CleanupResult{}
	for _, record := range records {
		if record.Status != StatusRunning && record.EndedAt != nil && record.EndedAt.Before(cutoff) {
			result.Purged++
		}
	}
	total, unique, err := archive.store.CountIndexEntries()
	if err != nil {
		return result, err
	}
	result.Compacted = total - unique
	return result, nil
}

// AppendEnvelope appends one session-scoped harness event envelope.
func (archive *Archive) AppendEnvelope(sessionID string, envelope hwtranscript.EventEnvelope) error {
	if archive == nil || archive.store == nil {
		return fmt.Errorf("local session archive is unavailable")
	}
	return archive.store.AppendEnvelope(sessionID, envelope)
}

// LoadEnvelopes returns the deduplicated event log for one session.
func (archive *Archive) LoadEnvelopes(sessionID string) ([]hwtranscript.EventEnvelope, error) {
	if archive == nil || archive.store == nil {
		return nil, fmt.Errorf("local session archive is unavailable")
	}
	return archive.store.LoadEnvelopes(sessionID)
}

// Read-only projections used by diagnostics. Mutation remains behind the
// archive intents above.
func (archive *Archive) Dir() string { return archive.store.Dir() }

func (archive *Archive) NativeTranscriptPath(sessionID string) string {
	return archive.store.NativeTranscriptPath(sessionID)
}

func (archive *Archive) Query(filter Filter) ([]SessionRecord, error) {
	return archive.store.Query(filter)
}

func (archive *Archive) LoadMetadata(sessionID string) (*SessionMetadata, error) {
	return archive.store.LoadMetadata(sessionID)
}
