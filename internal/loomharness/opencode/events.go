package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// Feed opens the shared GET /api/event stream. It reconnects whenever the
// stream ends (an OpenCode restart included) and emits feed.gap after each
// reconnect, because events in between are lost. It ends when ctx ends or on
// Close.
func (c *Client) Feed(ctx context.Context) (loomharness.Feed, error) {
	ctx, cancel := context.WithCancel(ctx)
	body, err := c.stream(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	f := &feed{ch: make(chan loomharness.Event, 64), cancel: cancel, done: make(chan struct{})}
	go f.run(ctx, c, body)
	return f, nil
}

type feed struct {
	ch     chan loomharness.Event
	cancel context.CancelFunc
	done   chan struct{}
}

func (f *feed) Events() <-chan loomharness.Event { return f.ch }

func (f *feed) Close() error {
	f.cancel()
	<-f.done
	return nil
}

func (f *feed) run(ctx context.Context, c *Client, body io.ReadCloser) {
	defer close(f.done)
	defer close(f.ch)
	m := newMapper(c.rootOf, func(sid, anchor string) (string, string, bool) {
		id, key, ok, err := c.Session(loomharness.NativeRef{NativeID: sid}).turnOf(ctx, anchor)
		return id, key, ok && err == nil
	})
	for {
		readSSE(body, func(data []byte) bool {
			for _, e := range m.process(data) {
				if !f.send(ctx, e) {
					return false
				}
			}
			return true
		})
		_ = body.Close()
		for wait := 100 * time.Millisecond; ; wait = min(wait*2, 2*time.Second) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			var err error
			if body, err = c.stream(ctx); err == nil {
				break
			}
		}
		if !f.send(ctx, loomharness.Event{Type: loomharness.EventFeedGap, Time: time.Now()}) {
			return
		}
	}
}

func (f *feed) send(ctx context.Context, e loomharness.Event) bool {
	select {
	case f.ch <- e:
		return true
	case <-ctx.Done():
		return false
	}
}

func (c *Client) stream(ctx context.Context) (io.ReadCloser, error) {
	resp, err := c.do(ctx, "GET", "/api/event", nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("opencode GET /api/event: %w", translate(resp.StatusCode, b))
	}
	return resp.Body, nil
}

// readSSE calls fn with each event's data until the stream ends or fn
// returns false.
func readSSE(r io.Reader, fn func([]byte) bool) {
	br := bufio.NewReader(r)
	var data []string
	for {
		line, err := br.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		// A field is its name up to the first colon, then the value with one
		// leading space removed (the SSE spec); only data fields matter here.
		if field, v, _ := strings.Cut(line, ":"); field == "data" {
			data = append(data, strings.TrimPrefix(v, " "))
		} else if line == "" && len(data) > 0 {
			if !fn([]byte(strings.Join(data, "\n"))) {
				return
			}
			data = nil
		}
		if err != nil {
			return
		}
	}
}

// mapper turns native events into port events. It drops durable events it
// has already passed on (by per-session seq), so a replay never repeats an
// item, and gives each session's turn events the same ids Messages gives.
//
// A turn is what OpenCode's history holds between two idle markers
// (schema/src/session-message.ts:282-292). History stores no execution
// start, so a turn's id is the id of its first message that the feed maps:
// the delivered input (its InputKey), the restart notice, the first
// assistant step, or the idle marker of an empty turn. Live, turn.started is
// emitted when a session's first such event arrives while no turn is open,
// right before it, with the id OpenCode's history gives that turn
// (Session.turnOf); session.execution.started itself is not mapped. A
// shutdown interrupt records no idle marker and the resumed execution
// continues the same turn (session-message.ts:283-285), so it ends nothing.
type mapper struct {
	seq  map[string]int64
	turn map[string]string // the open turn's id, per session
	// root gives the Root of a session Loom opened or resumed ("" for any
	// other session); every event carries it, since dispatch matches Root
	// plus NativeID.
	root func(nativeID string) string
	// lookup finds the turn holding a stored message in the session's
	// history: its id and InputKey. Nil, or not found, uses the anchor.
	lookup func(sid, anchor string) (id, key string, ok bool)
	last   string // the native id of the event mapEvent last mapped
}

