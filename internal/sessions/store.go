package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

const (
	sessDirPerm  = 0o700
	sessFilePerm = 0o600
)

// Store provides session storage under a runtimeDir/sessions/ directory.
// Each session gets its own subdirectory containing metadata.json,
// transcript.jsonl, and prompt.txt.
type Store struct {
	ctx context.Context
	dir string // absolute path to <runtimeDir>/sessions/
}

// Dir returns the absolute path to the sessions directory.
func (s *Store) Dir() string { return s.dir }

// SessionDir returns the absolute path to a single session's directory
// (<runtimeDir>/sessions/<sessionID>), which holds prompt.txt, metadata.json,
// the native transcript, and the event store's events.jsonl. It is the single
// source of truth both the event-store WRITER (the OnEvent sink) and the READER
// (transcript serving) resolve through, so the two can't diverge. No existence
// check (cf. resolveSessionDir).
func (s *Store) SessionDir(sessionID string) string {
	return filepath.Join(s.dir, sessionID)
}

// NewStore creates a Store rooted at runtimeDir/sessions/.
// It creates the sessions/ directory if it does not exist.
func NewStore(ctx context.Context, runtimeDir string) (*Store, error) {
	if ctx == nil {
		return nil, errors.New("sessions: context is required")
	}
	_, span := startSpan(ctx, "service.Sessions.NewStore")
	defer span.End()

	dir := filepath.Join(runtimeDir, "sessions")
	if err := os.MkdirAll(dir, sessDirPerm); err != nil {
		recordErr(span, err)
		return nil, fmt.Errorf("create sessions dir: %w", err)
	}
	return &Store{ctx: ctx, dir: dir}, nil
}

// CreateSession initializes a new session directory with prompt.txt and
// metadata.json (status=running). Returns a Session handle for the caller
// to use during the agent run.
func (s *Store) CreateSession(opts CreateOptions) (*Session, error) {
	_, span := startSpan(s.ctx, "service.Sessions.CreateSession",
		attrLoomAgent(opts.AgentName),
		attrLoomBackend(opts.Backend),
	)
	defer span.End()

	// Derive a short task identifier (empty string is fine — design allows it).
	taskShort := ""

	sid, err := GenerateSessionID(opts.AgentName, taskShort)
	if err != nil {
		recordErr(span, err)
		return nil, fmt.Errorf("generate session ID: %w", err)
	}
	span.SetAttributes(attrLoomSessionID(sid))

	sessDir := filepath.Join(s.dir, sid)
	if err := os.MkdirAll(sessDir, sessDirPerm); err != nil {
		recordErr(span, err)
		return nil, fmt.Errorf("create session dir: %w", err)
	}

	// Write prompt.txt.
	promptPath := filepath.Join(sessDir, "prompt.txt")
	if err := os.WriteFile(promptPath, []byte(opts.Prompt), sessFilePerm); err != nil {
		recordErr(span, err)
		return nil, fmt.Errorf("write prompt.txt: %w", err)
	}

	meta := initialSessionMetadata(sid, opts)
	if err := writeMetadataAtomic(sessDir, meta); err != nil {
		recordErr(span, err)
		return nil, fmt.Errorf("write metadata.json: %w", err)
	}
	if err := s.appendIndex(meta.SessionRecord); err != nil {
		// Non-fatal — session dir is created, just won't appear in queries until finalize.
		fmt.Fprintf(os.Stderr, "sessions: warning: failed to write running index entry: %v\n", err)
	}
	return &Session{store: s, Meta: meta}, nil
}

func initialSessionMetadata(sid string, opts CreateOptions) SessionMetadata {
	return SessionMetadata{
		SessionRecord: SessionRecord{
			SchemaVersion: CurrentSchemaVersion,
			SessionID:     sid,
			TaskID:        opts.TaskID,
			EpicID:        opts.EpicID,
			AgentName:     opts.AgentName,
			Backend:       opts.Backend,
			Phase:         opts.Phase,
			StartedAt:     time.Now().UTC(),
			Status:        StatusRunning,
			AttemptNum:    opts.AttemptNum,
		},
	}
}

// UpdatePrompt replaces prompt.txt for an existing session. This lets a
// daemon-created parent session be filled by the child CLI after it renders the
// final role/task prompt.
func (s *Store) UpdatePrompt(sessionID, prompt string) error {
	_, span := startSpan(s.ctx, "service.Sessions.UpdatePrompt",
		attrLoomSessionID(sessionID),
	)
	defer span.End()

	if err := validateSessionID(sessionID); err != nil {
		recordErr(span, err)
		return err
	}
	sessDir := filepath.Join(s.dir, sessionID)
	if _, err := os.Stat(sessDir); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("session %q does not exist", sessionID)
		}
		recordErr(span, err)
		return fmt.Errorf("stat session dir: %w", err)
	}
	promptPath := filepath.Join(sessDir, "prompt.txt")
	if err := os.WriteFile(promptPath, []byte(prompt), sessFilePerm); err != nil {
		recordErr(span, err)
		return fmt.Errorf("write prompt.txt: %w", err)
	}
	return nil
}

