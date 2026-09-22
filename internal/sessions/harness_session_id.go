package sessions

import (
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backendnames"
)

// harnessSessionUUID matches the harness session UUID at the END of a native
// transcript's base name. Claude Code names its transcripts "<uuid>.jsonl", and
// Codex names its rollouts "rollout-<timestamp>-<uuid>.jsonl" — and that
// timestamp contains hyphens of its own, so anchoring on the UUID shape is the
// only split that works for both without knowing the timestamp format.
var harnessSessionUUID = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// LatestHarnessSessionID resolves the session UUID the BACKEND assigned to its
// own most recent transcript for workDir — the key every
// transcript.UsageReader is indexed by.
//
// It exists for readers that are handed a loom session but not a harness one.
// The daemon supervisor is the motivating caller: it finalizes after the worker
// has already been reaped, so the worker's in-process capture
// (backends.GetLastCapturedSessionID) is gone, and the lock file's carried
// claude_session_id is cleared on a successful run by clearDaemonResumeOnSuccess.
// Rather than invent a new plumbing channel, this reuses the same
// newest-transcript-since-start resolution SyncLatestClaudeTranscript and
// SyncLatestCodexRollout already rely on, and recovers the id from the filename.
//
// hintUUID, when known, is preferred over the mtime scan. `since` is the
// session start; both resolvers apply their own clock-skew margin to it.
// Returns "" for a backend with no readable transcript layout, or when nothing
// matched — callers treat that as "no usage available", never as an error.
func LatestHarnessSessionID(backend, workDir, hintUUID string, since time.Time) string {
	return LatestHarnessSessionIDFor("", "", backend, workDir, hintUUID, since)
}

// LatestHarnessSessionIDFor is LatestHarnessSessionID scoped to one agent's
// harness profile under projectDir. Empty projectDir/agent resolves from the
// process environment, which is what LatestHarnessSessionID passes — so every
// existing caller keeps its behavior exactly.
func LatestHarnessSessionIDFor(projectDir, agent, backend, workDir, hintUUID string, since time.Time) string {
	path := latestHarnessTranscriptPath(projectDir, agent, backend, workDir, strings.TrimSpace(hintUUID), since)
	if path == "" {
		return ""
	}
	return harnessSessionUUID.FindString(strings.TrimSuffix(filepath.Base(path), ".jsonl"))
}

// latestHarnessTranscriptPath dispatches to the per-backend resolver already
// used to mirror that backend's transcript onto a session.
func latestHarnessTranscriptPath(projectDir, agent, backend, workDir, hintUUID string, since time.Time) string {
	switch backend {
	case backendnames.Claude:
		claudeDir := claudeProjectDirFor(projectDir, agent, workDir)
		if claudeDir == "" {
			return ""
		}
		return resolveClaudeTranscript(claudeDir, hintUUID, since)
	case backendnames.Codex:
		root := CodexSessionsRootFor(projectDir, agent)
		if root == "" {
			return ""
		}
		// Codex indexes rollouts by session id, not by working directory, so the
		// resolver has to read each candidate's session_meta to match workDir.
		best, err := findLatestCodexRollout(root, workDir, since)
		if err != nil {
			return ""
		}
		return best
	default:
		return ""
	}
}