func newMapper(root func(string) string, lookup func(string, string) (string, string, bool)) *mapper {
	return &mapper{seq: map[string]int64{}, turn: map[string]string{}, root: root, lookup: lookup}
}

// process maps one native event to the port events it releases, in order.
func (m *mapper) process(raw []byte) []loomharness.Event {
	e, ok := m.mapEvent(raw)
	if !ok {
		return nil
	}
	if m.root != nil {
		e.Session.Root = m.root(e.Session.NativeID)
	}
	sid := e.Session.NativeID
	var out []loomharness.Event
	if _, open := m.turn[sid]; !open && opensTurn(e.Type) {
		anchor := m.anchor(e)
		id, key := anchor, ""
		if e.Type == loomharness.EventMessageDelivered {
			key = e.InputKey
		}
		if m.lookup != nil {
			if lid, lkey, found := m.lookup(sid, anchor); found {
				id, key = lid, lkey
			}
		}
		m.turn[sid] = id
		out = append(out, loomharness.Event{Type: loomharness.EventTurnStarted, Session: e.Session, TurnID: id, InputKey: key, Time: e.Time})
	}
	if opensTurn(e.Type) || e.Type == loomharness.EventAskOpened || e.Type == loomharness.EventAskResolved {
		e.TurnID = m.turn[sid]
	}
	if e.Type == loomharness.EventTurnCompleted {
		delete(m.turn, sid)
	}
	return append(out, e)
}

// opensTurn: the event types that belong to a turn in OpenCode's history.
// Asks and subagent starts can happen outside one.
func opensTurn(t loomharness.EventType) bool {
	switch t {
	case loomharness.EventMessageDelivered, loomharness.EventTurnResumed, loomharness.EventItemStarted,
		loomharness.EventDelta, loomharness.EventItemCompleted, loomharness.EventUsage, loomharness.EventTurnCompleted:
		return true
	}
	return false
}

// anchor is the id of the stored message e belongs to.
func (m *mapper) anchor(e loomharness.Event) string {
	switch e.Type {
	case loomharness.EventMessageDelivered:
		return e.InputKey
	case loomharness.EventTurnResumed, loomharness.EventTurnCompleted:
		return messageID(m.last)
	}
	msg, _, _ := strings.Cut(e.ItemID, "/")
	return msg
}

// messageID is the id OpenCode gives the message an event creates
// (SessionMessage.ID.fromEvent, schema/src/session-message.ts:23-29).
func messageID(eventID string) string { return "msg_" + strings.TrimPrefix(eventID, "evt_") }

type wireEvent struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Created int64  `json:"created"`
	Durable *struct {
		AggregateID string `json:"aggregateID"`
		Seq         int64  `json:"seq"`
	} `json:"durable"`
	Data struct {
		SessionID          string `json:"sessionID"`
		ParentID           string `json:"parentID"`
		Reason             string `json:"reason"`
		AssistantMessageID string `json:"assistantMessageID"`
		Ordinal            int    `json:"ordinal"`
		ID                 string `json:"id"`
		InboxID            string `json:"inboxID"`
		RequestID          string `json:"requestID"`
		Text               string `json:"text"`
		Delta              string `json:"delta"`
		Metadata           struct {
			Notice string `json:"notice"`
		} `json:"metadata"`
		Form struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
		} `json:"form"` // form.created
	} `json:"data"`
}

func (m *mapper) mapEvent(raw []byte) (loomharness.Event, bool) {
	var w wireEvent
	if json.Unmarshal(raw, &w) != nil {
		return loomharness.Event{}, false
	}
	if w.Type == "form.created" {
		w.Data.SessionID, w.Data.ID = w.Data.Form.SessionID, w.Data.Form.ID
	}
	if w.Data.SessionID == "" {
		return loomharness.Event{}, false
	}
	if w.Durable != nil {
		if last, ok := m.seq[w.Durable.AggregateID]; ok && w.Durable.Seq <= last {
			return loomharness.Event{}, false
		}
		m.seq[w.Durable.AggregateID] = w.Durable.Seq
	}
	sid := w.Data.SessionID
	m.last = w.ID
	e := loomharness.Event{
		Session: loomharness.NativeRef{NativeID: sid},
		TurnID:  m.turn[sid],
		Time:    time.UnixMilli(w.Created),
	}
	if w.Durable != nil {
		e.Seq = w.Durable.Seq
	}
	if !m.fill(&e, w) {
		return loomharness.Event{}, false
	}
	return e, true
}

