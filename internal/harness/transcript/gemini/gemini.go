// Package gemini reads Gemini CLI session transcripts.
//
// Gemini CLI writes one JSONL per session at:
//
//	~/.gemini/tmp/<project>/chats/session-<YYYY-MM-DDTHH-MM>-<short-id>.jsonl
//
// where <project> is the short slug that ~/.gemini/projects.json maps
// the absolute working directory to (e.g. /Users/me/Work/aether →
// "aether"), and <short-id> is the first 8 hex chars of the session
// UUID.
//
// Schema notes — Gemini CLI v0.x:
//
//   - The first line is a session-header object carrying metadata only:
//     {"sessionId":"…","projectHash":"…","startTime":"…","kind":"main"}.
//     The reader skips it.
//   - Subsequent lines record turn content. Two shapes are observed:
//     {"role":"user"|"model","parts":[{"text":"…"}], "timestamp":"…"}
//     and {"type":"user"|"assistant","message":"…","timestamp":"…"}.
//     This reader accepts either.
//
// User-typed slash commands (/exit, /chat save, …) are additionally
// surfaced in ~/.gemini/tmp/<project>/logs.json — this reader ignores
// that file; it's a flat command log, not a turn-by-turn transcript.
package gemini

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/harness/transcript"
)

// Reader implements transcript.Reader for Gemini CLI.
type Reader struct {
	// GeminiRoot overrides the default ~/.gemini location. Empty means
	// use the default.
	GeminiRoot string
}

// New constructs a Gemini transcript Reader.
func New() *Reader { return &Reader{} }

// Read returns the ordered list of turns for the given Gemini session
// UUID. workingDir is required: Gemini indexes session files by
// per-project slug under ~/.gemini/tmp/.
func (r *Reader) Read(harnessSessionID, workingDir string) ([]transcript.Turn, error) {
	if harnessSessionID == "" {
		return nil, fmt.Errorf("gemini transcript: empty session id")
	}
	path, err := r.locate(harnessSessionID, workingDir)
	if err != nil {
		return nil, err
	}
	return parseJSONL(path)
}

func (r *Reader) geminiRoot() (string, error) {
	if r.GeminiRoot != "" {
		return r.GeminiRoot, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("gemini transcript: resolve home: %w", err)
	}
	return filepath.Join(home, ".gemini"), nil
}

// ProjectSlug returns the short slug Gemini CLI assigns to a working
// directory, looked up in ~/.gemini/projects.json. Returns ("", false)
// if the working directory is not mapped (e.g. the user has never
// launched gemini there) or the projects.json file is missing.
func (r *Reader) ProjectSlug(workingDir string) (string, bool, error) {
	root, err := r.geminiRoot()
	if err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(filepath.Join(root, "projects.json")) //nolint:gosec // G304: root is derived from the gemini transcript root resolver; reading its registry file is the documented purpose.
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("gemini transcript: read projects.json: %w", err)
	}
	var projects struct {
		Projects map[string]string `json:"projects"`
	}
	if err := json.Unmarshal(data, &projects); err != nil {
		return "", false, fmt.Errorf("gemini transcript: parse projects.json: %w", err)
	}
	slug, ok := projects.Projects[workingDir]
	if !ok || slug == "" {
		return "", false, nil
	}
	return slug, true, nil
}

