// Package codex reads Codex CLI session transcripts.
//
// Codex writes one JSONL per session at:
//
//	~/.codex/sessions/<YYYY>/<MM>/<DD>/rollout-<timestamp>-<session-uuid>.jsonl
//
// Each line is one event. The lines this reader cares about have
// shape:
//
//	{"type":"response_item","payload":{"role":"assistant",
//	  "content":[{"type":"text","text":"..."}]}}
//
// Roles observed in practice: "user", "assistant", "system", "tool".
// Tool-call payloads are mapped to role "system" for now (the chat
// History view does not need the structural detail).
package codex

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

// Reader implements transcript.Reader for Codex CLI.
type Reader struct {
	// SessionsRoot overrides the default ~/.codex/sessions/ location.
	// Empty means use the default.
	SessionsRoot string
}

// New constructs a Codex transcript Reader.
func New() *Reader { return &Reader{} }

// Read returns the ordered list of turns for the given Codex session
// UUID. workingDir is ignored — Codex indexes sessions by date/UUID,
// not by working directory.
func (r *Reader) Read(harnessSessionID, _ string) ([]transcript.Turn, error) {
	if harnessSessionID == "" {
		return nil, fmt.Errorf("codex transcript: empty session id")
	}
	path, err := r.locate(harnessSessionID)
	if err != nil {
		return nil, err
	}
	return parseJSONL(path)
}

func (r *Reader) sessionsRoot() (string, error) {
	if r.SessionsRoot != "" {
		return r.SessionsRoot, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("codex transcript: resolve home: %w", err)
	}
	return filepath.Join(home, ".codex", "sessions"), nil
}

// locate scans the sessions root for a file whose name contains the
// given session UUID. Codex's rollout filenames are
// rollout-<timestamp>-<uuid>.jsonl; we match on the suffix so any
// timestamp prefix works.
func (r *Reader) locate(sessionID string) (string, error) {
	root, err := r.sessionsRoot()
	if err != nil {
		return "", err
	}
	suffix := "-" + sessionID + ".jsonl"
	var found string
	werr := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), suffix) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if werr != nil {
		return "", fmt.Errorf("codex transcript: walk %s: %w", root, werr)
	}
	if found == "" {
		return "", fmt.Errorf("codex transcript: no session file for %s under %s", sessionID, root)
	}
	return found, nil
}

// Internal JSONL line shape — only the fields we read.
type rolloutLine struct {
	Type    string `json:"type"`
	Payload struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"payload"`
	// Codex records a top-level "timestamp" on some line types; capture
	// when present (RFC3339).
	Timestamp string `json:"timestamp,omitempty"`
}

//nolint:funlen // Linear JSONL parser (open → scan → unmarshal each line → assemble turns). Mirrors upstream harness-wrapper.
func parseJSONL(path string) ([]transcript.Turn, error) {
	f, err := os.Open(path) //nolint:gosec // G304: caller-supplied transcript path is the function's documented purpose.
	if err != nil {
		return nil, fmt.Errorf("codex transcript: open %s: %w", path, err)
	}
	defer f.Close()

	out := make([]transcript.Turn, 0, 32)
	sc := bufio.NewScanner(f)
	// Some response_item lines can be large; bump buffer ceiling.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var ln rolloutLine
		if err := json.Unmarshal(raw, &ln); err != nil {
			return nil, fmt.Errorf("codex transcript: parse line %d in %s: %w", lineNo, path, err)
		}
		if ln.Type != "response_item" {
			continue
		}
		role := ln.Payload.Role
		switch role {
		case "user", "assistant", "system":
			// keep as-is
		case "":
			continue
		default:
			role = "system"
		}
		var parts []string
		for _, c := range ln.Payload.Content {
			if c.Text != "" {
				parts = append(parts, c.Text)
			}
		}
		if len(parts) == 0 {
			continue
		}
		var ts time.Time
		if ln.Timestamp != "" {
			ts, _ = time.Parse(time.RFC3339, ln.Timestamp)
		}
		out = append(out, transcript.Turn{
			Role:      role,
			Text:      strings.Join(parts, "\n\n"),
			Timestamp: ts,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("codex transcript: scan %s: %w", path, err)
	}
	return out, nil
}
