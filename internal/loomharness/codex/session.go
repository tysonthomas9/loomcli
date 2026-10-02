package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/codex/protocol"
)

// Session returns the protocol methods for one recorded thread.
func (a *Adapter) Session(ref loomharness.NativeRef) loomharness.Session { return a.session(ref) }

func (a *Adapter) session(ref loomharness.NativeRef) *Session { return &Session{a: a, ref: ref} }

var _ loomharness.Harness = (*Adapter)(nil)

// Session is one codex thread on its recorded root's app-server. It holds
// no agent state: the running turn is read from codex.
type Session struct {
	a   *Adapter
	ref loomharness.NativeRef
}

// call runs method on the recorded root's app-server; it never falls back
// to another root. A thread codex cannot read is ErrSessionNotFound.
func (s *Session) call(ctx context.Context, method string, params, result any) error {
	if s.ref.Root == "" {
		return fmt.Errorf("codex: thread %s has no recorded root", s.ref.NativeID)
	}
	conn, err := s.a.Conn(ctx, s.ref.Root)
	if err != nil {
		return err
	}
	err = conn.Call(ctx, method, params, result)
	var rpc *RPCError
	if errors.As(err, &rpc) && strings.HasPrefix(rpc.Message, "thread not loaded") {
		return fmt.Errorf("codex %s %s: %w: %w", method, s.ref.NativeID, loomharness.ErrSessionNotFound, err)
	}
	return err
}

// Prompt starts a turn with in.Text, keyed in.Key (codex keeps it as the
// user item's clientId). codex would steer an active turn with it, so a
// running thread is ErrBusy.
func (s *Session) Prompt(ctx context.Context, in loomharness.Input) error {
	running, err := s.running(ctx)
	if err != nil {
		return err
	}
	if running {
		return fmt.Errorf("codex thread %s: %w", s.ref.NativeID, loomharness.ErrBusy)
	}
	text, _ := json.Marshal(map[string]string{"type": "text", "text": in.Text})
	params := protocol.TurnStartParams{ThreadId: s.ref.NativeID, Input: []protocol.UserInput{text}, ClientUserMessageId: &in.Key}
	s.a.mu.Lock()
	o := s.a.next[s.ref]
	s.a.mu.Unlock()
	if o.model != "" {
		params.Model = &o.model
	}
	if o.dir != "" {
		params.Cwd = &o.dir
	}
	if err := s.call(ctx, "turn/start", params, nil); err != nil {
		return err
	}
	s.a.mu.Lock()
	if s.a.next[s.ref] == o { // codex keeps both for later turns
		delete(s.a.next, s.ref)
	}
	s.a.mu.Unlock()
	return nil
}

// Interrupt stops the running turn; false means none was running.
func (s *Session) Interrupt(ctx context.Context) (bool, error) {
	st, err := s.Status(ctx)
	if err != nil || st.TurnID == "" {
		return false, err
	}
	return true, s.call(ctx, "turn/interrupt", protocol.TurnInterruptParams{ThreadId: s.ref.NativeID, TurnId: st.TurnID}, nil)
}

// Status reports the thread's run state, its in-progress turn and whether
// its newest finished turn was interrupted.
func (s *Session) Status(ctx context.Context) (loomharness.Status, error) {
	running, err := s.running(ctx)
	if err != nil {
		return loomharness.Status{}, err
	}
	st := loomharness.Status{Running: running}
	limit := int64(2) // an in-progress turn, then the newest finished one
	page, err := s.turns(ctx, protocol.ThreadTurnsListParams{Limit: &limit, SortDirection: protocol.SortDirectionDesc, ItemsView: json.RawMessage(`"notLoaded"`)})
	if err != nil {
		return loomharness.Status{}, err
	}
	for _, t := range page.Data {
		if t.Status == protocol.TurnStatusInProgress {
			st.TurnID = t.Id
			continue
		}
		st.LastTurnInterrupt = t.Status == protocol.TurnStatusInterrupted
		break
	}
	return st, nil
}

func (s *Session) running(ctx context.Context) (bool, error) {
	var r protocol.ThreadReadResponse
	if err := s.call(ctx, "thread/read", protocol.ThreadReadParams{ThreadId: s.ref.NativeID}, &r); err != nil {
		return false, err
	}
	var status struct {
		Type string `json:"type"` // notLoaded | idle | active | systemError
	}
	_ = json.Unmarshal(r.Thread.Status, &status)
	return status.Type == "active", nil
}

// HasInput looks for an input keyed key among the thread's user items.
func (s *Session) HasInput(ctx context.Context, key string) (loomharness.Landed, error) {
	params := history("")
	for {
		page, err := s.turns(ctx, params)
		if err != nil {
			return loomharness.LandedUnknown, err
		}
		for _, t := range page.Data {
			for _, raw := range t.Items {
				var it item
				if json.Unmarshal(raw, &it) == nil && it.Type == "userMessage" && it.ClientID == key {
					return loomharness.LandedFound, nil
				}
			}
		}
		if page.NextCursor == nil || len(page.Data) == 0 {
			return loomharness.LandedNotFound, nil
		}
		params.Cursor = page.NextCursor
	}
}

