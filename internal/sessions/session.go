package sessions

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
