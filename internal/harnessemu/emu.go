// Package harnessemu is Loom's test-support harness emulator (design v2
// §8.1.10, R28). Server plays the OpenCode b30c4d0 HTTP surface that
// internal/loomharness/opencode drives: sessions, prompts, streamed text and
// reasoning, usage, failures, interrupt, message paging, HasInput and the
// child-session list. It is never linked into loom serve; only
// cmd/loom-harness-emu runs it.
//
// Behavior comes only from a test-owned scenario file: a JSON map from a
// native session id or an AgentID (the session's metadata agent_id) to the
// turns that key's prompts play, in order. User text never selects a
// scenario. A prompt with no scripted turn left asks Model, 2.0's scripted
// fake model, when set, and otherwise echoes its text. Like OpenCode's Code
// Mode, a model call to the execute tool with code `tools.<server>.<tool>(args)`
// runs that tool on the location's registered MCP server (the Loom bridge).
//
// Modeled, not proven: real OpenCode keeps sessions in its database. The
// emulator saves its state to one JSON file, so a restart keeps sessions,
// and a turn that was running at a stop resumes on the next boot after a
// synthetic restart notice, as OpenCode's boot sweep does.
package harnessemu

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// Version is what the emulator reports: the pinned OpenCode build.
const Version = "2.0.19"

// RestartNotice is the text of OpenCode's boot-sweep synthetic message.
const RestartNotice = "The server restarted while you were working"

// Turn is one scripted turn.
type Turn struct {
	Reasoning string  `json:"reasoning,omitempty"`
	Text      string  `json:"text,omitempty"`     // "" echoes the prompt
	Child     bool    `json:"child,omitempty"`    // start a child session first
	Hold      bool    `json:"hold,omitempty"`     // run until interrupted
	Fail      string  `json:"fail,omitempty"`     // fail the step and the execution
	Tokens    Tokens  `json:"tokens"`             // the step's usage
	Cost      float64 `json:"cost,omitempty"`     // the step's cost
	DelayMS   int     `json:"delay_ms,omitempty"` // pause before each streamed delta
	Tools     []Tool  `json:"tools,omitempty"`    // tool calls the turn ran first
	Ask       bool    `json:"ask,omitempty"`      // ask Model when the turn plays
	// Suspend ends the execution after the text as OpenCode b30c4d0 ends a
	// declined one (Server.suspend), with no reject, so a feed sees no end.
	Suspend bool `json:"suspend,omitempty"`
}

// Tool is one tool call a turn ran: its name and input, and its output, or
// with Fail its error. With Permission, the call first asks that permission
// (OpenCode's Permission.Request fields: action, resources, save, metadata)
// and waits for its reply; a reject ends the turn (Server.decline). With Questions, it plays
// OpenCode's question tool: it asks them as a form and waits, and its output
// is the answers, one list per question.
type Tool struct {
	ID         string         `json:"id"`
	Name       string         `json:"name,omitempty"` // "" plays execute, Code Mode's tool
	Input      map[string]any `json:"input,omitempty"`
	Output     string         `json:"output"`
	Fail       string         `json:"fail,omitempty"`
	Permission map[string]any `json:"permission,omitempty"`
	Questions  []Question     `json:"questions,omitempty"`
}

// Tokens is OpenCode's per-step token usage.
type Tokens struct {
	Input     int64 `json:"input"`
	Output    int64 `json:"output"`
	Reasoning int64 `json:"reasoning"`
	Cache     struct {
		Read  int64 `json:"read"`
		Write int64 `json:"write"`
	} `json:"cache"`
}

// Prompt is one captured prompt.
type Prompt struct {
	Session string `json:"session"`
	Agent   string `json:"agent"`
	ID      string `json:"id"`
	Text    string `json:"text"`
}

type session struct {
	Info     map[string]any   `json:"info"`
	Messages []map[string]any `json:"messages"`
	Seq      int64            `json:"seq"`
	Played   int              `json:"played"`            // scripted turns used
	Running  *run             `json:"running,omitempty"` // the running turn
	Asks     map[string]any   `json:"asks,omitempty"`    // pending per_ and frm_ asks by id, as listed

	Instructions map[string]string `json:"instructions,omitempty"` // instruction entries by name (instructions.go)
}

// agent is the session's metadata agent_id, or "".
func (ss *session) agent() string {
	md, _ := ss.Info["metadata"].(map[string]any)
	a, _ := md["agent_id"].(string)
	return a
}

