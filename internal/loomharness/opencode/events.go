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
		// A turn may have ended unseen while the stream was down; one still
		// running gets its id back from history (lookup) on its next event.
		clear(m.turn)
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
// returns false. It only parses the stream OpenCode sends (GET /api/event)
// and never writes SSE frames; Loom's own SSE output goes through the
// realtime writer.
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
// A rejected permission is the exception: OpenCode then interrupts its own
// step with no reason, so the execution also ends as a "shutdown" interrupt
// with no idle marker (core/src/session/runner/step.ts, execution.ts
// terminal), though nothing restarts and nothing runs. After a reject in
// the session that interrupt ends the turn as declined.
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
	// tools holds a started tool call's name and input, by its item id,
	// until the call ends: OpenCode's end event names neither.
	tools map[string]loomharness.Tool
	// declined marks a session whose open turn had a permission rejected.
	declined map[string]bool
}

func newMapper(root func(string) string, lookup func(string, string) (string, string, bool)) *mapper {
	return &mapper{seq: map[string]int64{}, turn: map[string]string{}, root: root, lookup: lookup, tools: map[string]loomharness.Tool{}, declined: map[string]bool{}}
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
		delete(m.declined, sid)
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
		SessionID          string  `json:"sessionID"`
		ParentID           string  `json:"parentID"`
		Reason             string  `json:"reason"`
		AssistantMessageID string  `json:"assistantMessageID"`
		Ordinal            int     `json:"ordinal"`
		ID                 string  `json:"id"`
		InboxID            string  `json:"inboxID"`
		RequestID          string  `json:"requestID"`
		Text               string  `json:"text"`
		Delta              string  `json:"delta"`
		Cost               float64 `json:"cost"`   // session.step.ended
		Tokens             tokens  `json:"tokens"` // session.step.ended
		Metadata           struct {
			Notice string     `json:"notice"`
			Files  []fileDiff `json:"files"` // permission.asked for an edit
		} `json:"metadata"`
		Form      form            `json:"form"`      // form.created
		Name      string          `json:"name"`      // session.tool.input.started
		Input     json.RawMessage `json:"input"`     // session.tool.called
		Content   toolContent     `json:"content"`   // session.tool.success and failed
		Error     *toolError      `json:"error"`     // session.tool.failed and session.execution.failed
		Action    string          `json:"action"`    // permission.asked
		Resources []string        `json:"resources"` // permission.asked
		Message   string          `json:"message"`   // permission.asked
		Reply     string          `json:"reply"`     // permission.replied: once | always | reject
	} `json:"data"`
}

// fileDiff is one file a permission ask would change (FileDiff.Info).
type fileDiff struct {
	File  string `json:"file"`
	Patch string `json:"patch"`
}

// permissionAbout is what a permission ask asks about: its message, or its
// action and resources (a command, a path), then any file patches.
func permissionAbout(action, message string, resources []string, files []fileDiff) string {
	about := message
	if about == "" {
		about = strings.TrimSpace(action + " " + strings.Join(resources, "\n"))
	}
	for _, f := range files {
		about += "\n" + f.Patch
	}
	return about
}

// form is a form ask (Form.Info); OpenCode's question tool asks with one,
// a field per question (keys q0, q1, ...).
type form struct {
	ID        string      `json:"id"`
	SessionID string      `json:"sessionID"`
	Title     string      `json:"title"`
	Fields    []formField `json:"fields"`
}

type formField struct {
	Key         string `json:"key"`
	Type        string `json:"type"` // string | number | integer | boolean | multiselect | external
	Title       string `json:"title"`
	Description string `json:"description"`
	Options     []struct {
		Value       string `json:"value"`
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
}

// questions are the form's fields as questions: a field's title is the
// header and its description the question.
func (f form) questions() []loomharness.Question {
	var out []loomharness.Question
	for _, fl := range f.Fields {
		q := loomharness.Question{ID: fl.Key, Header: fl.Title, Question: fl.Description, MultiSelect: fl.Type == "multiselect"}
		if q.Question == "" {
			q.Header, q.Question = f.Title, fl.Title
		}
		for _, o := range fl.Options {
			q.Options = append(q.Options, loomharness.Choice{Label: o.Label, Description: o.Description})
		}
		out = append(out, q)
	}
	return out
}

// value is answers (option labels or text) as this field's form value.
func (fl formField) value(answers []string) any {
	vals := make([]string, 0, len(answers))
	for _, a := range answers {
		for _, o := range fl.Options {
			if o.Label == a {
				a = o.Value
				break
			}
		}
		vals = append(vals, a)
	}
	first := ""
	if len(vals) > 0 {
		first = vals[0]
	}
	switch fl.Type {
	case "multiselect":
		return vals
	case "number", "integer":
		if n, err := strconv.ParseFloat(first, 64); err == nil {
			return n
		}
	case "boolean":
		if b, err := strconv.ParseBool(first); err == nil {
			return b
		}
	}
	return first
}

// toolContent is a tool call's result parts; the chat shows their text.
type toolContent []struct {
	Type string `json:"type"` // text | file
	Text string `json:"text"`
	URI  string `json:"uri"`
}

func (c toolContent) text() string {
	var out []string
	for _, p := range c {
		switch p.Type {
		case "text":
			out = append(out, p.Text)
		case "file":
			out = append(out, p.URI)
		}
	}
	return strings.Join(out, "\n")
}

// toolError is a failed tool call's or execution's structured error.
type toolError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// text is the error's message, or its type when it has none.
func (e *toolError) text() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return e.Type
	}
	return e.Message
}

