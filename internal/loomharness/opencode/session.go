package opencode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
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
		agent := "loom-" + spec.Preset.Name
		if err := c.hasAgent(ctx, agent, spec.Dir); err != nil {
			return loomharness.NativeRef{}, err
		}
		body["agent"] = agent
	}
	if provider, model, ok := strings.Cut(spec.Model, "/"); ok {
		body["model"] = map[string]string{"providerID": provider, "id": model}
	}
	rules, err := nativeRules(spec.Rules)
	if err != nil {
		return loomharness.NativeRef{}, err
	}
	if len(rules) > 0 {
		body["permissions"] = rules
	}
	s := c.Session(ref)
	err = c.call(ctx, "POST", "/api/session", body, nil)
	if isCode(err, "input_id_conflict") {
		err = nil
	}
	// A repeat may find the session holding older rules, and b30c4d0 answers
	// a repeat POST with success and ignores its body, so always install.
	if err == nil {
		err = s.install(ctx, rules)
	}
	if err != nil {
		return loomharness.NativeRef{}, err
	}
	c.remember(ref)
	return ref, s.isolate(ctx)
}

// hasAgent fails closed unless the running service offers agent for dir.
// Loom's presets are loom-<name> agents in <worktrees root>/.opencode/agent,
// which OpenCode finds by walking up from a session's directory
// (core/src/config/discovery.ts:34-48, config/plugin/agent.ts:21-26) on any
// service, Loom-started or not, and reloads when the files change. So dir
// must be under that root, and the service must not disable project config.
// OpenCode accepts an unknown agent id at session create without an error.
// It reads the agent list for dir: b30c4d0's GET /api/agent/:id ignores the
// location and misses project agents (server/src/handlers/agent.ts:15-25). A
// missing agent is retried until agentWait, for a location still loading
// (it lists no agents at all) or a preset file not yet reloaded.
func (c *Client) hasAgent(ctx context.Context, agent, dir string) error {
	if c.presets == "" {
		return &Error{Code: "bad_request", Message: fmt.Sprintf("preset %s: no Loom worktrees root is configured for OpenCode presets", agent)}
	}
	if c.defined != nil && !c.defined(agent) {
		return &Error{Code: "bad_request", Message: fmt.Sprintf("preset %s is not one of Loom's current presets", agent)}
	}
	if rel, err := filepath.Rel(c.presets, filepath.Clean(dir)); err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || !filepath.IsAbs(dir) {
		return &Error{Code: "bad_request", Message: fmt.Sprintf("preset %s: session directory %s is not under the Loom worktrees root %s, where OpenCode finds Loom's presets", agent, dir, c.presets)}
	}
	for deadline := time.Now().Add(agentWait); ; {
		var r struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := c.call(ctx, "GET", "/api/agent?location[directory]="+url.QueryEscape(dir), nil, &r); err != nil {
			return err
		}
		for _, a := range r.Data {
			if a.ID == agent {
				return nil
			}
		}
		if time.Now().After(deadline) {
			if len(r.Data) == 0 {
				return fmt.Errorf("opencode lists no agents for %s after %s: %w", dir, agentWait, loomharness.ErrUnavailable)
			}
			return &Error{Code: "bad_request", Message: fmt.Sprintf("preset %s is not on the running OpenCode service for %s: Loom writes it to %s; the service may disable project config (OPENCODE_DISABLE_PROJECT_CONFIG)",
				agent, dir, filepath.Join(c.presets, ".opencode", "agent", agent+".md"))}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// agentWait bounds hasAgent's wait for a loading location's agents.
var agentWait = 5 * time.Second

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
	out := []map[string]string{}
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

// Resume checks that the recorded session still exists, installs rules as
// its whole policy and returns its ref. OpenCode never starts a turn on its
// own, so nothing runs before the install; it fails closed.
func (s *Session) Resume(ctx context.Context, l loomharness.Launch, rules []loomharness.PermissionRule) (loomharness.NativeRef, error) {
	native, err := nativeRules(rules)
	if err != nil {
		return loomharness.NativeRef{}, err
	}
	var info struct {
		Data struct {
			Agent    string `json:"agent"`
			Location struct {
				Directory string `json:"directory"`
			} `json:"location"`
		} `json:"data"`
	}
	if err := s.c.call(ctx, "GET", s.path(""), nil, &info); err != nil {
		return loomharness.NativeRef{}, err
	}
	if agent := info.Data.Agent; strings.HasPrefix(agent, "loom-") {
		if err := s.c.hasAgent(ctx, agent, info.Data.Location.Directory); err != nil {
			return loomharness.NativeRef{}, err
		}
	}
	if err := s.install(ctx, native); err != nil {
		return loomharness.NativeRef{}, err
	}
	ref := loomharness.NativeRef{Root: l.Root, NativeID: s.ref.NativeID}
	s.c.remember(ref)
	return ref, s.isolate(ctx)
}

// install replaces the session's permission rules with rules (b30c4d0:
// PATCH /api/session/:id, protocol/src/groups/session.ts:358-364). OpenCode
// stores them on the session row before the PATCH returns
// (session/projector.ts:584-590, committed with the event in bus.ts:380-402)
// and re-reads that row at every permission check (permission.ts:158-175),
// so they also survive a server restart.
func (s *Session) install(ctx context.Context, rules []map[string]string) error {
	if err := s.c.call(ctx, "PATCH", s.path(""), map[string]any{"permissions": rules}, nil); err != nil {
		return fmt.Errorf("opencode: install session permissions: %w", err)
	}
	s.c.rulesMu.Lock()
	defer s.c.rulesMu.Unlock()
	s.c.rules[s.ref.NativeID] = rules
	return nil
}

// isolate sets the environment OpenCode gives this session's shell commands,
// which otherwise is the server's own, per-boot password included
// (packages/core/src/shell.ts:259-271 at b30c4d0). OpenCode keeps it in
// memory only, so every prompt sets it again in case the server restarted.
// Callers fail closed: no prompt is sent unless this succeeds.
func (s *Session) isolate(ctx context.Context) error {
	if s.c.shellEnv == nil {
		return nil
	}
	env, err := s.c.shellEnv()
	if err != nil {
		return fmt.Errorf("opencode: set session environment: %w", err)
	}
	vars := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		vars[k] = v
	}
	if err := s.c.call(ctx, "PUT", s.path("/environment"), map[string]any{"variables": vars}, nil); err != nil {
		return fmt.Errorf("opencode: set session environment: %w", err)
	}
	return nil
}

// Prompt queues in.Text under the native id in.Key; the first write of an id
// wins. It first installs the rules Loom last gave Open or Resume and sets
// the session environment again, failing closed: a turn the service resumed
// on its own at boot ran under the stored rules and the service's process
// environment (the accepted 18:00 UTC exception), and this Prompt is where
// Loom's own apply again.
func (s *Session) Prompt(ctx context.Context, in loomharness.Input) error {
	s.c.rulesMu.Lock()
	rules, ok := s.c.rules[s.ref.NativeID]
	s.c.rulesMu.Unlock()
	if !ok {
		// R-H: no prompt runs under rules Loom did not install in this
		// process; Open or Resume installs them first.
		return &Error{Code: "bad_request", Message: fmt.Sprintf("opencode: no permission rules installed for session %s; Open or Resume it first", s.ref.NativeID)}
	}
	if err := s.install(ctx, rules); err != nil {
		return err
	}
	if err := s.isolate(ctx); err != nil {
		return err
	}
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

// Messages reads one page of history as the events the live feed gives for
// it, with the same ids (see mapper): each turn opens with turn.started,
// TurnID is the id of the turn's first message that maps to an event, and
// the last page ends with an ask.opened for each pending permission or form.
//
// OpenCode history does not hold everything the feed does: resolved or
// cancelled asks are gone (the permission and form lists return pending
// ones only), so history never gives ask.resolved or ask.lost; it records
// no execution start, hence the TurnID rule; and a text or reasoning part
// still streaming has no end marker, so history gives its item.completed
// only once a later part exists or its step has ended.
//
// The cursor is OpenCode's own plus the open turn ("c=<native>&t=<turn>");
// a bare OpenCode cursor still works, with the open turn read back.
func (s *Session) Messages(ctx context.Context, after string, limit int) (loomharness.MessagePage, error) {
	native, turn, known := parseCursor(after)
	page, err := s.list(ctx, native, limit)
	if err != nil {
		return loomharness.MessagePage{}, err
	}
	ref := loomharness.NativeRef{Root: s.c.rootOf(s.ref.NativeID), NativeID: s.ref.NativeID}
	var out loomharness.MessagePage
	for _, m := range page.Data {
		if !m.opens() {
			continue
		}
		if !known {
			known = true
			id, _, found, err := s.turnOf(ctx, m.ID)
			if err != nil {
				return loomharness.MessagePage{}, err
			}
			if found {
				turn = id
			}
		}
		if turn == "" {
			turn = m.ID
			start := loomharness.Event{Type: loomharness.EventTurnStarted, Session: ref, TurnID: turn, Time: m.created()}
			if m.Type == "user" {
				start.InputKey = m.ID
			}
			out.Events = append(out.Events, start)
		}
		for _, e := range m.events(ref) {
			e.TurnID = turn
			out.Events = append(out.Events, e)
		}
		if m.Type == "idle" {
			turn = ""
		}
	}
	if limit > 0 && len(page.Data) == limit && page.Cursor.Next != "" {
		out.Next = url.Values{"c": {page.Cursor.Next}, "t": {turn}}.Encode()
		return out, nil
	}
	asks, err := s.pendingAsks(ctx)
	if err != nil {
		return loomharness.MessagePage{}, err
	}
	for _, id := range asks {
		out.Events = append(out.Events, loomharness.Event{Type: loomharness.EventAskOpened, Session: ref, AskID: id, TurnID: turn})
	}
	return out, nil
}

// parseCursor splits a Messages cursor into OpenCode's cursor and the open
// turn; known is false for a bare OpenCode cursor.
func parseCursor(after string) (native, turn string, known bool) {
	if q, err := url.ParseQuery(after); err == nil && q.Has("c") {
		return q.Get("c"), q.Get("t"), true
	}
	return after, "", after == ""
}

// pendingAsks lists the session's pending permission and form ids.
func (s *Session) pendingAsks(ctx context.Context) ([]string, error) {
	var ids []string
	for _, kind := range []string{"/permission", "/form"} {
		var r struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := s.c.call(ctx, "GET", s.path(kind), nil, &r); err != nil {
			return nil, fmt.Errorf("list pending asks: %w", err)
		}
		for _, a := range r.Data {
			ids = append(ids, a.ID)
		}
	}
	return ids, nil
}

// turnOf finds the turn holding stored message anchor: the first message
// that maps to an event after the newest idle marker before anchor, and its
// InputKey when that message is a user input. found is false when anchor is
// itself the turn's first such message, or is not stored.
func (s *Session) turnOf(ctx context.Context, anchor string) (id, key string, found bool, err error) {
	c, err := json.Marshal(map[string]string{"id": anchor, "order": "desc", "direction": "next"})
	if err != nil {
		return "", "", false, err
	}
	cursor := base64.RawURLEncoding.EncodeToString(c)
	for {
		page, err := s.list(ctx, cursor, 100)
		if err != nil {
			return "", "", false, err
		}
		for _, m := range page.Data {
			if m.Type == "idle" {
				return id, key, found, nil
			}
			if m.opens() {
				id, key, found = m.ID, "", true
				if m.Type == "user" {
					key = m.ID
				}
			}
		}
		if len(page.Data) < 100 || page.Cursor.Next == "" {
			return id, key, found, nil
		}
		cursor = page.Cursor.Next
	}
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
	Type string `json:"type"` // user | assistant | synthetic | idle | ...
	Text string `json:"text"`
	Time struct {
		Created   json.RawMessage `json:"created"`
		Completed json.RawMessage `json:"completed"`
	} `json:"time"`
	Outcome  string          `json:"outcome"`
	Finish   string          `json:"finish"` // assistant: set when its step ended
	Error    json.RawMessage `json:"error"`  // assistant: set when its step failed
	Metadata struct {
		Notice string `json:"notice"`
	} `json:"metadata"`
	Content []struct {
		Type  string `json:"type"` // text | reasoning | tool
		ID    string `json:"id"`
		Text  string `json:"text"`
		State struct {
			Status string `json:"status"` // tool: streaming | running | completed | error
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
				item.ItemKind, item.ItemID, item.Text = "tool", toolItem(m.ID, c.ID), ""
			default:
				continue
			}
			out = append(out, item)
		}
		if m.usage() {
			u := e
			u.Type, u.ItemID = loomharness.EventUsage, m.ID
			out = append(out, u)
		}
		return out
	default:
		return nil
	}
	return []loomharness.Event{e}
}