type run struct {
	Turn Turn          `json:"turn"`
	stop chan struct{} // closed when the turn stops early
}

type state struct {
	Sessions map[string]*session `json:"sessions"`
	Next     int64               `json:"next"`
	Prompts  []Prompt            `json:"prompts"` // every new prompt, in order
}

// Server is one emulated OpenCode service.
type Server struct {
	Password  string
	Model     string // the fake model's OpenAI base URL; "" echoes
	path      string // state file
	scenarios string // scenario file
	mu        sync.Mutex
	st        state
	subs      map[chan []byte]bool
	quit      chan struct{}
	mcp       map[string]map[string]map[string]any // runtime MCP servers: directory, name, config
	replies   map[string]chan any                  // a pending ask's reply, by ask id
}

// New loads the state file (absent is empty) and resumes turns a stop left running.
func New(statePath, scenarios, password string) (*Server, error) {
	s := &Server{Password: password, path: statePath, scenarios: scenarios, subs: map[chan []byte]bool{}, quit: make(chan struct{}), mcp: map[string]map[string]map[string]any{}, replies: map[string]chan any{},
		st: state{Sessions: map[string]*session{}}}
	if b, err := os.ReadFile(statePath); err == nil { //nolint:gosec // G304: the test-owned state file.
		if err := json.Unmarshal(b, &s.st); err != nil {
			return nil, fmt.Errorf("harnessemu state %s: %w", statePath, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ss := range s.st.Sessions {
		ss.Asks = nil // a resumed turn asks again
		if ss.Running != nil {
			ss.Running.stop = make(chan struct{})
			s.add(id, map[string]any{"type": "synthetic", "text": RestartNotice, "metadata": map[string]string{"notice": "restart"}})
			go s.play(id, ss.Running)
		}
	}
	return s, nil
}

// Close stops running turns without ending them, as a graceful OpenCode
// stop keeps their claims; the next New resumes them.
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.quit:
	default:
		close(s.quit)
		s.save()
	}
}

func (s *Server) newID() string { s.st.Next++; return fmt.Sprintf("%012d", s.st.Next) }

// emit sends one durable session event to every /api/event stream and saves.
func (s *Server) emit(sid, typ string, data map[string]any) string {
	ss := s.st.Sessions[sid]
	ss.Seq++
	id := "evt_" + s.newID()
	data["sessionID"] = sid
	b, _ := json.Marshal(map[string]any{"id": id, "type": typ, "created": time.Now().UnixMilli(),
		"durable": map[string]any{"aggregateID": sid, "seq": ss.Seq}, "data": data})
	for ch := range s.subs {
		select {
		case ch <- b:
		default: // a stalled reader loses events, as a dropped stream does
		}
	}
	s.save()
	return id
}

// add stores message m and emits the event OpenCode gives for it.
func (s *Server) add(sid string, m map[string]any) {
	m["time"] = map[string]int64{"created": time.Now().UnixMilli()}
	s.st.Sessions[sid].Messages = append(s.st.Sessions[sid].Messages, m)
	switch m["type"] {
	case "user":
		s.emit(sid, "session.inbox.delivered", map[string]any{"inboxID": m["id"]})
	case "synthetic":
		// The synthetic message's id derives from its event's id.
		id := s.emit(sid, "session.synthetic", map[string]any{"text": m["text"], "metadata": m["metadata"]})
		m["id"] = "msg_" + strings.TrimPrefix(id, "evt_")
		s.save()
	}
}

func (s *Server) save() {
	if s.path == "" {
		return
	}
	b, _ := json.Marshal(s.st)
	if os.WriteFile(s.path+".tmp", b, 0o600) == nil {
		_ = os.Rename(s.path+".tmp", s.path)
	}
}

// FailSessionCreate is the suffix of a test-owned flag file beside the
// scenario file: while it exists, creating a session answers 503, so a
// Create's start fails as a retryable harness error (S3 C2).
const FailSessionCreate = ".fail-session-create"

// next is the session's next scripted turn, or an echo of text.
func (s *Server) next(ss *session, text string) Turn {
	var m map[string][]Turn
	if b, err := os.ReadFile(s.scenarios); err == nil { //nolint:gosec // G304: the test-owned scenario file.
		_ = json.Unmarshal(b, &m)
	}
	turns := m[ss.Info["id"].(string)]
	if turns == nil {
		turns = m[ss.agent()]
	}
	if ss.Played < len(turns) {
		ss.Played++
		return turns[ss.Played-1]
	}
	if s.Model != "" {
		return Turn{Ask: true}
	}
	return Turn{Text: text}
}

var modelClient = &http.Client{Timeout: 30 * time.Second}

// ask sends the session's user texts and earlier replies to the fake model's
// chat completions, as OpenCode would, and plays its reply. A Code Mode call of an MCP tool
// on one of servers runs it and asks again with its result; any other tool
// call holds the turn until it is interrupted: the emulator runs no other
// tools. It runs without the lock, after the prompt is answered, as in
// OpenCode, so a tool may call back into this service (a child's session).
func (s *Server) ask(msgs []map[string]any, servers map[string]map[string]any, effort string) Turn {
	var tools []Tool
	for {
		t, calls := s.complete(msgs, effort)
		t.Tools = tools
		if t.Fail != "" || len(calls) == 0 {
			return t
		}
		msgs = append(msgs, map[string]any{"role": "assistant", "tool_calls": calls})
		for _, c := range calls {
			out, ok := runTool(servers, c.Function.Name, c.Function.Arguments)
			if !ok {
				t.Hold = true
				return t
			}
			var input map[string]any
			_ = json.Unmarshal([]byte(c.Function.Arguments), &input)
			tools = append(tools, Tool{ID: c.ID, Name: c.Function.Name, Input: input, Output: out})
			msgs = append(msgs, map[string]any{"role": "tool", "tool_call_id": c.ID, "content": out})
		}
	}
}

// toolCall is one streamed tool call, its arguments joined across deltas.
type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// complete runs one chat completion: its text and its tool calls. effort,
// the session model's variant, goes as reasoning_effort, as OpenCode sends an
// openai-compatible model's variant.
func (s *Server) complete(msgs []map[string]any, effort string) (Turn, []toolCall) {
	req := map[string]any{"model": "m", "stream": true, "messages": msgs}
	if effort != "" {
		req["reasoning_effort"] = effort
	}
	b, _ := json.Marshal(req)
	resp, err := modelClient.Post(s.Model+"/chat/completions", "application/json", bytes.NewReader(b))
	if err != nil {
		return Turn{Fail: err.Error()}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Turn{Fail: "model: " + resp.Status}, nil
	}
	var t Turn
	var calls []toolCall
	for sc := bufio.NewScanner(resp.Body); sc.Scan(); {
		var c struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index int `json:"index"`
						toolCall
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		_, v, _ := strings.Cut(sc.Text(), ":") // a data field's JSON; other lines don't parse
		_ = json.Unmarshal([]byte(v), &c)
		for _, ch := range c.Choices {
			t.Text += ch.Delta.Content
			for _, d := range ch.Delta.ToolCalls {
				for len(calls) <= d.Index {
					calls = append(calls, toolCall{Type: "function"})
				}
				tc := &calls[d.Index]
				tc.ID += d.ID
				tc.Function.Name += d.Function.Name
				tc.Function.Arguments += d.Function.Arguments
			}
		}
	}
	return t, calls
}

// codeCall is the one Code Mode call the emulator runs: tools.<server>.<tool>(<JSON args>).
var codeCall = regexp.MustCompile(`tools\.([\w-]+)\.(\w+)\(\s*(\{.*\})?\s*\)`)

// runTool runs an execute call of an MCP tool on the session location's
// registered server, as OpenCode's Code Mode does, and returns the tool's
// output (its structured content, else its text) or the failure. false
// means the call is not one the emulator runs.
func runTool(servers map[string]map[string]any, name, args string) (string, bool) {
	var in struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal([]byte(args), &in)
	m := codeCall.FindStringSubmatch(in.Code)
	if name != "execute" || m == nil {
		return "", false
	}
	cfg := servers[m[1]]
	cmd, _ := cfg["command"].([]any)
	if len(cmd) == 0 {
		return "MCP server not found: " + m[1], true
	}
	argv := make([]string, len(cmd))
	for i, a := range cmd {
		argv[i], _ = a.(string)
	}
	toolArgs := map[string]any{}
	if m[3] != "" {
		if err := json.Unmarshal([]byte(m[3]), &toolArgs); err != nil {
			return "invalid tool arguments: " + err.Error(), true
		}
	}
	env, _ := cfg["environment"].(map[string]any)
	return callMCP(argv, env, m[2], toolArgs), true
}

// callMCP runs tool on the stdio MCP server argv started with env, and
// returns its output (its structured content, else its text) or the failure.
func callMCP(argv []string, env map[string]any, tool string, args map[string]any) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // G204: the command Loom registered for this location.
	c.Env = os.Environ()
	for k, v := range env {
		c.Env = append(c.Env, fmt.Sprintf("%s=%v", k, v))
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "loom-harness-emu", Version: Version}, nil).
		Connect(ctx, &mcp.CommandTransport{Command: c}, nil)
	if err != nil {
		return err.Error()
	}
	defer func() { _ = cs.Close() }()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return err.Error()
	}
	if res.StructuredContent != nil {
		out, _ := json.Marshal(res.StructuredContent)
		return string(out)
	}
	var out strings.Builder
	for _, part := range res.Content {
		if tc, ok := part.(*mcp.TextContent); ok {
			out.WriteString(tc.Text)
		}
	}
	return out.String()
}

