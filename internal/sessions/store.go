package sessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// Store provides session storage under a beadsDir/sessions/ directory.
// Each session gets its own subdirectory containing metadata.json,
// transcript.jsonl, and prompt.txt.
type Store struct {
	dir string // absolute path to <beadsDir>/sessions/
}

// NewStore creates a Store rooted at beadsDir/sessions/.
// It creates the sessions/ directory if it does not exist.
func NewStore(beadsDir string) (*Store, error) {
	dir := filepath.Join(beadsDir, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create sessions dir: %w", err)
	}
	return &Store{dir: dir}, nil
}

// CreateSession initializes a new session directory with prompt.txt and
// metadata.json (status=running). Returns a Session handle for the caller
// to use during the agent run.
func (s *Store) CreateSession(opts CreateOptions) (*Session, error) {
	// Derive a short task identifier (empty string is fine — design allows it).
	taskShort := ""

	sid, err := GenerateSessionID(opts.AgentName, taskShort)
	if err != nil {
		return nil, fmt.Errorf("generate session ID: %w", err)
	}

	sessDir := filepath.Join(s.dir, sid)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}

	// Write prompt.txt.
	promptPath := filepath.Join(sessDir, "prompt.txt")
	// #nosec G306 — prompt text is not sensitive
	if err := os.WriteFile(promptPath, []byte(opts.Prompt), 0o644); err != nil {
		return nil, fmt.Errorf("write prompt.txt: %w", err)
	}

	// Build initial metadata.
	now := time.Now().UTC()
	meta := SessionMetadata{
		SessionRecord: SessionRecord{
			SessionID:  sid,
			EpicID:     opts.EpicID,
			AgentName:  opts.AgentName,
			Backend:    opts.Backend,
			Phase:      opts.Phase,
			StartedAt:  now,
			Status:     StatusRunning,
			AttemptNum: opts.AttemptNum,
		},
	}

	// Write metadata.json atomically (temp + rename).
	if err := writeMetadataAtomic(sessDir, meta); err != nil {
		return nil, fmt.Errorf("write metadata.json: %w", err)
	}

	// Write running record to index.jsonl so active sessions are queryable.
	if err := s.appendIndex(meta.SessionRecord); err != nil {
		// Non-fatal — session dir is created, just won't appear in queries until finalize.
		fmt.Fprintf(os.Stderr, "sessions: warning: failed to write running index entry: %v\n", err)
	}

	return &Session{store: s, Meta: meta}, nil
}

// AppendTranscript appends a single TranscriptEntry to
// sessions/<sessionID>/transcript.jsonl using flock for concurrency safety.
// The Seq field is auto-assigned from a counter file (seq) in the session
// directory, ensuring monotonic ordering even across concurrent processes.
func (s *Store) AppendTranscript(sessionID string, entry TranscriptEntry) error {
	// Reject session IDs containing path separators to prevent traversal.
	if strings.ContainsAny(sessionID, "/\\") {
		return fmt.Errorf("invalid session ID %q: contains path separator", sessionID)
	}

	sessDir := filepath.Join(s.dir, sessionID)

	// Verify the resolved path is still under the store directory.
	cleanDir := filepath.Clean(sessDir)
	if !strings.HasPrefix(cleanDir+string(os.PathSeparator), filepath.Clean(s.dir)+string(os.PathSeparator)) {
		return fmt.Errorf("invalid session ID %q", sessionID)
	}

	// Verify the session directory exists.
	if _, err := os.Stat(sessDir); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("session %q does not exist", sessionID)
		}
		return fmt.Errorf("stat session dir: %w", err)
	}

	txPath := filepath.Join(sessDir, "transcript.jsonl")

	// #nosec G304 — path constructed from trusted store directory
	f, err := os.OpenFile(txPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open transcript file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if err := lockfile.FlockExclusiveBlocking(f); err != nil {
		return fmt.Errorf("flock transcript file: %w", err)
	}
	defer func() { _ = lockfile.FlockUnlock(f) }()

	// Auto-assign Seq from a counter file, under the flock.
	seq := readAndIncrementSeq(sessDir)
	entry.Seq = seq

	// Marshal with the assigned seq.
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal transcript entry: %w", err)
	}
	data = append(data, '\n')

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write transcript entry: %w", err)
	}
	return nil
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

	// #nosec G306 — seq file is not sensitive
	_ = os.WriteFile(seqPath, []byte(strconv.Itoa(seq)), 0o644)
	return seq
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

	// #nosec G306 — metadata is not sensitive
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write metadata tmp: %w", err)
	}

	if err := os.Rename(tmpPath, metaPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename metadata: %w", err)
	}

	return nil
}
