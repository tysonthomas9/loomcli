package trigger

// File-backed cursor persistence for the issue-journal bridge (A4-3). The
// bridge's IssueJournalCursorStore is satisfied by an in-memory map by default;
// serve injects this file-backed implementation so the per-workspace resume
// cursor survives a restart.
//
// WHY A LOST FILE IS SAFE. The bridge derives deterministic loopback event ids
// (fleet-journal-{streamID}) so every re-emission of a journal entry dedups in
// the dispatch path (see IssueJournalBridge's package doc). The cursor is
// therefore an OPTIMIZATION, not a correctness boundary: a missing or unreadable
// file only costs a dedup-absorbed rescan from the journal start, never
// duplicate triage. That is why Save tolerates write failures (logged, not
// fatal) and a missing file loads as empty rather than erroring — only a present
// but corrupt file surfaces an error to the caller, so an operator notices a
// genuinely broken state file instead of silently losing the cursor on every
// boot.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/atomicfile"
)

// issueJournalCursorFilePerm is the mode for the cursor state file: owner
// read/write only, matching loom's other per-user state files.
const issueJournalCursorFilePerm = 0o600

// FileIssueJournalCursorStore is a file-backed IssueJournalCursorStore: a
// per-workspace cursor map persisted as JSON and rewritten atomically
// (tmp+rename via atomicfile) after each Save. It is safe for concurrent use.
//
// The in-memory map is authoritative for Load after construction; the file is
// read once at construction and rewritten in full on every Save, so the two
// never diverge within a process. Save failures are logged and swallowed
// because a lost cursor is recoverable (see the package-level rationale).
type FileIssueJournalCursorStore struct {
	path   string
	logger *slog.Logger

	mu      sync.Mutex
	cursors map[string]string
}

// NewFileIssueJournalCursorStore loads the cursor map from path and returns a
// ready store. A missing file is the normal first-boot case and loads as an
// empty map (no error). A present but unparseable file surfaces an error so a
// genuinely corrupt state file is noticed rather than silently truncated on the
// next Save. A nil logger falls back to slog.Default.
func NewFileIssueJournalCursorStore(path string, logger *slog.Logger) (*FileIssueJournalCursorStore, error) {
	if logger == nil {
		logger = slog.Default()
	}
	cursors, err := loadIssueJournalCursors(path)
	if err != nil {
		return nil, err
	}
	return &FileIssueJournalCursorStore{path: path, logger: logger, cursors: cursors}, nil
}

// loadIssueJournalCursors reads and decodes the cursor map. A missing file
// yields an empty map and no error; a corrupt file yields an error.
func loadIssueJournalCursors(path string) (map[string]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is the operator-configured serve state file (LoomDir or LOOM_ISSUE_BRIDGE_STATE_PATH), not user input.
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read issue journal cursor state %q: %w", path, err)
	}
	cursors := map[string]string{}
	if len(data) == 0 {
		return cursors, nil
	}
	if err := json.Unmarshal(data, &cursors); err != nil {
		return nil, fmt.Errorf("decode issue journal cursor state %q: %w", path, err)
	}
	return cursors, nil
}

// Load returns the stored cursor for the workspace and whether one exists.
// found=false signals the bridge's first observation of the workspace (its
// bootstrap fast-forward / replay decision).
func (s *FileIssueJournalCursorStore) Load(ws string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cursor, ok := s.cursors[ws]
	return cursor, ok
}

// Save records the workspace's resume cursor and rewrites the whole map
// atomically. A write failure is logged and swallowed: the in-memory map still
// advances so the running process polls only the tail, and the cursor is
// recoverable on the next clean write (or, in the worst case, a dedup-absorbed
// rescan after restart).
func (s *FileIssueJournalCursorStore) Save(ws, cursor string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cursors[ws] = cursor
	if err := s.persistLocked(); err != nil {
		s.logger.Error("issue journal bridge: persist cursor state failed (cursor kept in memory)",
			"path", s.path, "workspace", ws, "err", err)
	}
}

// persistLocked marshals the cursor map and writes it atomically. Callers hold
// s.mu. The map is encoded sorted-key-deterministic by encoding/json so the
// on-disk bytes are stable across writes with identical content.
func (s *FileIssueJournalCursorStore) persistLocked() error {
	data, err := json.Marshal(s.cursors)
	if err != nil {
		return fmt.Errorf("encode issue journal cursor state: %w", err)
	}
	if err := atomicfile.WriteFile(s.path, data, issueJournalCursorFilePerm); err != nil {
		return err
	}
	return nil
}