// turn is r's turn, asking Model first, with the lock released, when it
// says to; false means r stopped or was replaced, or the server is closing.
func (s *Server) turn(sid string, r *run) (Turn, bool) {
	if !r.Turn.Ask {
		return r.Turn, true
	}
	ss := s.st.Sessions[sid]
	msgs := []map[string]any{}
	for _, m := range ss.Messages {
		switch m["type"] {
		case "user":
			msgs = append(msgs, map[string]any{"role": "user", "content": m["text"]})
		case "assistant":
			if text := replyText(m); text != "" {
				msgs = append(msgs, map[string]any{"role": "assistant", "content": text})
			}
		}
	}
	loc, _ := ss.Info["location"].(map[string]any)
	dir, _ := loc["directory"].(string)
	servers := maps.Clone(s.mcp[dir])
	model, _ := ss.Info["model"].(map[string]any)
	effort, _ := model["variant"].(string)
	s.mu.Unlock()
	t := s.ask(msgs, servers, effort)
	s.mu.Lock()
	select {
	case <-s.quit:
		return t, false
	default:
	}
	ss = s.st.Sessions[sid]
	return t, ss != nil && ss.Running == r
}

// replyText is an assistant message's text parts, as OpenCode sends an earlier
// reply back to the model; its content is typed once loaded from the state.
func replyText(m map[string]any) string {
	var parts []struct{ Type, Text string }
	b, _ := json.Marshal(m["content"])
	_ = json.Unmarshal(b, &parts)
	var out strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			out.WriteString(p.Text)
		}
	}
	return out.String()
}