// locate resolves the session file path for harnessSessionID. It first
// consults the projects.json mapping; failing that it walks every
// ~/.gemini/tmp/*/chats/ directory looking for a file whose name
// matches the session UUID's first 8 chars. The walk fallback covers
// the case where projects.json has not been written yet (first run in
// a directory).
func (r *Reader) locate(sessionID, workingDir string) (string, error) {
	root, err := r.geminiRoot()
	if err != nil {
		return "", err
	}

	short := sessionShort(sessionID)
	suffix := "-" + short + ".jsonl"

	if workingDir != "" {
		slug, ok, err := r.ProjectSlug(workingDir)
		if err != nil {
			return "", err
		}
		if ok {
			candidate, found, err := findInChats(filepath.Join(root, "tmp", slug, "chats"), sessionID, suffix)
			if err != nil {
				return "", err
			}
			if found {
				return candidate, nil
			}
		}
	}

	tmpRoot := filepath.Join(root, "tmp")
	entries, err := os.ReadDir(tmpRoot)
	if err != nil {
		return "", fmt.Errorf("gemini transcript: read %s: %w", tmpRoot, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate, found, err := findInChats(filepath.Join(tmpRoot, e.Name(), "chats"), sessionID, suffix)
		if err != nil {
			return "", err
		}
		if found {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("gemini transcript: no session file for %s under %s", sessionID, tmpRoot)
}

// findInChats looks for the session file in a single chats/ directory.
// It first tries a fast filename-suffix match (session-…-<short>.jsonl)
// and, on a match, confirms the embedded sessionId in the header line.
// Returns ("", false, nil) when no file matches; surfaces IO errors
// other than ENOENT for the chats/ dir itself.
func findInChats(chatsDir, sessionID, suffix string) (string, bool, error) {
	entries, err := os.ReadDir(chatsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("gemini transcript: read %s: %w", chatsDir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, suffix) {
			continue
		}
		path := filepath.Join(chatsDir, name)
		if confirmHeader(path, sessionID) {
			return path, true, nil
		}
	}
	return "", false, nil
}

// confirmHeader opens path, reads the first line, and returns true when
// it carries the expected sessionId. Defensive against shared-prefix
// short IDs across concurrent sessions.
func confirmHeader(path, sessionID string) bool {
	f, err := os.Open(path) //nolint:gosec // G304: caller-supplied gemini transcript path is the function's documented purpose.
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)
	if !sc.Scan() {
		return false
	}
	var hdr struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(sc.Bytes(), &hdr); err != nil {
		return false
	}
	return hdr.SessionID == sessionID
}

// sessionShort returns the first 8 hex chars of a session UUID, matching
// the convention Gemini uses to build session filenames.
func sessionShort(id string) string {
	if i := strings.IndexByte(id, '-'); i >= 0 {
		id = id[:i]
	}
	if len(id) > 8 {
		id = id[:8]
	}
	return id
}

// jsonlLine is the union of observed turn-line shapes. Fields are
// optional; the parser inspects which set is populated.
type jsonlLine struct {
	// Header-only fields. When SessionID is set and Role/Type are not,
	// the line is metadata; skip.
	SessionID   string `json:"sessionId,omitempty"`
	ProjectHash string `json:"projectHash,omitempty"`
	Kind        string `json:"kind,omitempty"`

	// Shape A: Gemini API-style content.
	Role  string `json:"role,omitempty"`
	Parts []struct {
		Text string `json:"text,omitempty"`
	} `json:"parts,omitempty"`

	// Shape B: CLI-internal "type"/"message" form.
	Type    string          `json:"type,omitempty"`
	Message json.RawMessage `json:"message,omitempty"`

	Timestamp string `json:"timestamp,omitempty"`
}

func parseJSONL(path string) ([]transcript.Turn, error) {
	f, err := os.Open(path) //nolint:gosec // G304: caller-supplied gemini transcript path is the function's documented purpose.
	if err != nil {
		return nil, fmt.Errorf("gemini transcript: open %s: %w", path, err)
	}
	defer f.Close()

	out := make([]transcript.Turn, 0, 32)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var ln jsonlLine
		if err := json.Unmarshal(raw, &ln); err != nil {
			return nil, fmt.Errorf("gemini transcript: parse line %d in %s: %w", lineNo, path, err)
		}
		// Skip the metadata header.
		if ln.Kind != "" && ln.Role == "" && ln.Type == "" {
			continue
		}
		role := normalizeRole(ln.Role, ln.Type)
		if role == "" {
			continue
		}
		text := extractText(&ln)
		if text == "" {
			continue
		}
		var ts time.Time
		if ln.Timestamp != "" {
			ts, _ = time.Parse(time.RFC3339, ln.Timestamp)
		}
		out = append(out, transcript.Turn{
			Role:      role,
			Text:      text,
			Timestamp: ts,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("gemini transcript: scan %s: %w", path, err)
	}
	return out, nil
}

// normalizeRole maps observed role/type strings to the transcript
// vocabulary. Gemini uses "model" where other harnesses use
// "assistant".
func normalizeRole(role, typ string) string {
	switch role {
	case "user":
		return "user"
	case "model", "assistant":
		return "assistant"
	case "system":
		return "system"
	}
	switch typ {
	case "user":
		return "user"
	case "model", "assistant":
		return "assistant"
	case "system", "tool":
		return "system"
	}
	return ""
}

func extractText(ln *jsonlLine) string {
	if len(ln.Parts) > 0 {
		var parts []string
		for _, p := range ln.Parts {
			if p.Text != "" {
				parts = append(parts, p.Text)
			}
		}
		return strings.Join(parts, "\n\n")
	}
	if len(ln.Message) == 0 {
		return ""
	}
	if ln.Message[0] == '"' {
		var s string
		if err := json.Unmarshal(ln.Message, &s); err == nil {
			return s
		}
		return ""
	}
	return ""
}
