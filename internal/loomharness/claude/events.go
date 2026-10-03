package claude

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// mapper turns one session's stream-json frames into port events (design v2
// §5.2). Claude sends no turn start, so a turn starts at the first model
// frame (stream_event, assistant, a non-replay user tool result, or a frame
// carrying resume_reason) after a result. Item ids are built from the native message id and block, so the
// live feed and a transcript read can agree: <msg>/text/<index>,
// <msg>/reasoning/<index>, <msg>/tool/<tool_use_id>.
type mapper struct {
	ref             loomharness.NativeRef
	seq             int64
	turnID          string                      // "" between turns
	msgID           string                      // the streaming assistant message
	open            int                         // the open content block's index
	toolMsg         map[string]string           // tool_use id -> its message, until its result
	tools           map[string]loomharness.Tool // tool_use id -> its name and input, until its result
	pending         map[string]bool             // prompted keys not yet delivered
	handed          string                      // the prompted key no turn has started for yet
	cancelled       bool                        // Loom interrupted the running turn
	lastInterrupted bool
	usage           loomharness.Usage // the running turn's steps so far
}

func newMapper(ref loomharness.NativeRef) *mapper {
	return &mapper{ref: ref, toolMsg: map[string]string{}, tools: map[string]loomharness.Tool{}, pending: map[string]bool{}}
}

type block struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ToolUseID string          `json:"tool_use_id"`
	Name      string          `json:"name"`     // tool_use
	Input     json.RawMessage `json:"input"`    // tool_use
	Content   json.RawMessage `json:"content"`  // tool_result: a string or text blocks
	IsError   bool            `json:"is_error"` // tool_result
}

// result is a tool_result's content as text: a plain string, or its text
// blocks joined.
func (b block) result() string {
	var s string
	if json.Unmarshal(b.Content, &s) == nil {
		return s
	}
	var parts []block
	_ = json.Unmarshal(b.Content, &parts)
	var text []string
	for _, p := range parts {
		if p.Type == "text" {
			text = append(text, p.Text)
		}
	}
	return strings.Join(text, "\n")
}

// input is a tool_use's input as text; a streamed start has none yet.
func (b block) input() string {
	if len(b.Input) == 0 || string(b.Input) == "{}" || string(b.Input) == "null" {
		return ""
	}
	return string(b.Input)
}