// play streams turn r of session sid. Every change happens under the lock
// and only while r is still the session's running turn.
func (s *Server) play(sid string, r *run) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.turn(sid, r)
	if !ok {
		return
	}
	if t.Child {
		child := "ses_" + s.newID()
		s.st.Sessions[child] = &session{Info: map[string]any{"id": child, "parentID": sid, "metadata": map[string]any{}}}
		s.emit(child, "session.created", map[string]any{"parentID": sid})
	}
	msg := map[string]any{"id": "msg_" + s.newID(), "type": "assistant", "content": []map[string]any{}}
	s.add(sid, msg)
	for _, tool := range t.Tools {
		if !s.playTool(sid, r, msg, tool) {
			return
		}
	}
	if !s.playParts(sid, r, msg, t) {
		return
	}
	if t.Suspend {
		s.suspend(sid)
		return
	}
	if t.Hold && !s.wait(sid, r, -1) {
		return
	}
	msg["time"].(map[string]int64)["completed"] = time.Now().UnixMilli()
	outcome := "succeeded"
	if t.Fail != "" {
		outcome, msg["error"] = "failed", map[string]string{"message": t.Fail}
	} else {
		msg["finish"], msg["tokens"], msg["cost"] = "stop", t.Tokens, t.Cost
		s.emit(sid, "session.step.ended", map[string]any{"assistantMessageID": msg["id"], "tokens": t.Tokens, "cost": t.Cost})
	}
	s.end(sid, outcome, map[string]any{"error": msg["error"]})
}

