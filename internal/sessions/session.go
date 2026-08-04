package sessions

import "time"

// Session is a handle to an in-progress session.
// Created by Store.CreateSession and used by the parent process
// to track and finalize a running agent session.
type Session struct {
	store *Store
	Meta  SessionMetadata
}

// SessionID returns the session's unique identifier.
func (s *Session) SessionID() string {
	return s.Meta.SessionID
}

// SyncLatestCodexRollout mirrors the matching Codex rollout into this session.
func (s *Session) SyncLatestCodexRollout(workDir string, since time.Time) (string, error) {
	if s == nil || s.store == nil {
		return "", nil
	}
	return s.store.SyncLatestCodexRollout(s.Meta.SessionID, workDir, since)
}

// SyncLatestClaudeTranscript mirrors the matching Claude Code transcript into
// this session. claudeUUID is the session UUID captured from the agent's
// stream output (empty falls back to newest-by-mtime).
func (s *Session) SyncLatestClaudeTranscript(workDir, claudeUUID string, since time.Time) (string, error) {
	if s == nil || s.store == nil {
		return "", nil
	}
	return s.store.SyncLatestClaudeTranscript(s.Meta.SessionID, workDir, claudeUUID, since)
}
