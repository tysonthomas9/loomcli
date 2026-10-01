package codex

import (
	"encoding/json"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/codex/protocol"
)

// live maps one live notification or ask from root's app-server to a port
// event (design §5.2). Item ids are codex's own: a paginated thread keeps
// them in its stored history, so Messages gives the same ItemIDs.
func live(root string, m Message) (loomharness.Event, bool) {
	e := loomharness.Event{Session: loomharness.NativeRef{Root: root, NativeID: m.ThreadID}}
	switch m.Method {
	case "turn/started":
		var p protocol.TurnStartedNotification
		if json.Unmarshal(m.Params, &p) != nil {
			return e, false
		}
		e.Type, e.TurnID = loomharness.EventTurnStarted, p.Turn.Id
		return e, true
	case "turn/completed":
		var p protocol.TurnCompletedNotification
		if json.Unmarshal(m.Params, &p) != nil {
			return e, false
		}
		e.Type, e.TurnID, e.StopReason = loomharness.EventTurnCompleted, p.Turn.Id, stopReason(p.Turn.Status)
		return e, p.Turn.Status != protocol.TurnStatusInProgress
	case "item/started", "item/completed":
		var p protocol.ItemStartedNotification // the same shape as ItemCompletedNotification
		if json.Unmarshal(m.Params, &p) != nil {
			return e, false
		}
		e.TurnID = p.TurnId
		return itemEvent(e, p.Item, m.Method == "item/started", true)
	case "item/agentMessage/delta", "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		var p protocol.AgentMessageDeltaNotification // the reasoning deltas add only indexes
		if json.Unmarshal(m.Params, &p) != nil {
			return e, false
		}
		e.Type, e.TurnID, e.ItemID, e.Text = loomharness.EventDelta, p.TurnId, p.ItemId, p.Delta
		e.ItemKind = "message"
		if strings.HasPrefix(m.Method, "item/reasoning/") {
			e.ItemKind = "reasoning"
		}
		return e, true
	case "thread/tokenUsage/updated":
		var p protocol.ThreadTokenUsageUpdatedNotification
		if json.Unmarshal(m.Params, &p) != nil {
			return e, false
		}
		e.Type, e.TurnID = loomharness.EventUsage, p.TurnId
		return e, true
	case "serverRequest/resolved":
		var p protocol.ServerRequestResolvedNotification
		if json.Unmarshal(m.Params, &p) != nil {
			return e, false
		}
		e.Type, e.AskID = loomharness.EventAskResolved, askID(p.RequestId)
		return e, true
	}
	return askOpened(e, m)
}

// askOpened maps a server request that is an ask.
func askOpened(e loomharness.Event, m Message) (loomharness.Event, bool) {
	if m.ID == nil || !askMethods[m.Method] {
		return e, false
	}
	var p struct {
		TurnID string `json:"turnId"`
		ItemID string `json:"itemId"`
	}
	_ = json.Unmarshal(m.Params, &p)
	e.Type, e.AskID, e.TurnID, e.ItemID = loomharness.EventAskOpened, askID(m.ID), p.TurnID, p.ItemID
	return e, true
}

// item is the part of a ThreadItem the events use.
type item struct {
	Type     string          `json:"type"`
	ID       string          `json:"id"`
	ClientID string          `json:"clientId"` // userMessage: the turn/start clientUserMessageId
	Text     string          `json:"text"`     // agentMessage
	Summary  []string        `json:"summary"`  // reasoning
	Content  json.RawMessage `json:"content"`  // userMessage: [{type, text}]
}

// toolKinds are the ThreadItem types Loom shows as tool items.
var toolKinds = map[string]bool{
	"commandExecution": true, "fileChange": true, "mcpToolCall": true, "dynamicToolCall": true, "webSearch": true,
}

// itemEvent maps a ThreadItem. A started item is message.delivered (the
// user's input), item.started or harness.subagent.started; a completed one
// is item.completed. A stored item (live false) is mapped as the live feed
// left it: delivered for an input, else completed.
func itemEvent(e loomharness.Event, raw protocol.ThreadItem, started, isLive bool) (loomharness.Event, bool) {
	var it item
	if json.Unmarshal(raw, &it) != nil {
		return e, false
	}
	e.ItemID = it.ID
	e.Type = loomharness.EventItemCompleted
	if started {
		e.Type = loomharness.EventItemStarted
	}
	switch {
	case it.Type == "userMessage":
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(it.Content, &parts)
		var text []string
		for _, p := range parts {
			if p.Type == "text" {
				text = append(text, p.Text)
			}
		}
		e.Type, e.ItemKind, e.InputKey, e.Text = loomharness.EventMessageDelivered, "message", it.ClientID, strings.Join(text, "\n")
		return e, started || !isLive
	case it.Type == "agentMessage":
		e.ItemKind = "message"
		if !started {
			e.Text = it.Text
		}
	case it.Type == "reasoning":
		e.ItemKind = "reasoning"
		if !started {
			e.Text = strings.Join(it.Summary, "\n")
		}
	case toolKinds[it.Type]:
		e.ItemKind = "tool"
	case it.Type == "collabAgentToolCall" || it.Type == "subAgentActivity":
		e.Type = loomharness.EventSubagentStarted
		return e, started && isLive
	default:
		return e, false
	}
	return e, true
}

func stopReason(s protocol.TurnStatus) string {
	switch s {
	case protocol.TurnStatusCompleted:
		return "completed"
	case protocol.TurnStatusInterrupted:
		return "cancelled"
	}
	return "failed"
}