// playParts streams t's reasoning, then its text, into msg; false means r
// stopped.
func (s *Server) playParts(sid string, r *run, msg map[string]any, t Turn) bool {
	for _, p := range []struct{ kind, text string }{{"reasoning", t.Reasoning}, {"text", t.Text}} {
		if p.text == "" {
			continue
		}
		part := map[string]any{"type": p.kind, "text": ""}
		msg["content"] = append(msg["content"].([]map[string]any), part)
		ord := map[string]any{"assistantMessageID": msg["id"], "ordinal": 0}
		s.emit(sid, "session."+p.kind+".started", clone(ord))
		for _, d := range strings.SplitAfter(p.text, " ") {
			if !s.wait(sid, r, time.Duration(t.DelayMS)*time.Millisecond) {
				return false
			}
			part["text"] = part["text"].(string) + d
			s.emit(sid, "session."+p.kind+".delta", with(ord, "delta", d))
		}
		s.emit(sid, "session."+p.kind+".ended", with(ord, "text", p.text))
	}
	return true
}

// playTool plays one tool call as OpenCode b30c4d0 does: its input start
// names the tool, its call carries the input, then any permission or
// question ask waits for its reply, and its success the content, or its
// failure the error; the message keeps the call's final state. false means
// r stopped while an ask waited.
func (s *Server) playTool(sid string, r *run, msg map[string]any, tool Tool) bool {
	name := tool.Name
	if name == "" {
		name = "execute"
	}
	input := tool.Input
	if input == nil {
		input = map[string]any{}
	}
	call := map[string]any{"assistantMessageID": msg["id"], "id": tool.ID}
	s.emit(sid, "session.tool.input.started", with(call, "name", name))
	s.emit(sid, "session.tool.called", with(with(call, "input", input), "executed", true))
	output, fail := tool.Output, tool.Fail
	if name == "subagent" && fail == "" {
		fail = s.subagent(sid, input)
	}
	source := map[string]any{"type": "tool", "messageID": msg["id"], "id": tool.ID}
	if tool.Permission != nil {
		ans, ok := s.await(sid, r, "per_"+s.newID(), with(tool.Permission, "source", source))
		if !ok {
			return false
		}
		if ans.(map[string]any)["decision"] == "reject" {
			s.decline(sid, msg, call, name, input)
			return false
		}
	}
	if len(tool.Questions) > 0 {
		ans, ok := s.await(sid, r, "frm_"+s.newID(), questionForm(tool.Questions))
		if !ok {
			return false
		}
		output = formAnswers(tool.Questions, ans.(map[string]any))
	}
	content := []map[string]any{{"type": "text", "text": output}}
	state := map[string]any{"status": "completed", "input": input, "content": content}
	if fail != "" {
		e := map[string]any{"type": "tool", "message": fail}
		state = map[string]any{"status": "error", "input": input, "error": e}
		s.emit(sid, "session.tool.failed", with(with(call, "error", e), "executed", true))
	} else {
		s.emit(sid, "session.tool.success", with(with(call, "content", content), "executed", true))
	}
	msg["content"] = append(msg["content"].([]map[string]any), map[string]any{"type": "tool", "id": tool.ID, "name": name, "state": state})
	return true
}

// decline plays a rejected permission as OpenCode b30c4d0 does: the call
// fails as declined and the step as interrupted, and the step interrupts
// itself with no reason, so the execution ends as a "shutdown" interrupt
// with no idle marker (core/src/session/runner/step.ts, execution.ts
// terminal) and the session runs nothing more.
func (s *Server) decline(sid string, msg, call map[string]any, name string, input map[string]any) {
	e := map[string]any{"type": "aborted", "message": "The user declined this tool call"}
	s.emit(sid, "session.tool.failed", with(with(call, "error", e), "executed", false))
	part := map[string]any{"type": "tool", "id": call["id"], "name": name, "state": map[string]any{"status": "error", "input": input, "error": e}}
	msg["content"] = append(msg["content"].([]map[string]any), part)
	msg["error"] = map[string]any{"type": "aborted", "message": "Step interrupted"}
	s.suspend(sid)
}

// suspend ends the running execution as a "shutdown" interrupt that writes
// no idle marker, and runs nothing more. Not modeled: real OpenCode
// also keeps the session claimed, so its next boot would resume the turn.
func (s *Server) suspend(sid string) {
	s.st.Sessions[sid].Running = nil
	s.emit(sid, "session.execution.interrupted", map[string]any{"reason": "shutdown"})
	s.save()
}

// wait unlocks for d (forever when d < 0); false means r stopped or was
// replaced, or the server is closing.
func (s *Server) wait(sid string, r *run, d time.Duration) bool {
	s.mu.Unlock()
	var after <-chan time.Time
	if d >= 0 {
		after = time.After(d)
	}
	select {
	case <-after:
	case <-r.stop:
	case <-s.quit:
	}
	s.mu.Lock()
	select {
	case <-s.quit:
		return false
	default:
	}
	ss := s.st.Sessions[sid]
	return ss != nil && ss.Running == r
}

