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
func (s *Session) SyncLatestCodexRollout(workDir string, since time.Time) (string, error) {
	if s == nil || s.store == nil {
		return "", nil
	}
	return s.store.SyncLatestCodexRollout(s.Meta.SessionID, workDir, since)
}
