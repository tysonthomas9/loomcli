package loomstore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func stateEvents(agentID string, kinds ...string) []Event {
	var out []Event
	for _, k := range kinds {
		out = append(out, Event{AgentID: agentID, Kind: k, Payload: json.RawMessage(`{}`)})
	}
	return out
}

func eventCount(t *testing.T, s *Store, agentID string) int {
	t.Helper()
	p, err := s.ListEvents(context.Background(), EventQuery{AgentID: agentID})
	if err != nil {
		t.Fatal(err)
	}
	return len(p.Events)
}

// TestAtomicStateCrashBeforeCommit: a crash after CommitState's writes but
// before its COMMIT leaves, after a restart, neither the row change, the
// revision bump nor any of its events.
func TestAtomicStateCrashBeforeCommit(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "loom.db")
	s := openAt(t, path)
	if err := s.InsertAgent(ctx, agent("a1", "interactive")); err != nil {
		t.Fatal(err)
	}
	a, _ := s.GetAgent(ctx, "a1")
	if a.Revision != 0 {
		t.Fatalf("new agent revision = %d; want 0", a.Revision)
	}
	to := a.StateOf()
	to.State = "active"
	commitStateCrash = func() { panic("crash") }
	t.Cleanup(func() { commitStateCrash = func() {} })
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("did not crash")
			}
		}()
		_, _ = s.CommitState(ctx, "a1", a.StateOf(), to, a.Revision, stateEvents("a1", "agent.state_changed"))
	}()
	commitStateCrash = func() {}
	s.Close()

	s = openAt(t, path)
	got, err := s.GetAgent(ctx, "a1")
	if err != nil || got.State != "idle" || got.Revision != 0 {
		t.Fatalf("after restart: state %q revision %d err %v; want idle, 0", got.State, got.Revision, err)
	}
	if n := eventCount(t, s, "a1"); n != 0 {
		t.Fatalf("after restart: %d events; want 0", n)
	}
}

// TestReplaySameRevisionNoop: CommitState bumps the revision by one and
// names each event <agent>:<revision>:<kind>; replaying the same write (the
// same expected revision) changes nothing and adds no event.
func TestReplaySameRevisionNoop(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, filepath.Join(t.TempDir(), "loom.db"))
	if err := s.InsertAgent(ctx, agent("a1", "interactive")); err != nil {
		t.Fatal(err)
	}
	a, _ := s.GetAgent(ctx, "a1")
	reason := "r"
	to := a.StateOf()
	to.AttentionReason = &reason // a change that keeps the state
	evs := stateEvents("a1", "attention.raised", "agent.settled")
	got, err := s.CommitState(ctx, "a1", a.StateOf(), to, a.Revision, evs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].EventID != "a1:1:attention.raised" || got[1].EventID != "a1:1:agent.settled" ||
		got[0].Seq != 1 || got[1].Seq != 2 {
		t.Fatalf("saved %+v; want a1:1:<kind> at seqs 1, 2", got)
	}
	if b, _ := s.GetAgent(ctx, "a1"); b.Revision != 1 || deref(b.AttentionReason) != "r" {
		t.Fatalf("after commit: revision %d attention %v; want 1, r", b.Revision, b.AttentionReason)
	}
	if _, err := s.CommitState(ctx, "a1", a.StateOf(), to, a.Revision, evs); !errors.Is(err, ErrStateChanged) {
		t.Fatalf("replay err = %v; want ErrStateChanged", err)
	}
	// The state columns alone matching is not enough: the revision must too.
	b, _ := s.GetAgent(ctx, "a1")
	if _, err := s.CommitState(ctx, "a1", b.StateOf(), b.StateOf(), a.Revision, evs); !errors.Is(err, ErrStateChanged) {
		t.Fatalf("stale revision err = %v; want ErrStateChanged", err)
	}
	if b, _ := s.GetAgent(ctx, "a1"); b.Revision != 1 || eventCount(t, s, "a1") != 2 {
		t.Fatalf("after replays: revision %d events %d; want 1, 2", b.Revision, eventCount(t, s, "a1"))
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