// end finishes the running turn with an execution event and its idle marker.
func (s *Server) end(sid, outcome string, data map[string]any) {
	ss := s.st.Sessions[sid]
	ss.Running = nil
	id := s.emit(sid, "session.execution."+outcome, data)
	s.add(sid, map[string]any{"id": "msg_" + strings.TrimPrefix(id, "evt_"), "type": "idle", "outcome": outcome})
	s.save()
}

func clone(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func with(m map[string]any, k string, v any) map[string]any { out := clone(m); out[k] = v; return out }

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// h runs fn under the lock with the request's session, or answers OpenCode's 404.
func (s *Server) h(fn func(w http.ResponseWriter, r *http.Request, ss *session, body map[string]any)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		defer s.mu.Unlock()
		ss := s.st.Sessions[r.PathValue("id")]
		if ss == nil {
			reply(w, 404, map[string]string{"_tag": "SessionNotFoundError", "message": "Session not found: " + r.PathValue("id")})
			return
		}
		fn(w, r, ss, body)
	}
}

// failableCreate answers 503 instead of calling create while the
// FailSessionCreate flag file beside the scenario file exists.
func (s *Server) failableCreate(create http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(s.scenarios + FailSessionCreate); s.scenarios != "" && err == nil {
			reply(w, http.StatusServiceUnavailable, map[string]any{"name": "UnknownError",
				"data": map[string]any{"message": "emulator: session create refused by " + FailSessionCreate}})
			return
		}
		create(w, r)
	}
}

