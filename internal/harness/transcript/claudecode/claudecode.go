// Package claudecode reads Claude Code session transcripts.
//
// Claude Code writes one JSONL per session at:
//
//	~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl
//
// where <encoded-cwd> is the absolute working directory with every
// '/' replaced by '-' (so /Users/.../harness-wrapper becomes
// "-Users-...-harness-wrapper").
//
// Schema (excerpt) — the keys this reader consumes:
//
//	{"type":"user",      "message":{"role":"user",      "content":"text..."},
//	 "sessionId":"...", "timestamp":"2026-05-14T...Z"}
//
//	{"type":"assistant", "message":{"role":"assistant", "content":[
//	    {"type":"text","text":"..."}]}, "timestamp":"..."}
//
// Other line types (permission-mode, file-history-snapshot, attachment,
// system, ai-title) are skipped.
package claudecode

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

// Reader implements transcript.Reader for Claude Code.
type Reader struct {
	// ProjectsRoot overrides the default ~/.claude/projects/ location.
	// Empty means use the default.
	ProjectsRoot string
}

// New constructs a Claude Code transcript Reader.
func New() *Reader { return &Reader{} }

// Read returns the ordered list of turns for the given Claude Code
// session UUID. workingDir is required: Claude Code indexes
// transcripts by working directory.
func (r *Reader) Read(harnessSessionID, workingDir string) ([]transcript.Turn, error) {
	if harnessSessionID == "" {
		return nil, fmt.Errorf("claudecode transcript: empty session id")
	}
	if workingDir == "" {
		return nil, fmt.Errorf("claudecode transcript: empty working dir")
	}
	path, err := r.locate(harnessSessionID, workingDir)
	if err != nil {
		return nil, err
	}
	return parseJSONL(path)
}

// EncodedCWD returns the directory-name-encoding Claude Code uses
// for project paths: each '/' becomes '-'. Exposed for tests and for
// callers that want to map a working directory to its on-disk slot.
func EncodedCWD(workingDir string) string {
	// Claude Code: a leading slash produces a leading hyphen.
	return strings.ReplaceAll(workingDir, "/", "-")
}

func (r *Reader) projectsRoot() (string, error) {
	if r.ProjectsRoot != "" {
		return r.ProjectsRoot, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("claudecode transcript: resolve home: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

func (r *Reader) locate(sessionID, workingDir string) (string, error) {
	root, err := r.projectsRoot()
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, EncodedCWD(workingDir), sessionID+".jsonl")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("claudecode transcript: %s: %w", path, err)
	}
	return path, nil
}

// Internal JSONL line shape. message.content is polymorphic (string or
// array of blocks); we capture it as json.RawMessage and decode in two
// passes.
type sessionLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp,omitempty"`
	Message   struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func parseJSONL(path string) ([]transcript.Turn, error) {
	f, err := os.Open(path) //nolint:gosec // G304: caller-supplied transcript path is the function's documented purpose.
	if err != nil {
		return nil, fmt.Errorf("claudecode transcript: open %s: %w", path, err)
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
		var ln sessionLine
		if err := json.Unmarshal(raw, &ln); err != nil {
			return nil, fmt.Errorf("claudecode transcript: parse line %d in %s: %w", lineNo, path, err)
		}
		if ln.Type != "user" && ln.Type != "assistant" {
			continue
		}
		role := ln.Message.Role
		if role == "" {
			role = ln.Type
		}
		text, err := decodeContent(ln.Message.Content)
		if err != nil {
			return nil, fmt.Errorf("claudecode transcript: decode content on line %d in %s: %w", lineNo, path, err)
		}
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
		return nil, fmt.Errorf("claudecode transcript: scan %s: %w", path, err)
	}
	return out, nil
}

func decodeContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	// User messages: content is a plain string.
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
		return s, nil
	}
	// Assistant messages: array of blocks.
	if raw[0] == '[' {
		var blocks []contentBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return "", err
		}
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n\n"), nil
	}
	return "", nil
}
