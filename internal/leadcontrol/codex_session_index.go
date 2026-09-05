package leadcontrol

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CodexSessionIndexFile is the file codex keeps under its CODEX_HOME, one JSON
// object per line, naming every thread it has seen.
const CodexSessionIndexFile = "session_index.jsonl"

// codexSessionIndexMaxLine caps a single index line. A thread name is a short
// summary, so anything past this is corruption rather than data, and the cap
// keeps a damaged file from being read into memory whole.
const codexSessionIndexMaxLine = 1 << 20

// CodexSessionIndexEntry is one thread codex has recorded: its thread id, the
// human-readable name codex derived for it, and when codex last touched it.
type CodexSessionIndexEntry struct {
	ID         string    `json:"id"`
	ThreadName string    `json:"thread_name"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// codexSessionIndexLine mirrors the on-disk shape. It is decoded separately
// from CodexSessionIndexEntry so a line with an unparseable timestamp still
// yields a usable thread name instead of discarding the whole record.
type codexSessionIndexLine struct {
	ID         string `json:"id"`
	ThreadName string `json:"thread_name"`
	UpdatedAt  string `json:"updated_at"`
}

// ReadCodexSessionIndex reads codex's thread index under codexHome, keyed by
// thread id. Later lines win, which is how codex itself renames a thread: it
// appends a new record rather than rewriting the old one.
//
// This is DECORATION ONLY. It is read to put a human-readable name beside a
// thread id in `loom lead --list-sessions`, and it must never be consulted to
// resolve a resume target: it is codex's own file, so it lists threads loom
// never launched and it is not authoritative about anything loom recorded.
//
// It is total by construction. A missing CODEX_HOME, a missing index, a
// malformed line and a half-written final line (codex appends while lead
// reads) all yield whatever was readable and no error — a cosmetic column
// must never fail a listing.
func ReadCodexSessionIndex(codexHome string) (map[string]CodexSessionIndexEntry, error) {
	entries := map[string]CodexSessionIndexEntry{}
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return entries, nil
	}

	file, err := os.Open(filepath.Join(codexHome, CodexSessionIndexFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return entries, nil
		}
		return entries, err
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), codexSessionIndexMaxLine)
	for scanner.Scan() {
		entry, ok := parseCodexSessionIndexLine(scanner.Bytes())
		if !ok {
			continue
		}
		entries[entry.ID] = entry
	}
	// A scan error is a truncated or over-long tail, not a failure: everything
	// before it was read and is worth returning.
	return entries, nil
}

// parseCodexSessionIndexLine decodes one line, reporting whether it carried a
// usable id. Blank lines, non-JSON lines and records with no id are skipped.
func parseCodexSessionIndexLine(line []byte) (CodexSessionIndexEntry, bool) {
	if len(strings.TrimSpace(string(line))) == 0 {
		return CodexSessionIndexEntry{}, false
	}
	var raw codexSessionIndexLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return CodexSessionIndexEntry{}, false
	}
	id := strings.TrimSpace(raw.ID)
	if id == "" {
		return CodexSessionIndexEntry{}, false
	}
	entry := CodexSessionIndexEntry{ID: id, ThreadName: strings.TrimSpace(raw.ThreadName)}
	if ts := strings.TrimSpace(raw.UpdatedAt); ts != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			entry.UpdatedAt = parsed
		}
	}
	return entry, true
}

// CodexThreadName returns the recorded name for threadID, or "" when the index
// has nothing for it. Nil-safe so callers can pass an index they never loaded.
func CodexThreadName(index map[string]CodexSessionIndexEntry, threadID string) string {
	threadID = strings.TrimSpace(threadID)
	if index == nil || threadID == "" {
		return ""
	}
	return index[threadID].ThreadName
}