// Handler serves the OpenCode HTTP surface behind basic auth.
//
//nolint:funlen // One route per handler; splitting hides the surface it serves.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.sessionRoutes(mux)
	s.instructionRoutes(mux)
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, 200, map[string]any{"pid": os.Getpid(), "version": Version})
	})
	// One model with the low, medium and high variants OpenCode gives an
	// openai-compatible model, so the Agent API catalog has an effort option.
	model := map[string]any{"id": "emu", "providerID": "emu", "name": "Emulator",
		"capabilities": map[string]any{"input": []string{"text"}}, "limit": map[string]int{"context": 200000},
		"variants": []map[string]string{{"id": "low"}, {"id": "medium"}, {"id": "high"}}}
	mux.HandleFunc("GET /api/model", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, 200, map[string]any{"data": []any{model}})
	})
	mux.HandleFunc("GET /api/model/default", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, 200, map[string]any{"data": model})
	})
	mux.HandleFunc("GET /api/provider", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, 200, map[string]any{"data": []map[string]string{{"id": "emu", "name": "Emulator"}}})
	})
	mux.HandleFunc("GET /api/agent", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"data": agents(r.URL.Query().Get("location[directory]"))})
	})
	// The child-session query: ?parentID=<id> lists that session's children
	// and ?parentID=null the roots (b30c4d0 SessionsQueryFields.parentID).
	mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		q := r.URL.Query()
		want := q.Get("parentID")
		if want == "null" {
			want = ""
		}
		out := []map[string]any{}
		for _, ss := range s.st.Sessions {
			parent, _ := ss.Info["parentID"].(string)
			if !q.Has("parentID") || parent == want {
				out = append(out, ss.Info)
			}
		}
		reply(w, 200, map[string]any{"data": out})
	})
	mux.HandleFunc("POST /api/session", s.failableCreate(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		defer s.mu.Unlock()
		id, _ := body["id"].(string)
		if id == "" {
			id = "ses_" + s.newID()
			body["id"] = id
		}
		if ss := s.st.Sessions[id]; ss != nil { // b30c4d0 answers a repeat with success and ignores the body
			reply(w, 200, map[string]any{"data": ss.Info})
			return
		}
		if body["metadata"] == nil {
			body["metadata"] = map[string]any{}
		}
		s.st.Sessions[id] = &session{Info: body}
		s.emit(id, "session.created", map[string]any{})
		reply(w, 200, map[string]any{"data": body})
	}))
	mux.HandleFunc("GET /api/session/active", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		out := map[string]any{}
		for id, ss := range s.st.Sessions {
			if ss.Running != nil {
				out[id] = map[string]string{"type": "running"}
			}
		}
		reply(w, 200, map[string]any{"data": out})
	})
	s.mcpRoutes(mux)
	mux.HandleFunc("GET /api/event", func(w http.ResponseWriter, r *http.Request) {
		ch := make(chan []byte, 1024)
		s.mu.Lock()
		s.subs[ch] = true
		s.mu.Unlock()
		defer func() { s.mu.Lock(); delete(s.subs, ch); s.mu.Unlock() }()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		sw, err := realtime.NewWriter(w)
		if err != nil {
			return
		}
		_ = sw.WriteComment("ok") // flushes the headers
		for {
			select {
			case b := <-ch:
				_ = sw.WriteEventNoID("message", string(b)) // "message" is SSE's default event type
			case <-r.Context().Done():
				return
			case <-s.quit:
				return
			}
		}
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "opencode" || p != s.Password {
			w.WriteHeader(401)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// mcpRoutes serves b30c4d0's runtime MCP servers per location: list, add
// or replace, and remove (protocol/src/groups/mcp.ts:9-56,
// server/src/handlers/mcp.ts:14-44, core/src/mcp/index.ts:615-647). Like
// OpenCode, the emulator keeps them in memory until it restarts. It starts
// no server: a local one whose command resolves reports connected.
func (s *Server) mcpRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/mcp", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		dir := r.URL.Query().Get("location[directory]")
		names := make([]string, 0, len(s.mcp[dir]))
		for name := range s.mcp[dir] {
			names = append(names, name)
		}
		slices.Sort(names)
		out := []map[string]any{}
		for _, name := range names {
			out = append(out, map[string]any{"name": name, "status": mcpStatus(s.mcp[dir][name])})
		}
		reply(w, 200, map[string]any{"location": map[string]string{"directory": dir}, "data": out})
	})
	mux.HandleFunc("PUT /api/experimental/mcp/{server}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Config map[string]any `json:"config"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		cmd, _ := body.Config["command"].([]any)
		if !(body.Config["type"] == "local" && len(cmd) > 0) && !(body.Config["type"] == "remote" && body.Config["url"] != nil) {
			reply(w, 400, map[string]string{"_tag": "HttpApiDecodeError", "message": "invalid MCP server config"})
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		dir := r.URL.Query().Get("location[directory]")
		if s.mcp[dir] == nil {
			s.mcp[dir] = map[string]map[string]any{}
		}
		s.mcp[dir][r.PathValue("server")] = body.Config
		w.WriteHeader(204)
	})
	mux.HandleFunc("DELETE /api/experimental/mcp/{server}", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		dir, name := r.URL.Query().Get("location[directory]"), r.PathValue("server")
		if _, ok := s.mcp[dir][name]; !ok {
			reply(w, 404, map[string]string{"_tag": "McpServerNotFoundError", "server": name, "message": "MCP server not found: " + name})
			return
		}
		delete(s.mcp[dir], name)
		w.WriteHeader(204)
	})
}

// mcpStatus is disabled, failed for a local command that does not resolve,
// or connected.
func mcpStatus(cfg map[string]any) map[string]string {
	if cfg["disabled"] == true {
		return map[string]string{"status": "disabled"}
	}
	if cmd, _ := cfg["command"].([]any); len(cmd) > 0 {
		bin, _ := cmd[0].(string)
		if _, err := exec.LookPath(bin); err != nil {
			return map[string]string{"status": "failed", "error": err.Error()}
		}
	}
	return map[string]string{"status": "connected"}
}

