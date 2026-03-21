package sessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// AppendTranscript appends a single TranscriptEntry to
// sessions/<sessionID>/transcript.jsonl using flock for concurrency safety.
// The caller is responsible for setting the Seq field.
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

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal transcript entry: %w", err)
	}
	data = append(data, '\n')

	txPath := filepath.Join(sessDir, "transcript.jsonl")

	// #nosec G304 — path constructed from trusted store directory
	// #nosec G302 — transcript data is not sensitive
	f, err := os.OpenFile(txPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open transcript file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if err := lockfile.FlockExclusiveBlocking(f); err != nil {
		return fmt.Errorf("flock transcript file: %w", err)
	}
	defer func() { _ = lockfile.FlockUnlock(f) }()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write transcript entry: %w", err)
	}
	return nil
}
