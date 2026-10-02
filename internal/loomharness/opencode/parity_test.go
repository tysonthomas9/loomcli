package opencode

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// eventKey is loomagent's EventID rule (nativeRow, internal/loomagent/
// events.go) for the events Loom saves: kind:Root:NativeID:key, where key is
// the InputKey of a delivery, the AskID of an ask, the TurnID of a turn
// boundary, else the ItemID, else the native Seq. "" means Loom saves no row.
func eventKey(e loomharness.Event) string {
	key := e.ItemID
	switch e.Type {
	case loomharness.EventMessageDelivered:
		key = e.InputKey
	case loomharness.EventAskOpened, loomharness.EventAskResolved, loomharness.EventAskLost:
		key = e.AskID
	case loomharness.EventTurnStarted, loomharness.EventTurnCompleted, loomharness.EventTurnResumed:
		key = e.TurnID
	case loomharness.EventItemCompleted, loomharness.EventUsage, loomharness.EventSubagentStarted:
	default:
		return "" // deltas, item starts and feed gaps are live only
	}
	if key == "" {
		key = "seq:" + strconv.FormatInt(e.Seq, 10)
	}
	return strings.Join([]string{string(e.Type), e.Session.Root, e.Session.NativeID, key}, ":")
}

// ids is what the port contract requires to match between the two copies of
// an event (Text, Time and Seq may differ).
func ids(e loomharness.Event) string {
	return fmt.Sprintf("type=%s root=%s native=%s turn=%s input=%s ask=%s item=%s kind=%s stop=%s",
		e.Type, e.Session.Root, e.Session.NativeID, e.TurnID, e.InputKey, e.AskID, e.ItemID, e.ItemKind, e.StopReason)
}

// keyed indexes the saved events by EventID, failing on a collision (two
// different events Loom would save under one id).
func keyed(t *testing.T, name string, events []loomharness.Event) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, e := range events {
		k := eventKey(e)
		if k == "" {
			continue
		}
		if prev, ok := out[k]; ok && prev != ids(e) {
			t.Errorf("%s: EventID %s collides:\n  %s\n  %s", name, k, prev, ids(e))
		}
		out[k] = ids(e)
	}
	return out
}

// capture reads a feed until it has seen gaps feed.gap events.
func capture(t *testing.T, c *Client, gaps int) []loomharness.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f, err := c.Feed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []loomharness.Event
	for gaps > 0 {
		select {
		case e := <-f.Events():
			if e.Type == loomharness.EventFeedGap {
				gaps--
			}
			out = append(out, e)
		case <-ctx.Done():
			t.Fatalf("timed out after %d events", len(out))
		}
	}
	return out
}

func history(t *testing.T, s *Session, limit int) []loomharness.Event {
	t.Helper()
	var out []loomharness.Event
	for after := ""; ; {
		p, err := s.Messages(context.Background(), after, limit)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p.Events...)
		if after = p.Next; after == "" {
			return out
		}
	}
}

