package chat

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/harness/turns"
)

// fakeStore is a minimal in-memory chat.Store for tests in this
// package. We can't import memstore from chat tests because memstore
// depends on chat (import cycle).
type fakeStore struct {
	mu       sync.Mutex
	sessions map[string]Session
	turns    map[string][]Turn
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		sessions: make(map[string]Session),
		turns:    make(map[string][]Turn),
	}
}

func (f *fakeStore) CreateSession(_ context.Context, s *Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[s.ID] = *s
	return nil
}

func (f *fakeStore) GetSession(_ context.Context, id string) (*Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sessions[id]
	return &s, nil
}

func (f *fakeStore) UpdateSession(_ context.Context, s *Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[s.ID] = *s
	return nil
}

func (f *fakeStore) AppendTurn(_ context.Context, t *Turn) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.turns[t.SessionID] = append(f.turns[t.SessionID], *t)
	return nil
}

func (f *fakeStore) UpdateTurn(_ context.Context, t *Turn) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	list := f.turns[t.SessionID]
	for i := range list {
		if list[i].ID == t.ID {
			list[i] = *t
			return nil
		}
	}
	list = append(list, *t)
	f.turns[t.SessionID] = list
	return nil
}

func (f *fakeStore) ListTurns(_ context.Context, sessionID string) ([]Turn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Turn(nil), f.turns[sessionID]...), nil
}

// TestHandleTurnsEvent_APIErrorFieldsForwarded confirms that the chat
// layer carries the structured HTTPCode/RetryAfter fields from a
// turns.Blocked event onto the Turn it emits on Conversation.Events().
// Tests the W1 row of the plan's wire-layer test matrix.
func TestHandleTurnsEvent_APIErrorFieldsForwarded(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	sess := &Session{ID: "test-session"}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	turn := &Turn{
		ID:        "turn-1",
		SessionID: sess.ID,
		Role:      RoleAssistant,
		State:     TurnStateStreaming,
		StartedAt: time.Now(),
	}
	if err := store.AppendTurn(ctx, turn); err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}

	c := &Conversation{
		store:       store,
		session:     *sess,
		eventCh:     make(chan TurnEvent, 4),
		closed:      make(chan struct{}),
		currentTurn: turn,
	}

	c.handleTurnsEvent(turns.Event{
		Kind:       turns.Blocked,
		At:         time.Now(),
		Reason:     "api error 529: Overloaded.",
		HTTPCode:   529,
		RetryAfter: 30 * time.Second,
	})

	select {
	case ev := <-c.eventCh:
		if ev.Err != nil {
			t.Fatalf("unexpected Err: %v", ev.Err)
		}
		if ev.Turn.State != TurnStateErrored {
			t.Errorf("Turn.State = %q, want %q", ev.Turn.State, TurnStateErrored)
		}
		if ev.Turn.HTTPCode != 529 {
			t.Errorf("Turn.HTTPCode = %d, want 529", ev.Turn.HTTPCode)
		}
		if ev.Turn.RetryAfter != 30*time.Second {
			t.Errorf("Turn.RetryAfter = %v, want 30s", ev.Turn.RetryAfter)
		}
		if ev.Turn.Reason != "api error 529: Overloaded." {
			t.Errorf("Turn.Reason = %q, want propagated reason", ev.Turn.Reason)
		}
	case <-time.After(time.Second):
		t.Fatal("no TurnEvent emitted within 1s")
	}
}

// TestHandleTurnsEvent_APIErrorTransportNoCode confirms the transport-
// error variant (Code=0) is also forwarded — consumers must see Code=0
// rather than an absent field.
func TestHandleTurnsEvent_APIErrorTransportNoCode(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	sess := &Session{ID: "transport-session"}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	turn := &Turn{
		ID:        "turn-1",
		SessionID: sess.ID,
		Role:      RoleAssistant,
		State:     TurnStateStreaming,
		StartedAt: time.Now(),
	}
	if err := store.AppendTurn(ctx, turn); err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}
	c := &Conversation{
		store:       store,
		session:     *sess,
		eventCh:     make(chan TurnEvent, 4),
		closed:      make(chan struct{}),
		currentTurn: turn,
	}

	c.handleTurnsEvent(turns.Event{
		Kind:     turns.Blocked,
		At:       time.Now(),
		Reason:   "api error: The socket connection was closed unexpectedly.",
		HTTPCode: 0,
	})

	select {
	case ev := <-c.eventCh:
		if ev.Turn.HTTPCode != 0 {
			t.Errorf("Turn.HTTPCode = %d, want 0 for transport error", ev.Turn.HTTPCode)
		}
		if ev.Turn.RetryAfter != 0 {
			t.Errorf("Turn.RetryAfter = %v, want 0", ev.Turn.RetryAfter)
		}
		if ev.Turn.State != TurnStateErrored {
			t.Errorf("Turn.State = %q, want %q", ev.Turn.State, TurnStateErrored)
		}
	case <-time.After(time.Second):
		t.Fatal("no TurnEvent emitted within 1s")
	}
}
