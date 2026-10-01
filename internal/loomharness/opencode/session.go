package opencode

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// Open creates the session with the id derived from spec.Key. A repeat with
// the same key returns the existing session.
func (c *Client) Open(ctx context.Context, spec loomharness.OpenSpec) (loomharness.NativeRef, error) {
	ref := loomharness.NativeRef{Root: spec.Launch.Root, NativeID: SessionID(spec.Key)}
	body := map[string]any{
		"id":       ref.NativeID,
		"location": map[string]string{"directory": spec.Dir},
		"metadata": spec.Metadata,
	}
	if spec.Preset.Name != "" {
		body["agent"] = "loom-" + spec.Preset.Name
	}
	if provider, model, ok := strings.Cut(spec.Model, "/"); ok {
		body["model"] = map[string]string{"providerID": provider, "id": model}
	}
	if len(spec.Rules) > 0 {
		rules := make([]map[string]string, len(spec.Rules))
		for i, r := range spec.Rules {
			rules[i] = map[string]string{"action": r.Action, "resource": r.Resource, "effect": r.Effect}
		}
		body["permissions"] = rules
	}
	err := c.call(ctx, "POST", "/api/session", body, nil)
	if isCode(err, "input_id_conflict") {
		err = c.call(ctx, "GET", "/api/session/"+ref.NativeID, nil, nil)
	}
	if err != nil {
		return loomharness.NativeRef{}, err
	}
	return ref, nil
}

// Purge deletes exactly the given recorded sessions; one already gone is fine.
func (c *Client) Purge(ctx context.Context, owned []loomharness.NativeRef) error {
	for _, ref := range owned {
		err := c.call(ctx, "DELETE", "/api/session/"+url.PathEscape(ref.NativeID), nil, nil)
		if err != nil && !errors.Is(err, loomharness.ErrSessionNotFound) {
			return err
		}
	}
	return nil
}

// Session returns the protocol methods for one recorded session.
func (c *Client) Session(ref loomharness.NativeRef) *Session { return &Session{c: c, ref: ref} }

// Session is one OpenCode session. It holds no agent state.
type Session struct {
	c   *Client
	ref loomharness.NativeRef
}

func (s *Session) path(suffix string) string {
	return "/api/session/" + url.PathEscape(s.ref.NativeID) + suffix
}

// Resume checks that the recorded session still exists and returns its ref.
func (s *Session) Resume(ctx context.Context, l loomharness.Launch) (loomharness.NativeRef, error) {
	if err := s.c.call(ctx, "GET", s.path(""), nil, nil); err != nil {
		return loomharness.NativeRef{}, err
	}
	return loomharness.NativeRef{Root: l.Root, NativeID: s.ref.NativeID}, nil
}

// Prompt queues in.Text under the native id in.Key; the first write of an id wins.
func (s *Session) Prompt(ctx context.Context, in loomharness.Input) error {
	return s.c.call(ctx, "POST", s.path("/prompt"), map[string]string{"id": in.Key, "text": in.Text, "delivery": "queue"}, nil)
}

// HasInput looks for the prompt id key in the session's message list.
func (s *Session) HasInput(ctx context.Context, key string) (loomharness.Landed, error) {
	after := ""
	for {
		page, err := s.list(ctx, after, 200)
		if err != nil {
			return loomharness.LandedUnknown, err
		}
		for _, m := range page.Data {
			if m.ID == key {
				return loomharness.LandedFound, nil
			}
		}
		if len(page.Data) < 200 || page.Cursor.Next == "" {
			return loomharness.LandedNotFound, nil
		}
		after = page.Cursor.Next
	}
}

// Messages reads one page of history as events, with OpenCode's own cursor.
func (s *Session) Messages(ctx context.Context, after string, limit int) (loomharness.MessagePage, error) {
	page, err := s.list(ctx, after, limit)
	if err != nil {
		return loomharness.MessagePage{}, err
	}
	var out loomharness.MessagePage
	for _, m := range page.Data {
		out.Events = append(out.Events, m.events(s.ref)...)
	}
	if limit > 0 && len(page.Data) == limit {
		out.Next = page.Cursor.Next
	}
	return out, nil
}

// Status reports whether the session has a running execution.
func (s *Session) Status(ctx context.Context) (loomharness.Status, error) {
	var active struct {
		Data map[string]struct {
			Type string `json:"type"`
		} `json:"data"`
	}
	if err := s.c.call(ctx, "GET", "/api/session/active", nil, &active); err != nil {
		return loomharness.Status{}, err
	}
	st, ok := active.Data[s.ref.NativeID]
	return loomharness.Status{Running: ok && st.Type != "idle"}, nil
}

// Move points the session at dir, which must exist.
func (s *Session) Move(ctx context.Context, dir string) error {
	return s.c.call(ctx, "POST", s.path("/move"), map[string]string{"directory": dir}, nil)
}

type messagePage struct {
	Data   []message `json:"data"`
	Cursor struct {
		Next string `json:"next"`
	} `json:"cursor"`
}

type message struct {
	ID   string `json:"id"`
	Type string `json:"type"` // user | assistant | synthetic | idle
	Text string `json:"text"`
	Time struct {
		Created int64 `json:"created"`
	} `json:"time"`
	Outcome  string `json:"outcome"`
	Metadata struct {
		Notice string `json:"notice"`
	} `json:"metadata"`
	Content []struct {
		Type string `json:"type"` // text | reasoning | tool
		ID   string `json:"id"`
		Text string `json:"text"`
	} `json:"content"`
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
// with the same ItemIDs.
func (m message) events(ref loomharness.NativeRef) []loomharness.Event {
	e := loomharness.Event{Session: ref, ItemID: m.ID, Time: time.UnixMilli(m.Time.Created)}
	switch m.Type {
	case "user":
		e.Type, e.ItemKind, e.InputKey, e.Text = loomharness.EventMessageDelivered, "message", m.ID, m.Text
	case "synthetic":
		if m.Metadata.Notice != "restart" {
			return nil
		}
		e.Type, e.Text = loomharness.EventTurnResumed, m.Text
	case "idle":
		e.Type, e.StopReason = loomharness.EventTurnCompleted, stopReason(m.Outcome)
	case "assistant":
		var out []loomharness.Event
		ord := map[string]int{}
		for _, c := range m.Content {
			item := e
			item.Type, item.Text = loomharness.EventItemCompleted, c.Text
			switch c.Type {
			case "text", "reasoning":
				item.ItemKind = kind(c.Type)
				item.ItemID = partItem(m.ID, c.Type, ord[c.Type])
				ord[c.Type]++
			case "tool":
				item.ItemKind, item.ItemID, item.Text = "tool", toolItem(m.ID, c.ID), ""
			default:
				continue
			}
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
	return []loomharness.Event{e}
}
