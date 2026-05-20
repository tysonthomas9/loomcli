package chat

import "context"

// Store persists chat-level session metadata and turn records.
//
// Store does NOT store transcript bodies. Harnesses persist their own
// conversation logs (~/.codex/sessions/, ~/.claude/projects/); a future
// pkg/transcript layer reads them for History reconstruction. Store
// only holds the indexable metadata: who owns what, which chat session
// maps to which harness session, what turn IDs have been issued, when
// each turn started and finished.
//
// Implementations must be safe for concurrent use.
type Store interface {
	// CreateSession inserts a new session record. ID, Harness, and
	// CreatedAt are set by the caller. Implementations should return
	// an error if a session with the same ID already exists.
	CreateSession(ctx context.Context, s *Session) error

	// GetSession returns the session with the given ID, or a non-nil
	// error if not found.
	GetSession(ctx context.Context, id string) (*Session, error)

	// UpdateSession overwrites the stored record. Used to backfill
	// HarnessSessionID once the adapter extracts it.
	UpdateSession(ctx context.Context, s *Session) error

	// AppendTurn records a new turn. The caller is responsible for
	// allocating Turn.ID. Implementations must preserve insertion
	// order on ListTurns.
	AppendTurn(ctx context.Context, t *Turn) error

	// UpdateTurn replaces an existing turn record. The turn must
	// already exist (i.e. been previously AppendTurn'd).
	UpdateTurn(ctx context.Context, t *Turn) error

	// ListTurns returns every turn for the session in insertion order.
	ListTurns(ctx context.Context, sessionID string) ([]Turn, error)
}
