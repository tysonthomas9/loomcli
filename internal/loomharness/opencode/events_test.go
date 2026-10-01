package opencode

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

func ev(seq int, typ, data string) string {
	return fmt.Sprintf(`{"id":"evt_%d","type":%q,"created":%d,"data":%s,"durable":{"aggregateID":"ses_a","seq":%d}}`, seq, typ, 1000+seq, data, seq)
}

func live(typ, data string) string {
	return fmt.Sprintf(`{"id":"evt_x","type":%q,"created":1,"data":%s}`, typ, data)
}

// The first stream ends mid-turn (an OpenCode restart, whose shutdown
// interrupt ends no turn); the second replays already-seen durable events,
// then continues the same turn.
func TestEventsFeedReconnectGapAndNoDuplicateItems(t *testing.T) {
	st := newStore()
	st.streams = [][]string{
		{
			ev(1, "session.inbox.delivered", `{"sessionID":"ses_a","inboxID":"msg_in"}`),
			ev(2, "session.execution.started", `{"sessionID":"ses_a"}`),
			ev(3, "session.text.started", `{"sessionID":"ses_a","assistantMessageID":"msg_a1","ordinal":0}`),
			live("session.text.delta", `{"sessionID":"ses_a","assistantMessageID":"msg_a1","ordinal":0,"delta":"PA"}`),
			ev(4, "session.text.ended", `{"sessionID":"ses_a","assistantMessageID":"msg_a1","ordinal":0,"text":"PAST"}`),
			ev(5, "session.tool.called", `{"sessionID":"ses_a","assistantMessageID":"msg_a1","id":"call_1"}`),
			ev(6, "permission.asked", `{"sessionID":"ses_a","id":"per_1"}`),
			ev(7, "session.execution.interrupted", `{"sessionID":"ses_a","reason":"shutdown"}`),
		},
		{
			ev(4, "session.text.ended", `{"sessionID":"ses_a","assistantMessageID":"msg_a1","ordinal":0,"text":"PAST"}`),
			ev(8, "session.synthetic", `{"sessionID":"ses_a","text":"The server restarted","metadata":{"notice":"restart"}}`),
			ev(9, "session.tool.failed", `{"sessionID":"ses_a","assistantMessageID":"msg_a1","id":"call_1"}`),
			ev(10, "permission.replied", `{"sessionID":"ses_a","requestID":"per_1","reply":"once"}`),
			ev(11, "session.created", `{"sessionID":"ses_child","parentID":"ses_a"}`),
			ev(12, "session.step.ended", `{"sessionID":"ses_a","assistantMessageID":"msg_a1"}`),
			ev(13, "session.execution.interrupted", `{"sessionID":"ses_a","reason":"user"}`),
		},
	}
	c := fakeServer(t, st)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f, err := c.Feed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	completed := map[string]int{}
	for len(got) < 15 {
		var e loomharness.Event
		select {
		case e = <-f.Events():
		case <-ctx.Done():
			t.Fatalf("timed out after %d events:\n%s", len(got), strings.Join(got, "\n"))
		}
		if e.Type == loomharness.EventItemCompleted {
			completed[e.ItemID]++
		}
		got = append(got, fmt.Sprintf("%s %s %s %s %s %s %s %s", e.Type, e.Session.NativeID, e.TurnID, e.ItemKind, e.ItemID, e.InputKey, e.AskID, e.StopReason))
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, open := <-f.Events(); open {
		t.Fatal("Events not closed after Close")
	}
	want := []string{
		"turn.started ses_a msg_in   msg_in  ",
		"message.delivered ses_a msg_in message msg_in msg_in  ",
		"item.started ses_a msg_in message msg_a1/text/0   ",
		"delta ses_a msg_in message msg_a1/text/0   ",
		"item.completed ses_a msg_in message msg_a1/text/0   ",
		"item.started ses_a msg_in tool msg_a1/tool/call_1   ",
		"ask.opened ses_a msg_in    per_1 ",
		"feed.gap       ",
		"turn.resumed ses_a msg_in  msg_8   ",
		"item.completed ses_a msg_in tool msg_a1/tool/call_1   ",
		"ask.resolved ses_a msg_in    per_1 ",
		"harness.subagent.started ses_a msg_in  ses_child   ",
		"usage ses_a msg_in  msg_a1   ",
		"turn.completed ses_a msg_in     cancelled",
		"feed.gap       ",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for id, n := range completed {
		if n != 1 {
			t.Errorf("item %s completed %d times", id, n)
		}
	}
}

// The live feed and a catch-up read give the same ItemIDs for the same items.
func TestEventsLiveAndCatchUpItemIDsMatch(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	c := fakeServer(t, st)
	ref, _ := c.Open(ctx, loomharness.OpenSpec{Key: "agent-1", Dir: "/repo"})
	st.messages[ref.NativeID] = []map[string]any{
		{"id": "msg_a1", "type": "assistant", "finish": "stop", "content": []map[string]any{
			{"type": "reasoning", "text": "r"}, {"type": "tool", "id": "call_1", "state": map[string]string{"status": "completed"}}, {"type": "text", "text": "t"},
		}},
	}
	page, err := c.Session(ref).Messages(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	m := mapper{seq: map[string]int64{}, turn: map[string]string{}}
	for i, raw := range []string{
		`{"type":"session.reasoning.ended","data":{"sessionID":"s","assistantMessageID":"msg_a1","ordinal":0}}`,
		`{"type":"session.tool.success","data":{"sessionID":"s","assistantMessageID":"msg_a1","id":"call_1"}}`,
		`{"type":"session.text.ended","data":{"sessionID":"s","assistantMessageID":"msg_a1","ordinal":0}}`,
	} {
		e, ok := m.mapEvent([]byte(raw))
		if !ok || e.ItemID != page.Events[i+1].ItemID || e.ItemKind != page.Events[i+1].ItemKind {
			t.Errorf("live %+v vs catch-up %+v", e, page.Events[i+1])
		}
	}
}

func TestEventsFormAsks(t *testing.T) {
	m := mapper{seq: map[string]int64{}, turn: map[string]string{}}
	for _, c := range []struct {
		raw  string
		want loomharness.EventType
	}{
		{`{"type":"form.created","data":{"form":{"id":"frm_1","sessionID":"ses_1","title":"q","fields":[]}}}`, loomharness.EventAskOpened},
		{`{"type":"form.replied","data":{"id":"frm_1","sessionID":"ses_1","answer":{}}}`, loomharness.EventAskResolved},
		{`{"type":"form.cancelled","data":{"id":"frm_2","sessionID":"ses_1"}}`, loomharness.EventAskResolved},
	} {
		e, ok := m.mapEvent([]byte(c.raw))
		if !ok || e.Type != c.want || e.Session.NativeID != "ses_1" || !strings.HasPrefix(e.AskID, "frm_") {
			t.Fatalf("%s -> %+v, %v", c.raw, e, ok)
		}
	}
}

// TestEventsRootAndTurnInputKey: every feed event carries the Root Loom
// opened or resumed its session with (dispatch matches Root plus NativeID),
// and turn.started, emitted right before a turn's first mapped event, carries
// the key of the input that began it, whether OpenCode delivers that input
// after the execution starts (its runner's order) or before. A turn that
// begins with no delivery has no key, and an execution start alone emits
// nothing.
func TestEventsRootAndTurnInputKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st := newStore()
	c := fakeServer(t, st)
	ref, err := c.Open(ctx, loomharness.OpenSpec{Key: "agent-1", Launch: loomharness.Launch{Root: "/root-a"}, Dir: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	sid := ref.NativeID
	e := func(seq int, typ, data string) string {
		return fmt.Sprintf(`{"id":"evt_%d","type":%q,"created":%d,"data":%s,"durable":{"aggregateID":%q,"seq":%d}}`,
			seq, typ, 1000+seq, strings.ReplaceAll(data, "SID", sid), sid, seq)
	}
	st.streams = [][]string{
		{
			e(1, "session.execution.started", `{"sessionID":"SID"}`),
			e(2, "session.inbox.delivered", `{"sessionID":"SID","inboxID":"msg_k1"}`),
			e(3, "session.execution.succeeded", `{"sessionID":"SID"}`),
			e(4, "session.inbox.delivered", `{"sessionID":"SID","inboxID":"msg_k2"}`),
			e(5, "session.execution.started", `{"sessionID":"SID"}`),
			e(6, "session.execution.succeeded", `{"sessionID":"SID"}`),
			e(7, "session.execution.started", `{"sessionID":"SID"}`),
			e(8, "session.text.started", `{"sessionID":"SID","assistantMessageID":"msg_r","ordinal":0}`),
			e(9, "session.execution.succeeded", `{"sessionID":"SID"}`),
			live("session.execution.started", `{"sessionID":"ses_other"}`),
			live("session.inbox.delivered", `{"sessionID":"ses_other","inboxID":"msg_o"}`),
			e(10, "session.execution.started", `{"sessionID":"SID"}`),
		},
	}
	f, err := c.Feed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got []string
	for len(got) < 12 {
		select {
		case ev := <-f.Events():
			got = append(got, fmt.Sprintf("%s %s %s", ev.Type, ev.Session.Root, ev.InputKey))
		case <-ctx.Done():
			t.Fatalf("timed out after %d events:\n%s", len(got), strings.Join(got, "\n"))
		}
	}
	want := []string{
		"turn.started /root-a msg_k1",
		"message.delivered /root-a msg_k1",
		"turn.completed /root-a ",
		"turn.started /root-a msg_k2",
		"message.delivered /root-a msg_k2",
		"turn.completed /root-a ",
		"turn.started /root-a ",
		"item.started /root-a ",
		"turn.completed /root-a ",
		"turn.started  msg_o",
		"message.delivered  msg_o",
		"feed.gap  ",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