// failure is a failed execution's class, from its error type; nil when it
// has no error. OpenCode 2.0.19 types a provider failure by its reason:
// RateLimit, QuotaExceeded, Authentication, Transport, ProviderInternal.
func (e *toolError) failure() *loomharness.Failure {
	if e == nil {
		return nil
	}
	switch e.Type {
	case "provider.rate-limit", "provider.quota":
		return &loomharness.Failure{Class: loomharness.FailureUsageLimit, Retryable: true}
	case "provider.auth":
		return &loomharness.Failure{Class: loomharness.FailureAuth}
	case "provider.transport", "provider.timeout", "provider.connect", "provider.internal":
		return &loomharness.Failure{Class: loomharness.FailureProvider, Retryable: true}
	}
	return &loomharness.Failure{Class: loomharness.FailureProvider}
}

// failedStep is a failed assistant step's error type; "" for any other message.
func (m message) failedStep() string {
	var err toolError
	if m.Type != "assistant" || json.Unmarshal(m.Error, &err) != nil {
		return ""
	}
	return err.Type
}

// turnEvents is m's events in turn; a failed idle marker is classed by
// failed, the error type of the turn's failed step, which
// session.step.failed stores on the assistant message in the shape
// session.execution.failed gives the live feed.
func (m message) turnEvents(ref loomharness.NativeRef, turn, failed string) []loomharness.Event {
	out := m.events(ref)
	for i := range out {
		out[i].TurnID = turn
		if out[i].Type == loomharness.EventTurnCompleted && out[i].StopReason == "failed" && failed != "" {
			out[i].Failure = (&toolError{Type: failed}).failure()
		}
	}
	return out
}

// toolInput is a tool call's input object as text; "" when it has none.
func toolInput(raw json.RawMessage) string {
	switch string(raw) {
	case "", "null", "{}":
		return ""
	}
	return string(raw)
}

// toolOutput is an ended tool call's output text and whether it failed.
func toolOutput(c toolContent, err *toolError) (string, bool) {
	out := c.text()
	if err == nil {
		return out, false
	}
	if out == "" {
		return err.Message, true
	}
	return err.Message + "\n" + out, true
}

// tokens is OpenCode's per-step TokenUsageInfo, on session.step.ended and
// on the assistant message the step ended.
type tokens struct {
	Input     int64 `json:"input"`
	Output    int64 `json:"output"`
	Reasoning int64 `json:"reasoning"`
	Cache     struct {
		Read  int64 `json:"read"`
		Write int64 `json:"write"`
	} `json:"cache"`
}

// usage is the step's usage; OpenCode counts reasoning apart from output.
func (t tokens) usage(cost float64) loomharness.Usage {
	return loomharness.Usage{InputTokens: t.Input, OutputTokens: t.Output + t.Reasoning,
		CacheReadTokens: t.Cache.Read, CacheWriteTokens: t.Cache.Write, CostUSD: cost}
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
	case "session.tool.input.started", "session.tool.called", "session.tool.success", "session.tool.failed":
		return m.tool(e, w)
	case "session.step.ended":
		e.Type, e.ItemID, e.Usage = loomharness.EventUsage, d.AssistantMessageID, d.Tokens.usage(d.Cost)
	case "session.execution.interrupted":
		e.Type, e.StopReason = loomharness.EventTurnCompleted, stopReason(lastDot(w.Type))
		if d.Reason == "shutdown" {
			e.StopReason = "declined"
			return m.declined[sid] // else the turn goes on after the restart
		}
	case "session.execution.succeeded", "session.execution.failed":
		e.Type, e.StopReason = loomharness.EventTurnCompleted, stopReason(lastDot(w.Type))
		e.Error, e.Failure = d.Error.text(), d.Error.failure()
	case "permission.asked":
		e.Type, e.AskID = loomharness.EventAskOpened, d.ID
		e.Text = permissionAbout(d.Action, d.Message, d.Resources, d.Metadata.Files)
	case "form.created":
		e.Type, e.ItemKind, e.AskID = loomharness.EventAskOpened, "question", d.ID
		e.Questions = d.Form.questions()
		if len(e.Questions) > 0 {
			e.Text = e.Questions[0].Question
		}
	case "form.replied", "form.cancelled":
		e.Type, e.AskID = loomharness.EventAskResolved, d.ID
	case "permission.replied":
		e.Type, e.AskID = loomharness.EventAskResolved, d.RequestID
		if d.Reply == "reject" && m.declined != nil {
			m.declined[sid] = true
		}
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
// tool maps a tool call's events. Its input start names the tool and maps
// to nothing; its call starts the item with its input; its success or
// failure completes it with the output, or the error.
func (m *mapper) tool(e *loomharness.Event, w wireEvent) bool {
	d := w.Data
	id := toolItem(d.AssistantMessageID, d.ID)
	t := m.tools[id]
	switch w.Type {
	case "session.tool.input.started":
		m.setTool(id, loomharness.Tool{Name: d.Name})
		return false
	case "session.tool.called":
		t.Input = toolInput(d.Input)
		m.setTool(id, t)
		e.Type = loomharness.EventItemStarted
	default:
		delete(m.tools, id)
		t.Output, t.Failed = toolOutput(d.Content, d.Error)
		e.Type = loomharness.EventItemCompleted
	}
	e.ItemKind, e.ItemID, e.Tool = "tool", id, &t
	return true
}

func (m *mapper) setTool(id string, t loomharness.Tool) {
	if m.tools == nil {
		m.tools = map[string]loomharness.Tool{}
	}
	m.tools[id] = t
}

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
