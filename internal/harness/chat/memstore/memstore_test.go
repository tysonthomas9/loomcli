package memstore

import (
	"context"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/harness/chat"
)

func TestSessionLifecycle(t *testing.T) {
	s := New()
	ctx := context.Background()

	sess := &chat.Session{ID: "sess-1", Harness: "codex", CreatedAt: time.Now()}
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Duplicate insert rejected.
	if err := s.CreateSession(ctx, sess); err == nil {
		t.Fatal("expected duplicate CreateSession to fail")
	}

	got, err := s.GetSession(ctx, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Harness != "codex" {
		t.Errorf("Harness mismatch: %q", got.Harness)
	}

	sess.HarnessSessionID = "harness-abc"
	if err := s.UpdateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetSession(ctx, "sess-1")
	if got.HarnessSessionID != "harness-abc" {
		t.Errorf("UpdateSession did not persist HarnessSessionID")
	}
}

func TestTurnAppendListUpdate(t *testing.T) {
	s := New()
	ctx := context.Background()
	_ = s.CreateSession(ctx, &chat.Session{ID: "sess-1"})

	for i, role := range []chat.Role{chat.RoleUser, chat.RoleAssistant, chat.RoleUser} {
		t1 := &chat.Turn{ID: "t" + string(rune('0'+i)), SessionID: "sess-1", Role: role, State: chat.TurnStateComplete}
		if err := s.AppendTurn(ctx, t1); err != nil {
			t.Fatal(err)
		}
	}

	list, err := s.ListTurns(ctx, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 turns, got %d", len(list))
	}
	if list[0].Role != chat.RoleUser || list[1].Role != chat.RoleAssistant {
		t.Errorf("insertion order not preserved: %+v", list)
	}

	// Update existing.
	list[1].State = chat.TurnStateErrored
	list[1].Reason = "test"
	if err := s.UpdateTurn(ctx, &list[1]); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ListTurns(ctx, "sess-1")
	if got[1].State != chat.TurnStateErrored || got[1].Reason != "test" {
		t.Errorf("UpdateTurn did not persist: %+v", got[1])
	}

	// Unknown turn ID errors.
	if err := s.UpdateTurn(ctx, &chat.Turn{ID: "nope", SessionID: "sess-1"}); err == nil {
		t.Error("expected error for unknown turn ID")
	}
}

func TestTurnAppendUnknownSession(t *testing.T) {
	s := New()
	err := s.AppendTurn(context.Background(), &chat.Turn{ID: "t1", SessionID: "missing"})
	if err == nil {
		t.Fatal("expected error for unknown session ID")
	}
}
