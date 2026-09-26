package sessions

import (
	"os"
	"path/filepath"
	"regexp"
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
	return s.syncLatestClaudeTranscriptFor("", sessionID, workDir, claudeUUID, since)
}

// syncLatestClaudeTranscriptFor is SyncLatestClaudeTranscript scoped to an
// agent: when that agent has a harness profile directory, the transcript is
// read from there instead of from this process's own Claude config root. The
// daemon finalizes a run AFTER the agent is reaped, so its environment carries
// no CLAUDE_CONFIG_DIR — without the agent scope it would look under the
// operator's ~/.claude and mirror nothing. An empty agent resolves exactly as
// before.
func (s *Store) syncLatestClaudeTranscriptFor(agent, sessionID, workDir, claudeUUID string, since time.Time) (string, error) {
	_, span := startSpan(runtimectx.RootContext(), "service.Sessions.SyncLatestClaudeTranscript",
		attrLoomSessionID(sessionID),
		attrLoomBackend("claude"),
	)
	defer span.End()

	projectDir := claudeProjectDirFor(s.RootDir(), agent, workDir)
	if projectDir == "" {
		return "", nil
	}
	srcPath := resolveClaudeTranscript(projectDir, claudeUUID, since)
	if srcPath == "" {
		return "", nil
	}
	if err := s.SyncNativeTranscript(sessionID, srcPath, TranscriptFormatRaw); err != nil {
		recordErr(span, err)
		return "", err
	}
	return srcPath, nil
}

// claudeProjectDirFor returns <claude-config-dir>/projects/<encoded-cwd> for
// workDir, or "" if the config dir cannot be resolved or workDir is empty. The
// config dir is resolved for the named agent (profile dir → CLAUDE_CONFIG_DIR
// → ~/.claude); an empty projectDir or agent yields the process-scoped root.
func claudeProjectDirFor(projectDir, agent, workDir string) string {
	if workDir == "" {
		return ""
	}
	root := ClaudeConfigDirFor(projectDir, agent)
	if root == "" {
		return ""
	}
	return filepath.Join(root, "projects", encodeClaudeCWD(workDir))
}

// ClaudeConfigDir resolves Claude Code's config directory, honoring the
// CLAUDE_CONFIG_DIR override before falling back to ~/.claude.
//
// The override is load-bearing, not cosmetic: Claude Code writes its session
// transcripts under <config dir>/projects/, and the backend's own auth check
// (backends.claudeAuthFilePath) already honors it. Resolving only from $HOME
// here made the two disagree, so an operator who set the override — which the
// container and smoke-test paths do — got passing auth checks and a transcript
// lookup that never found anything. Downstream that is not a missing nicety:
// the comment completion hook needs the final assistant reply, so it fails,
// the run is demoted, and the owned task reopens on every attempt.
func ClaudeConfigDir() string {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// claudeCWDSanitize matches every character Claude Code rewrites when it names
// a project dir: anything that is not an ASCII letter or digit. Crucially this
// includes '.', so a path under ~/.loom encodes the dot too. Replacing only '/'
// produced "-Users-oleh-.loom-..." while Claude writes "-Users-oleh--loom-...",
// so the project dir was never found and transcripts came back empty for every
// fleet-mode run (all run under ~/.loom). Hyphens map to themselves, matching
// Claude's behavior of leaving existing '-' in place (no collapsing).
var claudeCWDSanitize = regexp.MustCompile(`[^A-Za-z0-9]`)

// encodeClaudeCWD encodes an absolute working directory the way Claude Code
// names its project dirs: every non-alphanumeric character becomes '-' (a
// leading slash yields a leading hyphen; a '.' becomes '-').
func encodeClaudeCWD(workDir string) string {
	return claudeCWDSanitize.ReplaceAllString(workDir, "-")
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
