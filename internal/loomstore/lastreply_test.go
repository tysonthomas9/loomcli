package loomstore

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

// TestLastReply: the final reply is every message item of the last
// message's turn in the current attempt, never an earlier turn's or
// attempt's text, on each harness's way of saving turns.
func TestLastReply(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, filepath.Join(t.TempDir(), "loom.db"))
	n := 0
	add := func(id, kind, turn, payload string) {
		t.Helper()
		n++
		if _, err := s.AppendEvent(ctx, Event{AgentID: id, EventID: "e" + strconv.Itoa(n), Kind: kind, TurnID: turn,
			Payload: []byte(payload)}); err != nil {
			t.Fatal(err)
		}
	}
	msg := func(id, turn, text string) {
		add(id, "item.completed", turn, `{"itemKind":"message","text":`+strconv.Quote(text)+`}`)
	}
	reopen := func(id string) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, `UPDATE agents SET attempt_after_seq =
			(SELECT COALESCE(MAX(seq), 0) FROM agent_events WHERE agent_id = ?) WHERE agent_id = ?`, id, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"codex", "parts", "untagged", "nomarker", "oldmarker", "reopened", "silent"} {
		if err := s.InsertAgent(ctx, agent(id, "interactive")); err != nil {
			t.Fatal(err)
		}
	}
	// A codex history replay: items tagged with their turn, no turn.started.
	msg("codex", "older-turn", "Earlier private reply")
	msg("codex", "final-turn", "Final reply")
	// One turn's reply in several parts around a tool call (OpenCode).
	add("parts", "turn.started", "t1", `{}`)
	msg("parts", "t0", "earlier turn")
	msg("parts", "t1", "Findings: a, b.")
	add("parts", "item.completed", "t1", `{"itemKind":"tool"}`)
	msg("parts", "t1", "Review completed")
	// Untagged rows fall back to the last turn.started before the message.
	msg("untagged", "", "before")
	add("untagged", "turn.started", "", `{}`)
	msg("untagged", "", "one")
	msg("untagged", "", "two")
	// Untagged with no turn.started: only the last message is provably the reply.
	msg("nomarker", "", "Earlier reply")
	msg("nomarker", "", "Final reply")
	// A turn.started from an earlier attempt doesn't count.
	add("oldmarker", "turn.started", "", `{}`)
	msg("oldmarker", "", "attempt one")
	reopen("oldmarker")
	msg("oldmarker", "", "Earlier reply")
	msg("oldmarker", "", "Final reply")
	// A reopened attempt never quotes the earlier attempt, even in the same turn id.
	msg("reopened", "t1", "attempt one")
	reopen("reopened")
	msg("reopened", "t1", "attempt two")
	// A new attempt with no message has no reply.
	msg("silent", "t1", "attempt one")
	reopen("silent")
	add("silent", "turn.started", "t2", `{}`)

	for id, want := range map[string][]string{"codex": {"Final reply"}, "parts": {"Findings: a, b.", "Review completed"},
		"untagged": {"one", "two"}, "nomarker": {"Final reply"}, "oldmarker": {"Final reply"}, "reopened": {"attempt two"}, "silent": nil} {
		if got, err := s.LastReply(ctx, id); err != nil || !slices.Equal(got, want) {
			t.Errorf("%s: LastReply = %q, %v; want %q", id, got, err, want)
		}
	}
}
