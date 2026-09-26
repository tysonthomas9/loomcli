package sessions

import "time"

// Session is a handle to an in-progress session.
// Created by Store.CreateSession and used by the parent process
// to track and finalize a running agent session.
type Session struct {
	store *Store
	Meta  SessionMetadata
}

// TranscriptUsage recovers token usage from this session's on-disk native
// transcript. Zero when the session has no store or nothing is recoverable.
func (s *Session) TranscriptUsage() TokenUsage {
	if s == nil || s.store == nil {
		return TokenUsage{}
	}
	return s.store.TranscriptUsage(s.Meta.SessionID)
}

// SessionID returns the session's unique identifier.
func (s *Session) SessionID() string {
	return s.Meta.SessionID
}

// NativeTranscriptPath returns the on-disk path to this session's mirrored
// native transcript (agent_transcript.jsonl). Empty when the session has no
// backing store. Does not check existence.
func (s *Session) NativeTranscriptPath() string {
	if s == nil || s.store == nil {
		return ""
	}
	return s.store.NativeTranscriptPath(s.Meta.SessionID)
}

// HasPersistedTokenUsage reports whether the session's on-disk metadata
// already carries non-zero token usage (e.g. captured by the SessionEnd
// hook). Returns false on load errors.
func (s *Session) HasPersistedTokenUsage() bool {
	if s == nil || s.store == nil {
		return false
	}
	meta, err := s.store.LoadMetadata(s.Meta.SessionID)
	if err != nil {
		return false
	}
	return meta.InputTokens != 0 || meta.OutputTokens != 0 ||
		meta.CacheReadTokens != 0 || meta.CacheWriteTokens != 0
}

// SyncLatestCodexRollout mirrors the matching Codex rollout into this session.
// Discovery is scoped to this session's agent, so a daemon-side finalize reads
// the agent's own profile roots rather than the daemon's environment.
func (s *Session) SyncLatestCodexRollout(workDir string, since time.Time) (string, error) {
	if s == nil || s.store == nil {
		return "", nil
	}
	return s.store.syncLatestCodexRolloutFor(s.Meta.AgentName, s.Meta.SessionID, workDir, since)
}

// SyncLatestClaudeTranscript mirrors the matching Claude Code transcript into
// this session. claudeUUID is the session UUID captured from the agent's
// stream output (empty falls back to newest-by-mtime). Discovery is scoped to
// this session's agent, so a daemon-side finalize reads the agent's own
// profile roots rather than the daemon's environment.
func (s *Session) SyncLatestClaudeTranscript(workDir, claudeUUID string, since time.Time) (string, error) {
	if s == nil || s.store == nil {
		return "", nil
	}
	return s.store.syncLatestClaudeTranscriptFor(s.Meta.AgentName, s.Meta.SessionID, workDir, claudeUUID, since)
}
