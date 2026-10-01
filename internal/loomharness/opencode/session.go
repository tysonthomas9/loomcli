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
		rules, err := nativeRules(spec.Rules)
		if err != nil {
			return loomharness.NativeRef{}, err
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

// nativeActions maps Loom's permission actions to the actions OpenCode
// b30c4d0 asserts: packages/core/src/tool/plugin/shell.ts asserts "shell";
// edit.ts, write.ts and patch.ts assert "edit"; file-access.ts asserts
// "read" and grep.ts and glob.ts assert "grep" and "glob", which only read.
var nativeActions = map[string][]string{
	"*":    {"*"},
	"read": {"read", "grep", "glob"},
	"edit": {"edit"},
	"bash": {"shell"},
}

// nativeRules renders Loom rules as OpenCode rules, keeping their order
// (both evaluate last match wins). A rule with no OpenCode action fails the
// whole Open: it is never dropped or widened.
func nativeRules(rules []loomharness.PermissionRule) ([]map[string]string, error) {
	var out []map[string]string
	for _, r := range rules {
		actions, ok := nativeActions[r.Action]
		if !ok {
			return nil, &Error{Code: "bad_request", Message: fmt.Sprintf("permission action %q has no OpenCode equivalent; refusing to open the session", r.Action)}
		}
		for _, a := range actions {
			out = append(out, map[string]string{"action": a, "resource": r.Resource, "effect": r.Effect})
		}
	}
	return out, nil
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

// Status reports whether the session has a running execution and whether
// its newest finished turn was interrupted. OpenCode has no turn id outside
// the live feed, so TurnID stays empty.
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
	outcome, err := s.lastOutcome(ctx)
	if err != nil {
		return loomharness.Status{}, err
	}
	return loomharness.Status{Running: ok && st.Type != "idle", LastTurnInterrupt: outcome == "interrupted"}, nil
}

// lastOutcome is the outcome of the newest idle message (a finished turn),
// read newest first; "" when no turn has finished.
func (s *Session) lastOutcome(ctx context.Context) (string, error) {
	q := "order=desc&limit=50"
	for {
		var page messagePage
		if err := s.c.call(ctx, "GET", s.path("/message?"+q), nil, &page); err != nil {
			return "", fmt.Errorf("list messages: %w", err)
		}
		for _, m := range page.Data {
			if m.Type == "idle" {
				return m.Outcome, nil
			}
		}
		if len(page.Data) < 50 || page.Cursor.Next == "" {
			return "", nil
		}
		q = "limit=50&cursor=" + url.QueryEscape(page.Cursor.Next)
	}
}

// Move points the session at dir, which must exist.
func (s *Session) Move(ctx context.Context, dir string) error {
	return s.c.call(ctx, "POST", s.path("/move"), map[string]string{"directory": dir}, nil)
}

// Interrupt stops the running execution; false means nothing was running.
func (s *Session) Interrupt(ctx context.Context) (bool, error) {
	var r struct {
		Interrupted bool `json:"interrupted"`
	}
	err := s.c.call(ctx, "POST", s.path("/interrupt"), nil, &r)
	return r.Interrupted, err
}

// Reply answers a permission ask (per_ id) or a question form (frm_ id). A
// form gets r.Answer in its first field.
func (s *Session) Reply(ctx context.Context, askID string, r loomharness.Reply) error {
	id := url.PathEscape(askID)
	if strings.HasPrefix(askID, "frm_") {
		var form struct {
			Data struct {
				Fields []struct {
					Key string `json:"key"`
				} `json:"fields"`
			} `json:"data"`
		}
		if err := s.c.call(ctx, "GET", s.path("/form/"+id), nil, &form); err != nil {
			return err
		}
		if len(form.Data.Fields) == 0 {
			return &Error{Code: "bad_request", Message: "form " + askID + " has no fields"}
		}
		answer := map[string]string{form.Data.Fields[0].Key: r.Answer}
		return s.c.call(ctx, "POST", s.path("/form/"+id+"/reply"), map[string]any{"answer": answer}, nil)
	}
	body := map[string]string{"decision": "reject"}
	if r.Allow {
		body["decision"] = "once"
	}
	if r.Answer != "" {
		body["message"] = r.Answer
	}
	return s.c.call(ctx, "POST", s.path("/permission/"+id+"/reply"), body, nil)
}

// SetModel sets the session's "provider/model" from the next turn.
func (s *Session) SetModel(ctx context.Context, model string) error {
	provider, id, ok := strings.Cut(model, "/")
	if !ok {
		return &Error{Code: "bad_request", Message: "model " + model + " is not provider/model"}
	}
	return s.c.call(ctx, "POST", s.path("/model"), map[string]any{"model": map[string]string{"providerID": provider, "id": id}}, nil)
}

// Unload is a no-op: OpenCode frees idle session memory only on a server
// restart (design v2 §4.15).
func (s *Session) Unload(context.Context) error { return nil }

// Close stops the session's active turn. It never deletes the native
// session, which stays recorded for Purge (R29).
func (s *Session) Close(ctx context.Context) error {
	_, err := s.Interrupt(ctx)
	return err
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
