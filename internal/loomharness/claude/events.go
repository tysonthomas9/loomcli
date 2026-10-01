package claude

import (
	"encoding/json"
	"strconv"
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
	turnID          string // "" between turns
	msgID           string // the streaming assistant message
	open            int    // the open content block's index
	toolMsg         map[string]string
	pending         map[string]bool // prompted keys not yet delivered
	cancelled       bool            // Loom interrupted the running turn
	lastInterrupted bool
}

func newMapper(ref loomharness.NativeRef) *mapper {
	return &mapper{ref: ref, toolMsg: map[string]string{}, pending: map[string]bool{}}
}

type block struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Text      string `json:"text"`
	Thinking  string `json:"thinking"`
	ToolUseID string `json:"tool_use_id"`
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
	Event            struct {
		Type         string              `json:"type"`
		Index        int                 `json:"index"`
		Message      struct{ ID string } `json:"message"`
		ContentBlock block               `json:"content_block"`
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
		m.turnID = uuid.NewString()
		emit(loomharness.Event{Type: loomharness.EventTurnStarted})
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
		m.result(f.Subtype, emit)
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
			delete(m.toolMsg, b.ToolUseID)
			emit(loomharness.Event{Type: loomharness.EventItemCompleted, ItemKind: "tool", ItemID: msg + "/tool/" + b.ToolUseID})
		}
	}
}

// stream maps a partial-message stream_event.
func (m *mapper) stream(f wireFrame, emit func(loomharness.Event)) {
	ev := f.Event
	switch ev.Type {
	case "message_start":
		m.msgID = ev.Message.ID
	case "content_block_start":
		m.open = ev.Index
		if kind, id := m.item(m.msgID, ev.Index, ev.ContentBlock); id != "" {
			emit(loomharness.Event{Type: loomharness.EventItemStarted, ItemKind: kind, ItemID: id})
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

// assistant maps a whole assistant message block: it completes the open
// text or thinking block, and starts a tool call that was not streamed.
func (m *mapper) assistant(msg string, blocks []block, emit func(loomharness.Event)) {
	for _, b := range blocks {
		switch b.Type {
		case "text":
			emit(loomharness.Event{Type: loomharness.EventItemCompleted, ItemKind: "message", ItemID: partItem(msg, "text", m.open), Text: b.Text})
		case "thinking":
			emit(loomharness.Event{Type: loomharness.EventItemCompleted, ItemKind: "reasoning", ItemID: partItem(msg, "reasoning", m.open), Text: b.Thinking})
		case "tool_use":
			if _, seen := m.toolMsg[b.ID]; !seen {
				kind, id := m.item(msg, m.open, b)
				emit(loomharness.Event{Type: loomharness.EventItemStarted, ItemKind: kind, ItemID: id})
			}
		}
	}
}

// result ends the turn: completed on success, cancelled when Loom
// interrupted it, else failed.
func (m *mapper) result(subtype string, emit func(loomharness.Event)) {
	emit(loomharness.Event{Type: loomharness.EventUsage})
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
		return "tool", msg + "/tool/" + b.ID
	}
	return "", ""
}

// exited ends the turn state when the process died; events in between are
// lost, so it emits feed.gap.
func (m *mapper) exited() loomharness.Event {
	m.turnID, m.cancelled, m.msgID = "", false, ""
	m.seq++
	return loomharness.Event{Type: loomharness.EventFeedGap, Session: m.ref, Seq: m.seq, Time: time.Now()}
}

func partItem(msg, part string, index int) string {
	return msg + "/" + part + "/" + strconv.Itoa(index)
}
