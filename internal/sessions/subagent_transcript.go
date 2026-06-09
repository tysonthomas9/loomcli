package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/atomicfile"
	"github.com/tysonthomas9/loomcli/internal/runtimectx"
	"github.com/tysonthomas9/loomcli/internal/sessions/redact"
)

// subagentsSubdir is the subdirectory inside a session dir that holds
// copies of Claude Code subagent transcripts (one per spawned Task).
const subagentsSubdir = "subagents"

// SubagentIDPattern matches Claude Code's subagent ID format (alphanumeric
// hex-ish). Restricts to a safe character set before using in a filename.
// Exported so upstream callers (e.g., the web service read path) can apply
// the same validation the write path uses.
var SubagentIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// SyncSubagentTranscript copies a subagent's JSONL transcript into the
// session directory at sessions/<sid>/subagents/agent-<subagentID>.jsonl.
// Called from the PostToolUse/Task hook once per completed subagent.
//
// No-op (nil error) when srcPath is empty or does not exist — this is the
// common case when a subagent was spawned but wrote no transcript before
// completion. Rejects invalid session or subagent IDs.
func (s *Store) SyncSubagentTranscript(sessionID, subagentID, srcPath string) error {
	if srcPath == "" || subagentID == "" {
		return nil
	}
	_, span := startSpan(runtimectx.RootContext(), "service.Sessions.SyncSubagentTranscript",
		attrLoomSessionID(sessionID),
	)
	defer span.End()

	if strings.ContainsAny(sessionID, "/\\") {
		err := fmt.Errorf("invalid session ID %q: contains path separator", sessionID)
		recordErr(span, err)
		return err
	}
	if !SubagentIDPattern.MatchString(subagentID) {
		err := fmt.Errorf("invalid subagent ID %q", subagentID)
		recordErr(span, err)
		return err
	}

	sessDir := filepath.Join(s.dir, sessionID)
	cleanDir := filepath.Clean(sessDir)
	if !strings.HasPrefix(cleanDir+string(os.PathSeparator), filepath.Clean(s.dir)+string(os.PathSeparator)) {
		err := fmt.Errorf("invalid session ID %q", sessionID)
		recordErr(span, err)
		return err
	}
	if _, err := os.Stat(sessDir); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("session %q does not exist", sessionID)
		}
		recordErr(span, err)
		return fmt.Errorf("stat session dir: %w", err)
	}

	if _, err := os.Stat(srcPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		recordErr(span, err)
		return fmt.Errorf("stat source transcript: %w", err)
	}

	dstDir := filepath.Join(sessDir, subagentsSubdir)
	if err := os.MkdirAll(dstDir, sessDirPerm); err != nil {
		recordErr(span, err)
		return fmt.Errorf("create subagents dir: %w", err)
	}

	dstPath := filepath.Join(dstDir, "agent-"+subagentID+".jsonl")

	// #nosec G304 — srcPath comes from the agent hook payload (trusted)
	data, err := os.ReadFile(srcPath)
	if err != nil {
		recordErr(span, err)
		return fmt.Errorf("read source subagent transcript: %w", err)
	}

	if redactionEnabled() {
		redacted, rerr := redact.JSONLBytes(data)
		if rerr != nil {
			recordErr(span, rerr)
			return fmt.Errorf("redact subagent transcript: %w", rerr)
		}
		data = redacted
	}

	if err := atomicfile.WriteFile(dstPath, data, sessFilePerm); err != nil {
		recordErr(span, err)
		return fmt.Errorf("write subagent transcript: %w", err)
	}
	return nil
}

// ListSubagentTranscripts returns the filenames (not full paths) of all
// subagent transcripts captured for a session. Returns an empty slice if the
// subagents directory does not exist.
func (s *Store) ListSubagentTranscripts(sessionID string) ([]string, error) {
	_, span := startSpan(runtimectx.RootContext(), "service.Sessions.ListSubagentTranscripts",
		attrLoomSessionID(sessionID),
	)
	defer span.End()

	if strings.ContainsAny(sessionID, "/\\") {
		err := fmt.Errorf("invalid session ID %q", sessionID)
		recordErr(span, err)
		return nil, err
	}
	dir := filepath.Join(s.dir, sessionID, subagentsSubdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		recordErr(span, err)
		return nil, fmt.Errorf("read subagents dir: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "agent-") && strings.HasSuffix(name, ".jsonl") {
			out = append(out, name)
		}
	}
	span.SetAttributes(attrResultCount(len(out)))
	return out, nil
}

// SubagentTranscriptPath returns the on-disk path to one captured subagent
// transcript. Does not check existence.
func (s *Store) SubagentTranscriptPath(sessionID, subagentID string) string {
	return filepath.Join(s.dir, sessionID, subagentsSubdir, "agent-"+subagentID+".jsonl")
}
