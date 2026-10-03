package opencode

import (
	"context"
	"encoding/json"
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
	// History as OpenCode holds it after the restart: the turn is still
	// open (no idle marker), so it keeps its id across the reconnect.
	st.messages["ses_a"] = []map[string]any{
		{"id": "msg_in", "type": "user"},
		{"id": "msg_a1", "type": "assistant", "content": []map[string]any{{"type": "text", "text": "PAST"}}},
		{"id": "msg_8", "type": "synthetic", "metadata": map[string]string{"notice": "restart"}},
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
	for len(got) < 16 {
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
		"turn.started ses_a msg_in   msg_in  ", // the same turn again, from history (Loom saves it once)
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

// A turn that ends while the stream is down leaves no open turn behind: the
// next message opens its own turn, with its own turn.started and
// turn.completed, instead of joining the ended one.
func TestEventsFeedReconnectAfterUnseenTurnEnd(t *testing.T) {
	st := newStore()
	st.streams = [][]string{
		{
			ev(1, "session.inbox.delivered", `{"sessionID":"ses_a","inboxID":"msg_in"}`),
			ev(2, "session.text.ended", `{"sessionID":"ses_a","assistantMessageID":"msg_a1","ordinal":0,"text":"one"}`),
		},
		{
			ev(5, "session.inbox.delivered", `{"sessionID":"ses_a","inboxID":"msg_in2"}`),
			ev(6, "session.execution.succeeded", `{"sessionID":"ses_a"}`),
		},
	}
	st.messages["ses_a"] = []map[string]any{
		{"id": "msg_in", "type": "user"},
		{"id": "msg_a1", "type": "assistant", "content": []map[string]any{{"type": "text", "text": "one"}}},
		{"id": "msg_idle", "type": "idle"},
		{"id": "msg_in2", "type": "user"},
	}
	c := fakeServer(t, st)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f, err := c.Feed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got []string
	for len(got) < 7 {
		select {
		case e := <-f.Events():
			got = append(got, fmt.Sprintf("%s %s %s %s", e.Type, e.TurnID, e.InputKey, e.StopReason))
		case <-ctx.Done():
			t.Fatalf("timed out after %d events:\n%s", len(got), strings.Join(got, "\n"))
		}
	}
	want := []string{
		"turn.started msg_in msg_in ",
		"message.delivered msg_in msg_in ",
		"item.completed msg_in  ",
		"feed.gap   ",
		"turn.started msg_in2 msg_in2 ",
		"message.delivered msg_in2 msg_in2 ",
		"turn.completed msg_in2  completed",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
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

// TestEventsFormAsks: a form is a question ask (ItemKind question on its
// ask.opened); a permission ask keeps no ItemKind (an approval).
func TestEventsFormAsks(t *testing.T) {
	m := mapper{seq: map[string]int64{}, turn: map[string]string{}}
	if e, ok := m.mapEvent([]byte(`{"type":"permission.asked","data":{"sessionID":"ses_1","id":"per_1"}}`)); !ok || e.ItemKind != "" {
		t.Fatalf("permission.asked -> %+v, %v; want no ItemKind", e, ok)
	}
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
		if want := map[bool]string{true: "question"}[c.want == loomharness.EventAskOpened]; e.ItemKind != want {
			t.Fatalf("%s -> ItemKind %q; want %q", c.raw, e.ItemKind, want)
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

// TestReadSSE: data fields are parsed as the SSE spec says (the name up to
// the first colon, one leading space dropped from the value), so multi-line
// data joins with newlines, "data:" without a space and CRLF line ends work,
// and comment lines and other fields are ignored.
func TestReadSSE(t *testing.T) {
	stream := ": keep-alive comment\n" +
		"event: message\n" +
		"data: {\"a\":\n" +
		"data:1}\n" +
		"\n" +
		"id: 7\r\n" +
		"data:no space\r\n" +
		"retry: 100\r\n" +
		"\r\n" +
		"data:  two spaces\n" +
		"data: x:y\n" +
		"\n" +
		": only a comment\n" +
		"\n" +
		"data: unterminated"
	var got []string
	readSSE(strings.NewReader(stream), func(b []byte) bool { got = append(got, string(b)); return true })
	want := []string{"{\"a\":\n1}", "no space", " two spaces\nx:y"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("events %q; want %q", got, want)
	}

	var first []string
	readSSE(strings.NewReader("data: 1\n\ndata: 2\n\n"), func(b []byte) bool { first = append(first, string(b)); return false })
	if len(first) != 1 {
		t.Fatalf("readSSE went on after fn returned false: %q", first)
	}
}

// TestEventsToolCallCarriesNameInputOutput: a tool call's start carries its
// name and input, and its end adds the output, or the error when it failed;
// a catch-up read of the stored message gives the same tool data.
func TestEventsToolCallCarriesNameInputOutput(t *testing.T) {
	m := newMapper(nil, nil)
	var got []loomharness.Event
	for _, raw := range []string{
		live("session.tool.input.started", `{"sessionID":"s","assistantMessageID":"msg_a1","id":"call_1","name":"bash"}`),
		live("session.tool.called", `{"sessionID":"s","assistantMessageID":"msg_a1","id":"call_1","input":{"command":"ls"},"executed":true}`),
		live("session.tool.success", `{"sessionID":"s","assistantMessageID":"msg_a1","id":"call_1","content":[{"type":"text","text":"a.go\nb.go"}],"executed":true}`),
		live("session.tool.input.started", `{"sessionID":"s","assistantMessageID":"msg_a1","id":"call_2","name":"read"}`),
		live("session.tool.called", `{"sessionID":"s","assistantMessageID":"msg_a1","id":"call_2","input":{"filePath":"x"},"executed":true}`),
		live("session.tool.failed", `{"sessionID":"s","assistantMessageID":"msg_a1","id":"call_2","error":{"type":"tool","message":"no such file"},"executed":true}`),
	} {
		if e, ok := m.mapEvent([]byte(raw)); ok {
			got = append(got, e)
		}
	}
	want := []loomharness.Tool{
		{Name: "bash", Input: `{"command":"ls"}`},
		{Name: "bash", Input: `{"command":"ls"}`, Output: "a.go\nb.go"},
		{Name: "read", Input: `{"filePath":"x"}`},
		{Name: "read", Input: `{"filePath":"x"}`, Output: "no such file", Failed: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(got), len(want), got)
	}
	for i, e := range got {
		if e.ItemKind != "tool" || e.Tool == nil || *e.Tool != want[i] {
			t.Errorf("event %d: %s %+v, want tool %+v", i, e.Type, e.Tool, want[i])
		}
	}

	msg := message{ID: "msg_a1", Type: "assistant", Finish: "stop"}
	if err := json.Unmarshal([]byte(`{"content":[
		{"type":"tool","id":"call_1","name":"bash","state":{"status":"completed","input":{"command":"ls"},"content":[{"type":"text","text":"a.go\nb.go"}]}},
		{"type":"tool","id":"call_2","name":"read","state":{"status":"error","input":{"filePath":"x"},"error":{"type":"tool","message":"no such file"}}}]}`), &msg); err != nil {
		t.Fatal(err)
	}
	stored := msg.events(loomharness.NativeRef{NativeID: "s"})
	for i, e := range stored[:2] {
		if e.Tool == nil || *e.Tool != *got[2*i+1].Tool || e.ItemID != got[2*i+1].ItemID {
			t.Errorf("catch-up %d: %s %+v, live %s %+v", i, e.ItemID, e.Tool, got[2*i+1].ItemID, got[2*i+1].Tool)
		}
	}
}

// TestEventsAsksAndFailureText: a live permission ask says what it asks
// about, a form carries its questions, and a failed execution carries its
// error's message (its type when it has none).
func TestEventsAsksAndFailureText(t *testing.T) {
	m := mapper{seq: map[string]int64{}, turn: map[string]string{}}
	e, ok := m.mapEvent([]byte(`{"type":"permission.asked","data":{"sessionID":"ses_1","id":"per_1","action":"bash","resources":["rm -rf build"]}}`))
	if !ok || e.Text != "bash rm -rf build" {
		t.Fatalf("permission.asked -> %+v", e)
	}
	e, _ = m.mapEvent([]byte(`{"type":"permission.asked","data":{"sessionID":"ses_1","id":"per_2","action":"edit","resources":["a.go"],"message":"Edit a.go?","metadata":{"files":[{"file":"a.go","patch":"@@ -1 +1 @@"}]}}}`))
	if e.Text != "Edit a.go?\n@@ -1 +1 @@" {
		t.Fatalf("edit ask Text = %q", e.Text)
	}
	e, _ = m.mapEvent([]byte(`{"type":"form.created","data":{"form":{"id":"frm_1","sessionID":"ses_1","title":"Questions","fields":[{"key":"q0","type":"multiselect","title":"Pick","description":"Which ones?","options":[{"value":"a","label":"A"}]}]}}}`))
	if len(e.Questions) != 1 || e.Text != "Which ones?" || !e.Questions[0].MultiSelect || e.Questions[0].Options[0].Label != "A" {
		t.Fatalf("form.created -> %+v", e)
	}
	for raw, want := range map[string]string{
		`{"type":"session.execution.failed","data":{"sessionID":"ses_1","error":{"type":"provider.invalid-output","message":"tool call delta is missing id"}}}`: "tool call delta is missing id",
		`{"type":"session.execution.failed","data":{"sessionID":"ses_1","error":{"type":"provider.auth"}}}`:                                                     "provider.auth",
		`{"type":"session.execution.succeeded","data":{"sessionID":"ses_1"}}`:                                                                                   "",
	} {
		e, ok := m.mapEvent([]byte(raw))
		if !ok || e.Type != loomharness.EventTurnCompleted || e.Error != want {
			t.Fatalf("%s -> %+v; want Error %q", raw, e, want)
		}
	}
}

// OC1: a rejected permission makes OpenCode b30c4d0 interrupt its own step,
// and with no stop reason the execution ends as a "shutdown" interrupt
// (core/src/session/execution.ts terminal) that writes no idle marker. The
// sequence below is the real build's, recorded by a reject on a shell ask.
// After a reject that interrupt ends the turn as declined; a real shutdown
// still ends nothing, and a reject's mark lasts only its own turn.
func TestEventsDeclineEndsTheTurn(t *testing.T) {
	m := newMapper(nil, nil)
	var got []loomharness.Event
	for _, raw := range []string{
		ev(1, "session.inbox.delivered", `{"sessionID":"ses_a","inboxID":"msg_in"}`),
		ev(2, "session.tool.called", `{"sessionID":"ses_a","assistantMessageID":"msg_a1","id":"call_1"}`),
		live("permission.asked", `{"sessionID":"ses_a","id":"per_1","action":"shell","resources":["echo hi"]}`),
		live("permission.replied", `{"sessionID":"ses_a","requestID":"per_1","reply":"reject"}`),
		ev(3, "session.tool.failed", `{"sessionID":"ses_a","assistantMessageID":"msg_a1","id":"call_1","error":{"type":"aborted","message":"The user declined this tool call"},"executed":false}`),
		ev(4, "session.execution.interrupted", `{"sessionID":"ses_a","reason":"shutdown"}`),
		ev(5, "session.inbox.delivered", `{"sessionID":"ses_a","inboxID":"msg_in2"}`),
		ev(6, "session.execution.interrupted", `{"sessionID":"ses_a","reason":"shutdown"}`),
	} {
		got = append(got, m.process([]byte(raw))...)
	}
	var ends []loomharness.Event
	for _, e := range got {
		if e.Type == loomharness.EventTurnCompleted {
			ends = append(ends, e)
		}
	}
	if len(ends) != 1 || ends[0].StopReason != "declined" || ends[0].TurnID != "msg_in" || ends[0].Error != "" {
		t.Fatalf("turn ends = %+v; want one, the declined turn msg_in, with no error", ends)
	}
}