// Messages reads one page of history, oldest first, as the events the live
// feed gave for it, with the same ItemIDs. limit counts turns.
func (s *Session) Messages(ctx context.Context, after string, limit int) (loomharness.MessagePage, error) {
	params := history(after)
	if limit > 0 {
		n := int64(limit)
		params.Limit = &n
	}
	page, err := s.turns(ctx, params)
	if err != nil {
		return loomharness.MessagePage{}, err
	}
	var out loomharness.MessagePage
	for _, t := range page.Data {
		e := loomharness.Event{Session: s.ref, TurnID: t.Id}
		if t.StartedAt != nil {
			e.Time = time.Unix(*t.StartedAt, 0)
		}
		for _, raw := range t.Items {
			if ie, ok := itemEvent(e, raw, false, false); ok {
				out.Events = append(out.Events, ie)
			}
		}
		if t.Status != protocol.TurnStatusInProgress {
			done := e
			if t.CompletedAt != nil {
				done.Time = time.Unix(*t.CompletedAt, 0)
			}
			done.Type, done.StopReason = loomharness.EventTurnCompleted, stopReason(t.Status)
			out.Events = append(out.Events, done)
		}
	}
	if page.NextCursor != nil && len(page.Data) > 0 {
		out.Next = *page.NextCursor
	}
	return out, nil
}

// history reads every stored item, oldest first.
func history(after string) protocol.ThreadTurnsListParams {
	p := protocol.ThreadTurnsListParams{SortDirection: protocol.SortDirectionAsc, ItemsView: json.RawMessage(`"full"`)}
	if after != "" {
		p.Cursor = &after
	}
	return p
}

// turns lists the thread's turns. A thread with no user message yet has no
// rollout file and no turns; codex 0.157.1 refuses thread/turns/list for it
// with "is not materialized yet", or, once it is named, "missing source
// rollout" (probed; the thread's path names a file that does not exist).
func (s *Session) turns(ctx context.Context, p protocol.ThreadTurnsListParams) (protocol.ThreadTurnsListResponse, error) {
	p.ThreadId = s.ref.NativeID
	var r protocol.ThreadTurnsListResponse
	err := s.call(ctx, "thread/turns/list", p, &r)
	var rpc *RPCError
	if errors.As(err, &rpc) && (strings.Contains(rpc.Message, "is not materialized yet") || strings.HasSuffix(rpc.Message, ": missing source rollout")) {
		return protocol.ThreadTurnsListResponse{}, nil
	}
	return r, err
}

// Unload frees what Loom holds for an idle thread: the record that it opened
// it, once the thread has a first message and Open can find it by name
// again (a thread with none keeps it, so Open stays idempotent). The thread
// lives in its root's shared app-server, where thread/unsubscribe left it
// loaded and did not lower memory, so Restart frees that (design §4.15).
// Unload never touches a running turn, a waiting message or an open ask.
func (s *Session) Unload(ctx context.Context) error {
	one := int64(1)
	page, err := s.turns(ctx, protocol.ThreadTurnsListParams{Limit: &one, ItemsView: json.RawMessage(`"notLoaded"`)})
	if err == nil && len(page.Data) > 0 {
		s.a.forget(s.ref)
	}
	return err
}

// Close frees what Unload frees and stops nothing else: the app-server is
// shared, and the thread and its history are kept.
func (s *Session) Close(ctx context.Context) error { return s.Unload(ctx) }

// Resume (4.2b) must install the rules first; until then it fails and
// nothing runs.
func (s *Session) Resume(context.Context, loomharness.Launch, []loomharness.PermissionRule) (loomharness.NativeRef, error) {
	return loomharness.NativeRef{}, fmt.Errorf("codex: Resume is not available until 4.2b installs rules first: %w", loomharness.ErrUnavailable)
}

// SetModel takes effect from the next turn: the next Prompt's turn/start
// sets it, and codex keeps it for later turns.
func (s *Session) SetModel(_ context.Context, model string) error {
	s.a.mu.Lock()
	defer s.a.mu.Unlock()
	o := s.a.next[s.ref]
	o.model = model
	s.a.next[s.ref] = o
	return nil
}

// Move takes effect from the next turn, as SetModel does, with dir as the
// thread's cwd. The NativeRef is unchanged.
func (s *Session) Move(_ context.Context, dir string) error {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("codex: move to %s: not a directory", dir)
	}
	s.a.mu.Lock()
	defer s.a.mu.Unlock()
	o := s.a.next[s.ref]
	o.dir = dir
	s.a.next[s.ref] = o
	return nil
}