// TestEventsLiveMatchesMessages: a live capture and a Messages read of the
// same two turns give each event Loom saves the same EventID and the same
// ids (port contract, 1.6d). Turn 1 has two steps (two usage events), an
// ask opened and resolved, completed items, and an OpenCode restart in the
// middle (a shutdown interrupt, then the restart notice); turn 2 is still
// waiting on an ask. The history below is what OpenCode's message updater
// projects from those events (core/src/session/message-updater.ts).
//
// History cannot hold a resolved ask (OpenCode lists pending asks only), so
// per_1's ask.opened and ask.resolved are live only; everything else
// matches. A second, fresh feed that starts after the restart and misses
// the turn's end still gives the turn's start and resume the same ids, and
// the history read supplies the turn.completed it missed, under the id the
// first feed saw.
func TestEventsLiveMatchesMessages(t *testing.T) {
	ctx := context.Background()
	seed := func() (*store, *Client, loomharness.NativeRef) {
		st := newStore()
		c := fakeServer(t, st)
		ref, err := c.Open(ctx, loomharness.OpenSpec{Key: "agent-1", Launch: loomharness.Launch{Root: "/root-a"}, Dir: "/repo"})
		if err != nil {
			t.Fatal(err)
		}
		st.messages[ref.NativeID] = []map[string]any{
			{"id": "msg_u1", "type": "user", "text": "go"},
			{"id": "msg_a1", "type": "assistant", "finish": "tool-calls", "content": []map[string]any{
				{"type": "reasoning", "text": "think"},
				{"type": "tool", "id": "call_1", "state": map[string]string{"status": "completed"}},
			}},
			{"id": "msg_11", "type": "synthetic", "text": "The server restarted", "metadata": map[string]string{"notice": "restart"}},
			{"id": "msg_a2", "type": "assistant", "finish": "stop", "content": []map[string]any{{"type": "text", "text": "done"}}},
			{"id": "msg_15", "type": "idle", "outcome": "succeeded"},
			{"id": "msg_u2", "type": "user", "text": "again"},
			{"id": "msg_a3", "type": "assistant", "content": []map[string]any{
				{"type": "tool", "id": "call_2", "state": map[string]string{"status": "running"}},
			}},
		}
		st.asks = map[string][]string{ref.NativeID: {"per_2"}}
		return st, c, ref
	}
	st, c, ref := seed()
	sid := ref.NativeID
	e := func(seq int, typ, data string) string {
		return fmt.Sprintf(`{"id":"evt_%d","type":%q,"created":%d,"data":%s,"durable":{"aggregateID":%q,"seq":%d}}`,
			seq, typ, 1000+seq, strings.ReplaceAll(data, "SID", sid), sid, seq)
	}
	beforeRestart := []string{
		e(1, "session.execution.started", `{"sessionID":"SID"}`),
		e(2, "session.inbox.delivered", `{"sessionID":"SID","inboxID":"msg_u1"}`),
		e(3, "session.reasoning.started", `{"sessionID":"SID","assistantMessageID":"msg_a1","ordinal":0}`),
		e(4, "session.reasoning.ended", `{"sessionID":"SID","assistantMessageID":"msg_a1","ordinal":0,"text":"think"}`),
		e(5, "session.tool.called", `{"sessionID":"SID","assistantMessageID":"msg_a1","id":"call_1"}`),
		e(6, "permission.asked", `{"sessionID":"SID","id":"per_1"}`),
		e(7, "permission.replied", `{"sessionID":"SID","requestID":"per_1","reply":"once"}`),
		e(8, "session.tool.success", `{"sessionID":"SID","assistantMessageID":"msg_a1","id":"call_1"}`),
		e(9, "session.step.ended", `{"sessionID":"SID","assistantMessageID":"msg_a1"}`),
		e(10, "session.execution.interrupted", `{"sessionID":"SID","reason":"shutdown"}`),
	}
	afterRestart := []string{
		e(11, "session.synthetic", `{"sessionID":"SID","text":"The server restarted","metadata":{"notice":"restart"}}`),
		e(12, "session.text.started", `{"sessionID":"SID","assistantMessageID":"msg_a2","ordinal":0}`),
		e(13, "session.text.ended", `{"sessionID":"SID","assistantMessageID":"msg_a2","ordinal":0,"text":"done"}`),
		e(14, "session.step.ended", `{"sessionID":"SID","assistantMessageID":"msg_a2"}`),
	}
	turnEnd := []string{
		e(15, "session.execution.succeeded", `{"sessionID":"SID"}`),
		e(16, "session.inbox.delivered", `{"sessionID":"SID","inboxID":"msg_u2"}`),
		e(17, "session.tool.called", `{"sessionID":"SID","assistantMessageID":"msg_a3","id":"call_2"}`),
		e(18, "permission.asked", `{"sessionID":"SID","id":"per_2"}`),
	}
	st.streams = [][]string{beforeRestart, append(append([]string{}, afterRestart...), turnEnd...)}
	full := capture(t, c, 2)

	for _, limit := range []int{0, 1, 2, 3} {
		hist := history(t, c.Session(ref), limit)
		lk, hk := keyed(t, "live", full), keyed(t, "history", hist)
		var liveOnly, histOnly []string
		for k, v := range lk {
			hv, ok := hk[k]
			switch {
			case !ok:
				liveOnly = append(liveOnly, k)
			case hv != v:
				t.Errorf("limit %d: %s\n  live:    %s\n  history: %s", limit, k, v, hv)
			}
		}
		for k := range hk {
			if _, ok := lk[k]; !ok {
				histOnly = append(histOnly, k)
			}
		}
		sort.Strings(liveOnly)
		want := []string{"ask.opened:/root-a:" + sid + ":per_1", "ask.resolved:/root-a:" + sid + ":per_1"}
		if strings.Join(liveOnly, " ") != strings.Join(want, " ") || len(histOnly) != 0 {
			t.Errorf("limit %d: live only %v (want %v), history only %v", limit, liveOnly, want, histOnly)
		}
		usage := 0
		for k := range hk {
			if strings.HasPrefix(k, "usage:") {
				usage++
			}
		}
		if usage != 2 {
			t.Errorf("limit %d: %d usage rows in history, want 2", limit, usage)
		}
	}
	for _, want := range []string{"turn.started", "turn.resumed", "turn.completed", "usage", "item.completed", "ask.opened", "message.delivered"} {
		found := false
		for _, ev := range full {
			found = found || string(ev.Type) == want
		}
		if !found {
			t.Errorf("live capture has no %s", want)
		}
	}

	// A fresh feed (a new adapter after the restart) that misses the end of
	// turn 1: it reads turn 1's start back from history.
	st2, c2, _ := seed()
	st2.streams = [][]string{afterRestart}
	partial := capture(t, c2, 1)
	pk, hk := keyed(t, "partial", partial), keyed(t, "history", history(t, c2.Session(ref), 2))
	fk := keyed(t, "live", full)
	for k, v := range pk {
		if hk[k] != v || fk[k] != v {
			t.Errorf("partial %s\n  partial: %s\n  history: %s\n  live:    %s", k, v, hk[k], fk[k])
		}
	}
	start := "turn.started:/root-a:" + sid + ":msg_u1"
	end := "turn.completed:/root-a:" + sid + ":msg_u1"
	if _, ok := pk[start]; !ok {
		t.Errorf("partial feed has no %s: %v", start, pk)
	}
	if _, ok := pk[end]; ok {
		t.Errorf("partial feed saw the turn end")
	}
	if hk[end] == "" || hk[end] != fk[end] {
		t.Errorf("history %s = %q, live %q", end, hk[end], fk[end])
	}
}
