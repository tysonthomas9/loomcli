package codex

import (
	"encoding/json"
	"slices"
	"strconv"
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
		e.Error, e.Failure = turnError(p.Turn.Error), turnFailure(p.Turn)
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
		return usage(e, p), true
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

// usage maps a step's token usage. Last is the step's own; Total is the
// thread's running sum, which only names the step: it grows with every step.
func usage(e loomharness.Event, p protocol.ThreadTokenUsageUpdatedNotification) loomharness.Event {
	l := p.TokenUsage.Last
	e.Type, e.TurnID = loomharness.EventUsage, p.TurnId
	e.ItemID = p.TurnId + "/usage/" + strconv.FormatInt(p.TokenUsage.Total.TotalTokens, 10)
	e.Usage = loomharness.Usage{InputTokens: l.InputTokens - l.CachedInputTokens, OutputTokens: l.OutputTokens,
		CacheReadTokens: l.CachedInputTokens, CacheWriteTokens: l.CacheWriteInputTokens}
	return e
}

// askOpened maps a server request that is an ask: a question when it asks
// for the user's input, else an approval. Text is what it asks about.
func askOpened(e loomharness.Event, m Message) (loomharness.Event, bool) {
	if m.ID == nil || !askMethods[m.Method] {
		return e, false
	}
	var p struct {
		TurnID      string                                  `json:"turnId"`
		ItemID      string                                  `json:"itemId"`
		Command     string                                  `json:"command"`     // commandExecution
		Cwd         string                                  `json:"cwd"`         // commandExecution
		Reason      string                                  `json:"reason"`      // approvals
		GrantRoot   string                                  `json:"grantRoot"`   // fileChange
		Permissions json.RawMessage                         `json:"permissions"` // permissions
		Message     string                                  `json:"message"`     // mcpServer elicitation
		Questions   []protocol.ToolRequestUserInputQuestion `json:"questions"`   // requestUserInput
	}
	_ = json.Unmarshal(m.Params, &p)
	e.Type, e.AskID, e.TurnID, e.ItemID = loomharness.EventAskOpened, askID(m.ID), p.TurnID, p.ItemID
	e.ItemKind = "approval"
	var about []string
	switch m.Method {
	case "item/commandExecution/requestApproval":
		about = append(about, p.Command)
		if p.Cwd != "" {
			about = append(about, "in "+p.Cwd)
		}
	case "item/fileChange/requestApproval":
		if p.GrantRoot != "" {
			about = append(about, "write access to "+p.GrantRoot)
		}
	case "item/permissions/requestApproval":
		about = append(about, string(p.Permissions))
	case "item/tool/requestUserInput":
		e.ItemKind = "question"
		for _, q := range p.Questions {
			lq := loomharness.Question{ID: q.Id, Header: q.Header, Question: q.Question}
			for _, o := range q.Options {
				lq.Options = append(lq.Options, loomharness.Choice{Label: o.Label, Description: o.Description})
			}
			e.Questions = append(e.Questions, lq)
		}
		if len(p.Questions) > 0 {
			about = append(about, p.Questions[0].Question)
		}
	case "mcpServer/elicitation/request":
		e.ItemKind = "question"
		about = append(about, p.Message)
	}
	if p.Reason != "" {
		about = append(about, p.Reason)
	}
	e.Text = strings.Join(slices.DeleteFunc(about, func(s string) bool { return s == "" || s == "null" }), "\n")
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

	// Tool items (toolKinds).
	Status           string          `json:"status"`
	Command          string          `json:"command"`          // commandExecution
	AggregatedOutput string          `json:"aggregatedOutput"` // commandExecution
	ExitCode         *int            `json:"exitCode"`         // commandExecution
	Changes          []fileChange    `json:"changes"`          // fileChange
	Server           string          `json:"server"`           // mcpToolCall
	Tool             string          `json:"tool"`             // mcpToolCall, dynamicToolCall
	Arguments        json.RawMessage `json:"arguments"`        // mcpToolCall, dynamicToolCall
	Result           *struct {
		Content []textPart `json:"content"`
	} `json:"result"` // mcpToolCall
	Error *struct {
		Message string `json:"message"`
	} `json:"error"` // mcpToolCall
	ContentItems []textPart `json:"contentItems"` // dynamicToolCall
	Success      *bool      `json:"success"`      // dynamicToolCall
	Query        string     `json:"query"`        // webSearch
}

type fileChange struct {
	Path string `json:"path"`
	Diff string `json:"diff"`
}

// textPart is a text content part; other part types carry no text.
type textPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func joinText(parts []textPart) string {
	var out []string
	for _, p := range parts {
		if p.Text != "" {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n")
}

// tool is a tool item as the chat shows it. Its name follows the item type;
// a started item carries no output.
func (it item) tool(started bool) *loomharness.Tool {
	enc := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	var t loomharness.Tool
	switch it.Type {
	case "commandExecution":
		t = loomharness.Tool{Name: "command", Input: enc(map[string]string{"command": it.Command}), Output: it.AggregatedOutput,
			Failed: it.Status == "failed" || it.Status == "declined" || (it.ExitCode != nil && *it.ExitCode != 0)}
	case "fileChange":
		paths := make([]string, len(it.Changes))
		diffs := make([]string, len(it.Changes))
		for i, c := range it.Changes {
			paths[i], diffs[i] = c.Path, c.Diff
		}
		t = loomharness.Tool{Name: "edit", Input: enc(map[string][]string{"files": paths}), Output: strings.Join(diffs, "\n"),
			Failed: it.Status == "failed" || it.Status == "declined"}
	case "mcpToolCall":
		t = loomharness.Tool{Name: it.Server + "/" + it.Tool, Input: string(it.Arguments), Failed: it.Error != nil || it.Status == "failed"}
		if it.Result != nil {
			t.Output = joinText(it.Result.Content)
		}
		if it.Error != nil {
			t.Output = it.Error.Message
		}
	case "dynamicToolCall":
		t = loomharness.Tool{Name: it.Tool, Input: string(it.Arguments), Output: joinText(it.ContentItems),
			Failed: (it.Success != nil && !*it.Success) || it.Status == "failed"}
	case "webSearch":
		t = loomharness.Tool{Name: "web_search", Input: enc(map[string]string{"query": it.Query})}
	}
	if t.Input == "null" {
		t.Input = ""
	}
	if started {
		t.Output, t.Failed = "", false
	}
	return &t
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
		e.ItemKind, e.Tool = "tool", it.tool(started)
	case it.Type == "collabAgentToolCall" || it.Type == "subAgentActivity":
		e.Type = loomharness.EventSubagentStarted
		return e, started && isLive
	default:
		return e, false
	}
	return e, true
}

// turnError is a failed turn's reason as text: its message and any details.
func turnError(err *protocol.TurnError) string {
	if err == nil {
		return ""
	}
	if err.AdditionalDetails != nil && *err.AdditionalDetails != "" {
		return err.Message + "\n" + *err.AdditionalDetails
	}
	return err.Message
}

// turnFailure is a failed turn's class, from its error's codexErrorInfo: a code
// string, or an object whose one key is the code. Ported from T3 Code
// apps/server/src/orchestration-v2/Adapters/CodexAdapterV2.ts (the error
// notification's failure class). Copyright (c) 2026 T3 Tools Inc. MIT
// License; see THIRD_PARTY_NOTICES.md.
func turnFailure(t protocol.Turn) *loomharness.Failure {
	err := t.Error
	if t.Status != protocol.TurnStatusFailed || err == nil {
		return nil
	}
	var code string
	if json.Unmarshal(err.CodexErrorInfo, &code) != nil {
		var obj map[string]json.RawMessage
		_ = json.Unmarshal(err.CodexErrorInfo, &obj)
		for k := range obj {
			code = k
		}
	}
	switch code {
	case "usageLimitExceeded", "rateLimitExceeded":
		return &loomharness.Failure{Class: loomharness.FailureUsageLimit, Retryable: true}
	case "unauthorized":
		return &loomharness.Failure{Class: loomharness.FailureAuth}
	case "serverOverloaded", "internalServerError", "httpConnectionFailed", "responseStreamConnectionFailed",
		"responseStreamDisconnected", "responseTooManyFailedAttempts":
		return &loomharness.Failure{Class: loomharness.FailureProvider, Retryable: true}
	}
	return &loomharness.Failure{Class: loomharness.FailureProvider}
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