// Reply answers an open ask on this thread with codex's own decision. An
// approval allowed Always is codex's session-wide grant: acceptForSession
// (codex's session approval cache) or a permissions grant scoped "session";
// Always on a command ask that does not offer acceptForSession fails with
// ErrUnsupported and leaves the ask open.
// A question gets r.Answer: request_user_input as its first question's
// answer, an MCP form elicitation as its first required field. With no
// answer and no Allow, an elicitation is declined.
func (s *Session) Reply(_ context.Context, askID string, r loomharness.Reply) error {
	s.a.mu.Lock()
	ask, ok := s.a.asks[s.ref.Root][askID]
	s.a.mu.Unlock()
	if !ok || ask.ThreadID != s.ref.NativeID {
		return fmt.Errorf("codex: ask %s is not open on thread %s", askID, s.ref.NativeID)
	}
	result, err := answer(ask, r)
	if err != nil {
		return err
	}
	s.a.mu.Lock()
	_, ok = s.a.asks[s.ref.Root][askID]
	delete(s.a.asks[s.ref.Root], askID) // answered once; serverRequest/resolved follows
	s.a.mu.Unlock()
	if !ok {
		return fmt.Errorf("codex: ask %s was answered or lost meanwhile", askID)
	}
	return ask.Respond(result)
}

// answer is the response payload for ask's method.
func answer(ask Message, r loomharness.Reply) (any, error) {
	decision := "decline"
	switch {
	case r.Allow && r.Always:
		decision = "acceptForSession"
	case r.Allow:
		decision = "accept"
	}
	switch ask.Method {
	case "item/commandExecution/requestApproval":
		d, err := offered(ask, decision)
		return protocol.CommandExecutionRequestApprovalResponse{Decision: quote(d)}, err
	case "item/fileChange/requestApproval":
		return protocol.FileChangeRequestApprovalResponse{Decision: quote(decision)}, nil
	case "item/permissions/requestApproval":
		var p protocol.PermissionsRequestApprovalParams
		if err := json.Unmarshal(ask.Params, &p); err != nil {
			return nil, fmt.Errorf("codex: permissions ask %s: %w", askID(ask.ID), err)
		}
		grant := protocol.PermissionsRequestApprovalResponse{Scope: protocol.PermissionGrantScopeTurn} // nothing granted denies
		if r.Allow {
			grant.Permissions = protocol.GrantedPermissionProfile(p.Permissions)
		}
		if r.Allow && r.Always {
			grant.Scope = protocol.PermissionGrantScopeSession
		}
		return grant, nil
	case "item/tool/requestUserInput":
		var p protocol.ToolRequestUserInputParams
		if err := json.Unmarshal(ask.Params, &p); err != nil || len(p.Questions) == 0 {
			return nil, fmt.Errorf("codex: question %s has no questions (%v)", askID(ask.ID), err)
		}
		out := protocol.ToolRequestUserInputResponse{Answers: map[string]protocol.ToolRequestUserInputAnswer{}}
		if r.Answer != "" {
			out.Answers[p.Questions[0].Id] = protocol.ToolRequestUserInputAnswer{Answers: []string{r.Answer}}
		}
		return out, nil
	case "mcpServer/elicitation/request":
		return elicitation(ask, r)
	}
	return nil, fmt.Errorf("codex: ask %s is a %s, which Loom cannot answer", askID(ask.ID), ask.Method)
}

// elicitation answers an MCP elicitation: a form gets r.Answer in its first
// required field, a URL is accepted as opened; a device-proof request can
// only be declined.
func elicitation(ask Message, r loomharness.Reply) (protocol.McpServerElicitationRequestResponse, error) {
	if !r.Allow && r.Answer == "" {
		return protocol.McpServerElicitationRequestResponse{Action: protocol.McpServerElicitationActionDecline}, nil
	}
	var p struct {
		Mode            string `json:"mode"`
		RequestedSchema struct {
			Required []string `json:"required"`
		} `json:"requestedSchema"`
	}
	_ = json.Unmarshal(ask.Params, &p)
	accept := protocol.McpServerElicitationRequestResponse{Action: protocol.McpServerElicitationActionAccept}
	switch {
	case p.Mode == "url":
		return accept, nil
	case strings.HasSuffix(strings.ToLower(p.Mode), "form") && len(p.RequestedSchema.Required) > 0:
		accept.Content, _ = json.Marshal(map[string]string{p.RequestedSchema.Required[0]: r.Answer})
		return accept, nil
	}
	return accept, fmt.Errorf("codex: elicitation %s (mode %q) cannot be answered with text", askID(ask.ID), p.Mode)
}

// offered checks decision against what the command ask lists in its
// availableDecisions (all, when it lists none). A session grant codex does
// not offer is ErrUnsupported, never narrowed; a decline it does not offer
// becomes cancel.
func offered(ask Message, decision string) (string, error) {
	var p struct {
		AvailableDecisions []json.RawMessage `json:"availableDecisions"`
	}
	_ = json.Unmarshal(ask.Params, &p)
	has := func(d string) bool {
		return p.AvailableDecisions == nil || slices.ContainsFunc(p.AvailableDecisions, func(r json.RawMessage) bool { return string(r) == string(quote(d)) })
	}
	switch {
	case has(decision):
		return decision, nil
	case decision == "acceptForSession":
		return "", fmt.Errorf("codex: command ask %s does not offer an always-allow (acceptForSession): %w", askID(ask.ID), errors.ErrUnsupported)
	case decision == "decline" && has("cancel"): // stricter, never wider: the action still does not run, and the turn stops too
		return "cancel", nil
	}
	return decision, nil
}

func quote(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}