// sessionRoutes serves the routes of one session.
//
//nolint:funlen // One route per handler; splitting hides the surface it serves.
func (s *Server) sessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/session/{id}", s.h(func(w http.ResponseWriter, _ *http.Request, ss *session, _ map[string]any) {
		reply(w, 200, map[string]any{"data": ss.Info})
	}))
	mux.HandleFunc("PATCH /api/session/{id}", s.h(func(w http.ResponseWriter, _ *http.Request, ss *session, body map[string]any) {
		for k, v := range body {
			ss.Info[k] = v
		}
		s.save()
		w.WriteHeader(204)
	}))
	mux.HandleFunc("PUT /api/session/{id}/environment", s.h(func(w http.ResponseWriter, _ *http.Request, _ *session, _ map[string]any) {
		w.WriteHeader(204) // in memory only in OpenCode; the emulator runs no shell
	}))
	mux.HandleFunc("DELETE /api/session/{id}", s.h(func(w http.ResponseWriter, r *http.Request, _ *session, _ map[string]any) {
		delete(s.st.Sessions, r.PathValue("id"))
		s.save()
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /api/session/{id}/move", s.h(func(w http.ResponseWriter, _ *http.Request, ss *session, body map[string]any) {
		dir, _ := body["directory"].(string)
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			reply(w, 400, map[string]string{"_tag": "InvalidRequestError", "message": "Directory does not exist"})
			return
		}
		ss.Info["location"] = map[string]any{"directory": dir}
		s.save()
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /api/session/{id}/prompt", s.h(func(w http.ResponseWriter, r *http.Request, ss *session, body map[string]any) {
		id, _ := body["id"].(string)
		text, _ := body["text"].(string)
		for _, m := range ss.Messages {
			if m["id"] == id { // the first write per msg_ id wins
				reply(w, 200, map[string]any{"data": m})
				return
			}
		}
		s.st.Prompts = append(s.st.Prompts, Prompt{Session: r.PathValue("id"), Agent: ss.agent(), ID: id, Text: text})
		m := map[string]any{"id": id, "type": "user", "text": text}
		s.add(r.PathValue("id"), m)
		if ss.Running == nil { // a prompt while running joins the running turn
			ss.Running = &run{Turn: s.next(ss, text), stop: make(chan struct{})}
			s.save()
			go s.play(r.PathValue("id"), ss.Running)
		}
		reply(w, 200, map[string]any{"data": m})
	}))
	mux.HandleFunc("POST /api/session/{id}/interrupt", s.h(func(w http.ResponseWriter, r *http.Request, ss *session, _ map[string]any) {
		run := ss.Running
		if run != nil {
			close(run.stop)
			s.end(r.PathValue("id"), "interrupted", map[string]any{"reason": "user"})
		}
		reply(w, 200, map[string]bool{"interrupted": run != nil})
	}))
	mux.HandleFunc("POST /api/session/{id}/model", s.h(func(w http.ResponseWriter, _ *http.Request, ss *session, body map[string]any) {
		ss.Info["model"] = body["model"]
		s.save()
		w.WriteHeader(204)
	}))
	s.askRoutes(mux)
	mux.HandleFunc("GET /api/session/{id}/message", s.h(func(w http.ResponseWriter, r *http.Request, ss *session, _ map[string]any) {
		page, next := pageOf(ss.Messages, r.URL.Query())
		reply(w, 200, map[string]any{"data": page, "cursor": map[string]string{"next": next}})
	}))
}

// pageOf pages messages like OpenCode: order asc or desc (the default), and
// a cursor that is base64url JSON {id, order, direction} of the last
// message returned; a cursor on an unknown message gives an empty page.
func pageOf(all []map[string]any, q url.Values) ([]map[string]any, string) {
	get := q.Get
	order, from := get("order"), 0
	if order == "" {
		order = "desc"
	}
	if c := get("cursor"); c != "" {
		var cur struct{ ID, Order string }
		raw, _ := base64.RawURLEncoding.DecodeString(c)
		_ = json.Unmarshal(raw, &cur)
		order, from = cur.Order, -1
		for i, m := range all {
			if m["id"] == cur.ID {
				from = i
			}
		}
		if from < 0 {
			return []map[string]any{}, ""
		}
		if order == "asc" {
			from++
		} else {
			from = len(all) - from
		}
	}
	seq := make([]map[string]any, len(all))
	for i := range all {
		seq[i] = all[i]
		if order != "asc" {
			seq[i] = all[len(all)-1-i]
		}
	}
	to := len(seq)
	if n, err := strconv.Atoi(get("limit")); err == nil && from+n < to {
		to = from + n
	}
	page := seq[from:to]
	if len(page) == 0 {
		return page, ""
	}
	raw, _ := json.Marshal(map[string]any{"id": page[len(page)-1]["id"], "order": order, "direction": "next"})
	return page, base64.RawURLEncoding.EncodeToString(raw)
}

// agents lists "build" and the .opencode/agent/*.md agents of dir and its
// parents, which is where OpenCode finds Loom's presets.
func agents(dir string) []map[string]string {
	out := []map[string]string{{"id": "build"}}
	for d := filepath.Clean(dir); dir != ""; d = filepath.Dir(d) {
		files, _ := filepath.Glob(filepath.Join(d, ".opencode", "agent", "*.md"))
		for _, f := range files {
			out = append(out, map[string]string{"id": strings.TrimSuffix(filepath.Base(f), ".md")})
		}
		if d == filepath.Dir(d) {
			break
		}
	}
	return out
}
