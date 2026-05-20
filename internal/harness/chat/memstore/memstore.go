// Package memstore is the in-memory chat.Store implementation shipped
// with v1. It holds sessions and turns in process memory; everything
// is lost on restart. Suitable for testing, single-process gateways,
// and prototype use.
//
// Production deployments that need durability should plug in an
// alternate Store (e.g. SQLite or Postgres-backed) implementing the
// same chat.Store interface.
package memstore

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/harness/chat"
)

// Store is the in-memory implementation of chat.Store.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]chat.Session // keyed by Session.ID
	turns    map[string][]chat.Turn  // keyed by Session.ID, preserves insertion order
}

// New constructs an empty Store.
func New() *Store {
	return &Store{
		sessions: make(map[string]chat.Session),
		turns:    make(map[string][]chat.Turn),
	}
}

// CreateSession inserts a new session. Returns an error if a session
// with the same ID already exists.
func (s *Store) CreateSession(_ context.Context, sess *chat.Session) error {
	if sess == nil {
		return errors.New("memstore: nil session")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[sess.ID]; exists {
		return fmt.Errorf("memstore: session %s already exists", sess.ID)
	}
	s.sessions[sess.ID] = *sess
	return nil
}

// GetSession returns a copy of the stored session. Mutations to the
// returned pointer do not affect the store.
func (s *Store) GetSession(_ context.Context, id string) (*chat.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[id]
	if !ok {
		return nil, fmt.Errorf("memstore: session %s not found", id)
	}
	return &sess, nil
}

// UpdateSession replaces an existing session record.
func (s *Store) UpdateSession(_ context.Context, sess *chat.Session) error {
	if sess == nil {
		return errors.New("memstore: nil session")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[sess.ID]; !exists {
		return fmt.Errorf("memstore: session %s not found", sess.ID)
	}
	s.sessions[sess.ID] = *sess
	return nil
}

// AppendTurn records a new turn at the end of the session's list.
func (s *Store) AppendTurn(_ context.Context, t *chat.Turn) error {
	if t == nil {
		return errors.New("memstore: nil turn")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[t.SessionID]; !exists {
		return fmt.Errorf("memstore: session %s not found for turn %s", t.SessionID, t.ID)
	}
	s.turns[t.SessionID] = append(s.turns[t.SessionID], *t)
	return nil
}

// UpdateTurn replaces an existing turn (matched by ID) within its
// session. Insertion order is preserved.
func (s *Store) UpdateTurn(_ context.Context, t *chat.Turn) error {
	if t == nil {
		return errors.New("memstore: nil turn")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.turns[t.SessionID]
	for i, existing := range list {
		if existing.ID == t.ID {
			list[i] = *t
			return nil
		}
	}
	return fmt.Errorf("memstore: turn %s not found in session %s", t.ID, t.SessionID)
}

// ListTurns returns a copy of the turn list for the given session.
func (s *Store) ListTurns(_ context.Context, sessionID string) ([]chat.Turn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list, ok := s.turns[sessionID]
	if !ok {
		if _, exists := s.sessions[sessionID]; !exists {
			return nil, fmt.Errorf("memstore: session %s not found", sessionID)
		}
		return nil, nil
	}
	out := make([]chat.Turn, len(list))
	copy(out, list)
	return out, nil
}
