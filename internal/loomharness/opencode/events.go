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
	m := mapper{seq: map[string]int64{}, turn: map[string]string{}}
	for {
		readSSE(body, func(data []byte) bool {
			e, ok := m.mapEvent(data)
			return !ok || f.send(ctx, e)
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
		if v, ok := strings.CutPrefix(line, "data:"); ok {
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
// item, and stamps each session's events with the current execution's id.
type mapper struct {
	seq  map[string]int64
	turn map[string]string
}

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
	if w.Type == "session.execution.started" {
		m.turn[sid] = w.ID
	}
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
	case "session.execution.started":
		e.Type = loomharness.EventTurnStarted
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
		e.Type = loomharness.EventUsage
	case "session.execution.succeeded", "session.execution.interrupted", "session.execution.failed":
		e.Type, e.StopReason = loomharness.EventTurnCompleted, stopReason(lastDot(w.Type))
		delete(m.turn, sid)
	case "permission.asked", "form.created":
		e.Type, e.AskID = loomharness.EventAskOpened, d.ID
	case "form.replied", "form.cancelled":
		e.Type, e.AskID = loomharness.EventAskResolved, d.ID
	case "permission.replied":
		e.Type, e.AskID = loomharness.EventAskResolved, d.RequestID
	case "session.synthetic":
		e.Type, e.Text = loomharness.EventTurnResumed, d.Text
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