// fill sets e's type and native ids from w; false means the event is not mapped.
func (m *mapper) fill(e *loomharness.Event, w wireEvent) bool {
	d, sid := w.Data, w.Data.SessionID
	_, rest, _ := strings.Cut(w.Type, ".")
	part, _, _ := strings.Cut(rest, ".") // text | reasoning | tool | ...
	switch w.Type {
	case "session.inbox.delivered":
		e.Type, e.ItemKind, e.ItemID, e.InputKey = loomharness.EventMessageDelivered, "message", d.InboxID, d.InboxID
	case "session.text.delta", "session.reasoning.delta":
		e.Type, e.ItemKind, e.ItemID, e.Text = loomharness.EventDelta, kind(part), partItem(d.AssistantMessageID, part, d.Ordinal), d.Delta
	case "session.text.started", "session.reasoning.started":
		e.Type, e.ItemKind, e.ItemID = loomharness.EventItemStarted, kind(part), partItem(d.AssistantMessageID, part, d.Ordinal)
	case "session.text.ended", "session.reasoning.ended":
		e.Type, e.ItemKind, e.ItemID, e.Text = loomharness.EventItemCompleted, kind(part), partItem(d.AssistantMessageID, part, d.Ordinal), d.Text
	case "session.tool.called":
		e.Type, e.ItemKind, e.ItemID = loomharness.EventItemStarted, "tool", toolItem(d.AssistantMessageID, d.ID)
	case "session.tool.success", "session.tool.failed":
		e.Type, e.ItemKind, e.ItemID = loomharness.EventItemCompleted, "tool", toolItem(d.AssistantMessageID, d.ID)
	case "session.step.ended":
		e.Type, e.ItemID = loomharness.EventUsage, d.AssistantMessageID
	case "session.execution.interrupted":
		if d.Reason == "shutdown" {
			return false // the turn goes on after the restart
		}
		e.Type, e.StopReason = loomharness.EventTurnCompleted, stopReason(lastDot(w.Type))
	case "session.execution.succeeded", "session.execution.failed":
		e.Type, e.StopReason = loomharness.EventTurnCompleted, stopReason(lastDot(w.Type))
	case "permission.asked":
		e.Type, e.AskID = loomharness.EventAskOpened, d.ID
	case "form.created":
		e.Type, e.ItemKind, e.AskID = loomharness.EventAskOpened, "question", d.ID
	case "form.replied", "form.cancelled":
		e.Type, e.AskID = loomharness.EventAskResolved, d.ID
	case "permission.replied":
		e.Type, e.AskID = loomharness.EventAskResolved, d.RequestID
	case "session.synthetic":
		e.Type, e.ItemID, e.Text = loomharness.EventTurnResumed, messageID(w.ID), d.Text
		return d.Metadata.Notice == "restart"
	case "session.created":
		e.Type, e.Session.NativeID, e.TurnID, e.ItemID = loomharness.EventSubagentStarted, d.ParentID, m.turn[d.ParentID], sid
		return d.ParentID != ""
	default:
		return false
	}
	return true
}

func lastDot(s string) string { return s[strings.LastIndex(s, ".")+1:] }

// partItem is the ItemID of a text or reasoning part, the same in the live
// feed and in Messages.
func partItem(messageID, part string, ordinal int) string {
	return messageID + "/" + part + "/" + strconv.Itoa(ordinal)
}

func toolItem(messageID, callID string) string { return messageID + "/tool/" + callID }

func kind(part string) string {
	if part == "text" {
		return "message"
	}
	return part
}

func stopReason(outcome string) string {
	switch outcome {
	case "succeeded":
		return "completed"
	case "interrupted":
		return "cancelled"
	}
	return "failed"
}