type wireFrame struct {
	Type             string   `json:"type"`
	Subtype          string   `json:"subtype"`
	IsReplay         bool     `json:"isReplay"`
	UserMessageUUIDs []string `json:"user_message_uuids"`
	UserMessageUUID  string   `json:"user_message_uuid"`
	ResumeReason     string   `json:"resume_reason"`
	TaskType         string   `json:"task_type"`
	TaskID           string   `json:"task_id"`
	TotalCostUSD     float64  `json:"total_cost_usd"` // result: the session's running total
	Event            struct {
		Type    string `json:"type"`
		Index   int    `json:"index"`
		Message struct {
			ID    string
			Usage wireUsage `json:"usage"`
		} `json:"message"` // message_start
		Usage        wireUsage `json:"usage"` // message_delta
		ContentBlock block     `json:"content_block"`
		Delta        struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"delta"`
	} `json:"event"`
	Message struct {
		ID      string          `json:"id"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// wireUsage is a model step's API usage. message_start gives its input and
// cache counts; message_delta its final output count.
type wireUsage struct {
	Input      int64 `json:"input_tokens"`
	Output     int64 `json:"output_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
}

func (m *mapper) frame(raw []byte) []loomharness.Event {
	var f wireFrame
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	var out []loomharness.Event
	emit := func(e loomharness.Event) {
		m.seq++
		e.Session, e.TurnID, e.Seq, e.Time = m.ref, m.turnID, m.seq, time.Now()
		out = append(out, e)
	}
	if startsTurn(f) && m.turnID == "" {
		// A prompt is handed only while idle, so the next turn is its turn;
		// a turn Claude starts by itself carries no key.
		m.turnID = uuid.NewString()
		emit(loomharness.Event{Type: loomharness.EventTurnStarted, InputKey: m.handed})
		m.handed = ""
	}
	if f.Type == "stream_event" || f.Type == "assistant" {
		m.delivered(append(f.UserMessageUUIDs, f.UserMessageUUID), emit)
	}
	if f.ResumeReason != "" {
		emit(loomharness.Event{Type: loomharness.EventTurnResumed, Text: f.ResumeReason})
	}
	var blocks []block
	_ = json.Unmarshal(f.Message.Content, &blocks) // a plain-string content has no blocks
	switch {
	case f.Type == "stream_event":
		m.stream(f, emit)
	case f.Type == "assistant":
		m.assistant(f.Message.ID, blocks, emit)
	case f.Type == "user" && !f.IsReplay:
		m.toolResults(blocks, emit)
	case f.Type == "system" && f.Subtype == "task_started" && f.TaskType == "local_agent":
		emit(loomharness.Event{Type: loomharness.EventSubagentStarted, ItemID: f.TaskID})
	case f.Type == "result":
		m.result(f.Subtype, f.TotalCostUSD, emit)
	}
	return out
}

// startsTurn reports whether f is a model frame (or a result) that begins a
// turn when none runs.
func startsTurn(f wireFrame) bool {
	return f.Type == "stream_event" || f.Type == "assistant" || f.Type == "result" ||
		(f.Type == "user" && !f.IsReplay) || f.ResumeReason != ""
}

// delivered emits message.delivered once for each prompted key a reply
// frame names in user_message_uuids.
func (m *mapper) delivered(keys []string, emit func(loomharness.Event)) {
	for _, key := range keys {
		if m.pending[key] {
			delete(m.pending, key)
			emit(loomharness.Event{Type: loomharness.EventMessageDelivered, ItemKind: "message", ItemID: key, InputKey: key})
		}
	}
}

// toolResults completes the tool calls whose results a user frame carries.
func (m *mapper) toolResults(blocks []block, emit func(loomharness.Event)) {
	for _, b := range blocks {
		if msg, ok := m.toolMsg[b.ToolUseID]; ok && b.Type == "tool_result" {
			t := m.tools[b.ToolUseID]
			t.Output, t.Failed = b.result(), b.IsError
			delete(m.toolMsg, b.ToolUseID)
			delete(m.tools, b.ToolUseID)
			emit(loomharness.Event{Type: loomharness.EventItemCompleted, ItemKind: "tool", ItemID: msg + "/tool/" + b.ToolUseID, Tool: &t})
		}
	}
}

// stream maps a partial-message stream_event.
func (m *mapper) stream(f wireFrame, emit func(loomharness.Event)) {
	ev := f.Event
	switch ev.Type {
	case "message_start":
		m.msgID = ev.Message.ID
		u := ev.Message.Usage
		m.usage.InputTokens += u.Input
		m.usage.CacheReadTokens += u.CacheRead
		m.usage.CacheWriteTokens += u.CacheWrite
	case "message_delta":
		m.usage.OutputTokens += ev.Usage.Output
	case "content_block_start":
		m.open = ev.Index
		if kind, id := m.item(m.msgID, ev.Index, ev.ContentBlock); id != "" {
			emit(loomharness.Event{Type: loomharness.EventItemStarted, ItemKind: kind, ItemID: id, Tool: m.tool(ev.ContentBlock)})
		}
	case "content_block_delta":
		switch ev.Delta.Type {
		case "text_delta":
			emit(loomharness.Event{Type: loomharness.EventDelta, ItemKind: "message", ItemID: partItem(m.msgID, "text", ev.Index), Text: ev.Delta.Text})
		case "thinking_delta":
			emit(loomharness.Event{Type: loomharness.EventDelta, ItemKind: "reasoning", ItemID: partItem(m.msgID, "reasoning", ev.Index), Text: ev.Delta.Thinking})
		}
	}
}

// assistant maps an assistant frame: it completes its text and thinking
// blocks and starts a tool call that was not streamed. With partial messages
// each frame of the streaming message carries one block, the open one;
// otherwise the frame is the whole message, so each block's index is its
// position.
func (m *mapper) assistant(msg string, blocks []block, emit func(loomharness.Event)) {
	for i, b := range blocks {
		index := i
		if len(blocks) == 1 && msg == m.msgID {
			index = m.open
		}
		switch b.Type {
		case "text":
			emit(loomharness.Event{Type: loomharness.EventItemCompleted, ItemKind: "message", ItemID: partItem(msg, "text", index), Text: b.Text})
		case "thinking":
			emit(loomharness.Event{Type: loomharness.EventItemCompleted, ItemKind: "reasoning", ItemID: partItem(msg, "reasoning", index), Text: b.Thinking})
		case "tool_use":
			_, seen := m.toolMsg[b.ID]
			kind, id := m.item(msg, index, b) // the whole block: its input is final
			if !seen {
				emit(loomharness.Event{Type: loomharness.EventItemStarted, ItemKind: kind, ItemID: id, Tool: m.tool(b)})
			}
		}
	}
}

// result ends the turn: completed on success, cancelled when Loom
// interrupted it, else failed. Its usage is the sum of the turn's steps (the
// result's own usage may be a running total). Its cost is the session's
// running total_cost_usd, which a resumed process continues; loomagent saves
// its rise since the session's last saved total.
func (m *mapper) result(subtype string, total float64, emit func(loomharness.Event)) {
	m.usage.CostTotalUSD = total
	emit(loomharness.Event{Type: loomharness.EventUsage, ItemID: m.turnID + "/usage", Usage: m.usage})
	m.usage = loomharness.Usage{}
	stop := "failed"
	switch {
	case subtype == "success":
		stop = "completed"
	case m.cancelled:
		stop = "cancelled"
	}
	emit(loomharness.Event{Type: loomharness.EventTurnCompleted, StopReason: stop})
	m.turnID, m.lastInterrupted, m.cancelled = "", stop == "cancelled", false
}

// item names a started content block; tool calls are remembered until their result.
func (m *mapper) item(msg string, index int, b block) (kind, id string) {
	switch b.Type {
	case "text":
		return "message", partItem(msg, "text", index)
	case "thinking":
		return "reasoning", partItem(msg, "reasoning", index)
	case "tool_use":
		m.toolMsg[b.ID] = msg
		if t := m.tools[b.ID]; b.input() != "" || t.Name == "" {
			m.tools[b.ID] = loomharness.Tool{Name: b.Name, Input: b.input()}
		}
		return "tool", msg + "/tool/" + b.ID
	}
	return "", ""
}

// tool is the started tool call b names, or nil for any other block.
func (m *mapper) tool(b block) *loomharness.Tool {
	if b.Type != "tool_use" {
		return nil
	}
	t := m.tools[b.ID]
	return &t
}

// exited ends the turn state when the process died; events in between are
// lost, so it emits feed.gap, after the cut-off turn's partial usage.
func (m *mapper) exited() []loomharness.Event {
	out := m.flush()
	m.turnID, m.cancelled, m.msgID, m.handed = "", false, "", ""
	m.seq++
	return append(out, loomharness.Event{Type: loomharness.EventFeedGap, Session: m.ref, Seq: m.seq, Time: time.Now()})
}

// flush emits the usage of a turn cut off before its result (no cost: only
// the result reports one) under the turn's own usage id, so the turn has
// one usage row either way; nothing when the turn had no steps.
func (m *mapper) flush() []loomharness.Event {
	if m.usage == (loomharness.Usage{}) {
		return nil
	}
	m.seq++
	e := loomharness.Event{Type: loomharness.EventUsage, Session: m.ref, TurnID: m.turnID, ItemID: m.turnID + "/usage",
		Seq: m.seq, Time: time.Now(), Usage: m.usage}
	m.usage = loomharness.Usage{}
	return []loomharness.Event{e}
}

func partItem(msg, part string, index int) string {
	return msg + "/" + part + "/" + strconv.Itoa(index)
}
