package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/runtimectx"
)

// SyncLatestClaudeTranscript mirrors Claude Code's native JSONL transcript for
// workDir into the Loom session as agent_transcript.jsonl.
//
// Claude Code writes one transcript per session at
// ~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl, where <encoded-cwd>
// is the absolute working directory with every '/' replaced by '-'. When
// claudeUUID is non-empty the exact file is resolved deterministically;
// otherwise (e.g. runs that never surfaced a session id) it falls back to the
// newest *.jsonl in the project dir modified at/after `since`.
//
// Best-effort and idempotent (delegates to SyncNativeTranscript): returns an
// empty path when no matching transcript is available. This is the non-hook
// counterpart of the hook-driven capture used outside fleet mode — agents run
// by `loom plan|task` in fleet mode have no Claude Code hooks installed.
func (s *Store) SyncLatestClaudeTranscript(sessionID, workDir, claudeUUID string, since time.Time) (string, error) {
	_, span := startSpan(runtimectx.RootContext(), "service.Sessions.SyncLatestClaudeTranscript",
		attrLoomSessionID(sessionID),
		attrLoomBackend("claude"),
	)
	defer span.End()

	projectDir := claudeProjectDir(workDir)
	if projectDir == "" {
		return "", nil
	}
	srcPath := resolveClaudeTranscript(projectDir, claudeUUID, since)
	if srcPath == "" {
		return "", nil
	}
	if err := s.SyncNativeTranscript(sessionID, srcPath); err != nil {
		recordErr(span, err)
		return "", err
	}
	return srcPath, nil
}

// claudeProjectDir returns ~/.claude/projects/<encoded-cwd> for workDir, or ""
// if the home dir cannot be resolved or workDir is empty.
func claudeProjectDir(workDir string) string {
	if workDir == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "projects", encodeClaudeCWD(workDir))
}

// encodeClaudeCWD encodes an absolute working directory the way Claude Code
// names its project dirs: every '/' becomes '-' (a leading slash yields a
// leading hyphen).
func encodeClaudeCWD(workDir string) string {
	return strings.ReplaceAll(workDir, "/", "-")
}

// resolveClaudeTranscript prefers the exact <uuid>.jsonl and otherwise returns
// the newest *.jsonl modified at/after `since`.
func resolveClaudeTranscript(projectDir, claudeUUID string, since time.Time) string {
	if claudeUUID != "" {
		candidate := filepath.Join(projectDir, claudeUUID+".jsonl")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return newestClaudeTranscript(projectDir, since)
}

// newestClaudeTranscript returns the most recently modified *.jsonl in
// projectDir whose mtime is at/after `since` (minus a small margin to tolerate
// clock skew between session start and the first transcript write).
func newestClaudeTranscript(projectDir string, since time.Time) string {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return ""
	}
	cutoff := since.Add(-1 * time.Minute)
	var bestPath string
	var bestMod time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, statErr := e.Info()
		if statErr != nil || info.ModTime().Before(cutoff) {
			continue
		}
		if bestPath == "" || info.ModTime().After(bestMod) {
			bestPath = filepath.Join(projectDir, e.Name())
			bestMod = info.ModTime()
		}
	}
	return bestPath
}
