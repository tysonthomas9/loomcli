package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

type messagePage struct {
	Data   []message `json:"data"`
	Cursor struct {
		Next string `json:"next"`
	} `json:"cursor"`
}

type message struct {
	ID   string `json:"id"`
	Type string `json:"type"` // user | assistant | synthetic | idle | ...
	Text string `json:"text"`
	Time struct {
		Created   json.RawMessage `json:"created"`
		Completed json.RawMessage `json:"completed"`
	} `json:"time"`
	Outcome  string          `json:"outcome"`
	Finish   string          `json:"finish"` // assistant: set when its step ended
	Error    json.RawMessage `json:"error"`  // assistant: set when its step failed
	Cost     float64         `json:"cost"`   // assistant: its step's cost
	Tokens   tokens          `json:"tokens"` // assistant: its step's tokens
	Metadata struct {
		Notice string `json:"notice"`
	} `json:"metadata"`
	Content []struct {
		Type  string `json:"type"` // text | reasoning | tool
		ID    string `json:"id"`
		Text  string `json:"text"`
		Name  string `json:"name"` // tool
		State struct {
			Status  string          `json:"status"` // tool: streaming | running | completed | error
			Input   json.RawMessage `json:"input"`
			Content toolContent     `json:"content"`
			Error   *toolError      `json:"error"`
		} `json:"state"`
	} `json:"content"`
}

// created reads the message's creation time, stored as epoch ms or an ISO
// string depending on the message type.
func (m message) created() time.Time {
	var ms int64
	if json.Unmarshal(m.Time.Created, &ms) == nil {
		return time.UnixMilli(ms)
	}
	var t time.Time
	_ = json.Unmarshal(m.Time.Created, &t)
	return t
}

// opens reports whether the live feed maps an event for this message, so
// that it can be the first message of a turn (see mapper).
func (m message) opens() bool {
	switch m.Type {
	case "user", "idle":
		return true
	case "synthetic":
		return m.Metadata.Notice == "restart"
	case "assistant":
		return len(m.Content) > 0 || m.usage()
	}
	return false
}

// declinedCall is the error OpenCode b30c4d0 gives a tool call whose
// permission was rejected (core/src/session/runner/step.ts).
const declinedCall = "The user declined this tool call"

// declined: the step's tool call was declined, which ends the turn with no
// idle marker (see mapper).
func (m message) declined() bool {
	for _, c := range m.Content {
		if c.Type == "tool" && c.State.Error != nil && c.State.Error.Message == declinedCall {
			return true
		}
	}
	return false
}

// usage: the step ended (session.step.ended) rather than failed.
func (m message) usage() bool {
	return m.Finish != "" && (len(m.Error) == 0 || string(m.Error) == "null")
}

func (m message) ended() bool {
	return m.Finish != "" || (len(m.Time.Completed) > 0 && string(m.Time.Completed) != "null")
}

func (s *Session) list(ctx context.Context, after string, limit int) (messagePage, error) {
	q := url.Values{}
	if after != "" {
		q.Set("cursor", after)
	} else {
		q.Set("order", "asc")
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var page messagePage
	if err := s.c.call(ctx, "GET", s.path("/message?"+q.Encode()), nil, &page); err != nil {
		return messagePage{}, fmt.Errorf("list messages: %w", err)
	}
	return page, nil
}

// events maps one stored message to the events the live feed gives for it,
// with the same ids; Messages adds the TurnID.
func (m message) events(ref loomharness.NativeRef) []loomharness.Event {
	e := loomharness.Event{Session: ref, Time: m.created()}
	switch m.Type {
	case "user":
		e.Type, e.ItemKind, e.ItemID, e.InputKey, e.Text = loomharness.EventMessageDelivered, "message", m.ID, m.ID, m.Text
	case "synthetic":
		if m.Metadata.Notice != "restart" {
			return nil
		}
		e.Type, e.ItemID, e.Text = loomharness.EventTurnResumed, m.ID, m.Text
	case "idle":
		e.Type, e.StopReason = loomharness.EventTurnCompleted, stopReason(m.Outcome)
	case "assistant":
		var out []loomharness.Event
		ord := map[string]int{}
		for i, c := range m.Content {
			item := e
			item.Type, item.Text = loomharness.EventItemCompleted, c.Text
			switch c.Type {
			case "text", "reasoning":
				item.ItemKind = kind(c.Type)
				item.ItemID = partItem(m.ID, c.Type, ord[c.Type])
				ord[c.Type]++
				if !m.ended() && i == len(m.Content)-1 {
					continue // still streaming
				}
			case "tool":
				if c.State.Status != "completed" && c.State.Status != "error" {
					continue
				}
				out, failed := toolOutput(c.State.Content, c.State.Error)
				item.ItemKind, item.ItemID, item.Text = "tool", toolItem(m.ID, c.ID), ""
				item.Tool = &loomharness.Tool{Name: c.Name, Input: toolInput(c.State.Input), Output: out, Failed: failed}
			default:
				continue
			}
			out = append(out, item)
		}
		if m.usage() {
			u := e
			u.Type, u.ItemID, u.Usage = loomharness.EventUsage, m.ID, m.Tokens.usage(m.Cost)
			out = append(out, u)
		}
		return out
	default:
		return nil
	}
	return []loomharness.Event{e}
}