// AppendTranscript appends a single TranscriptEntry to
// sessions/<sessionID>/transcript.jsonl using flock for concurrency safety.
// The Seq field is auto-assigned from a counter file (seq) in the session
// directory, ensuring monotonic ordering even across concurrent processes.
func (s *Store) AppendTranscript(sessionID string, entry TranscriptEntry) error {
	_, span := startSpan(s.ctx, "service.Sessions.AppendTranscript",
		attrLoomSessionID(sessionID),
	)
	defer span.End()

	sessDir, err := s.resolveSessionDir(sessionID)
	if err != nil {
		recordErr(span, err)
		return err
	}

	txPath := filepath.Join(sessDir, "transcript.jsonl")
	// #nosec G304 — path constructed from trusted store directory
	f, err := os.OpenFile(txPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, sessFilePerm)
	if err != nil {
		recordErr(span, err)
		return fmt.Errorf("open transcript file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if err := lockfile.FlockExclusiveBlocking(f); err != nil {
		recordErr(span, err)
		return fmt.Errorf("flock transcript file: %w", err)
	}
	defer func() { _ = lockfile.FlockUnlock(f) }()

	entry.Seq = readAndIncrementSeq(sessDir)
	data, err := json.Marshal(entry)
	if err != nil {
		recordErr(span, err)
		return fmt.Errorf("marshal transcript entry: %w", err)
	}
	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		recordErr(span, err)
		return fmt.Errorf("write transcript entry: %w", err)
	}
	return nil
}

// resolveSessionDir validates sessionID against path traversal and verifies
// the session directory exists. Returns the cleaned directory path.
func (s *Store) resolveSessionDir(sessionID string) (string, error) {
	if strings.ContainsAny(sessionID, "/\\") {
		return "", fmt.Errorf("invalid session ID %q: contains path separator", sessionID)
	}
	sessDir := filepath.Join(s.dir, sessionID)
	cleanDir := filepath.Clean(sessDir)
	if !strings.HasPrefix(cleanDir+string(os.PathSeparator), filepath.Clean(s.dir)+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid session ID %q", sessionID)
	}
	if _, err := os.Stat(sessDir); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("session %q does not exist", sessionID)
		}
		return "", fmt.Errorf("stat session dir: %w", err)
	}
	return sessDir, nil
}

// readAndIncrementSeq reads the current sequence number from sessDir/seq,
// increments it, writes it back, and returns the new value.
// Starts at 1 if the file doesn't exist. Best-effort: returns 0 on error.
func readAndIncrementSeq(sessDir string) int {
	seqPath := filepath.Join(sessDir, "seq")
	seq := 1

	// #nosec G304 — path constructed from trusted session directory
	data, err := os.ReadFile(seqPath)
	if err == nil {
		if n, parseErr := strconv.Atoi(strings.TrimSpace(string(data))); parseErr == nil {
			seq = n + 1
		}
	}

	_ = os.WriteFile(seqPath, []byte(strconv.Itoa(seq)), sessFilePerm)
	return seq
}

// SaveMetadata writes the given SessionMetadata to disk atomically for the
// specified session. This is used by hook handlers to patch metadata (e.g.,
// token usage) outside of the normal Finalize flow.
func (s *Store) SaveMetadata(sessionID string, meta *SessionMetadata) error {
	_, span := startSpan(s.ctx, "service.Sessions.SaveMetadata",
		attrLoomSessionID(sessionID),
	)
	defer span.End()

	if err := validateSessionID(sessionID); err != nil {
		recordErr(span, err)
		return err
	}
	sessDir := filepath.Join(s.dir, sessionID)
	if err := writeMetadataAtomic(sessDir, *meta); err != nil {
		recordErr(span, err)
		return err
	}
	return nil
}

// writeMetadataAtomic writes metadata.json using temp file + rename
// to prevent partial reads.
func writeMetadataAtomic(sessDir string, meta SessionMetadata) error {
	metaPath := filepath.Join(sessDir, "metadata.json")
	tmpPath := metaPath + ".tmp"

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	if err := os.WriteFile(tmpPath, data, sessFilePerm); err != nil {
		return fmt.Errorf("write metadata tmp: %w", err)
	}

	if err := os.Rename(tmpPath, metaPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename metadata: %w", err)
	}

	return nil
}
