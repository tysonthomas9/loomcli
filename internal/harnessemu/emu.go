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
// scenario; a prompt with no scripted turn left echoes its text.
//
// Modeled, not proven: real OpenCode keeps sessions in its database. The
// emulator saves its state to one JSON file, so a restart keeps sessions,
// and a turn that was running at a stop resumes on the next boot after a
// synthetic restart notice, as OpenCode's boot sweep does.
package harnessemu

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
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
	path      string // state file
	scenarios string // scenario file
	mu        sync.Mutex
	st        state
	subs      map[chan []byte]bool
	quit      chan struct{}
}

// New loads the state file (absent is empty) and resumes turns a stop left running.
func New(statePath, scenarios, password string) (*Server, error) {
	s := &Server{Password: password, path: statePath, scenarios: scenarios, subs: map[chan []byte]bool{}, quit: make(chan struct{}),
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
	return Turn{Text: text}
}

// play streams turn r of session sid. Every change happens under the lock
// and only while r is still the session's running turn.
func (s *Server) play(sid string, r *run) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := r.Turn
	if t.Child {
		child := "ses_" + s.newID()
		s.st.Sessions[child] = &session{Info: map[string]any{"id": child, "parentID": sid, "metadata": map[string]any{}}}
		s.emit(child, "session.created", map[string]any{"parentID": sid})
	}
	msg := map[string]any{"id": "msg_" + s.newID(), "type": "assistant", "content": []map[string]any{}}
	s.add(sid, msg)
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
				return
			}
			part["text"] = part["text"].(string) + d
			s.emit(sid, "session."+p.kind+".delta", with(ord, "delta", d))
		}
		s.emit(sid, "session."+p.kind+".ended", with(ord, "text", p.text))
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

// Handler serves the OpenCode HTTP surface behind basic auth.
//
//nolint:funlen // One route per handler; splitting hides the surface it serves.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.sessionRoutes(mux)
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, 200, map[string]any{"pid": os.Getpid(), "version": Version})
	})
	mux.HandleFunc("GET /api/model", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, 200, map[string]any{"data": []map[string]string{{"id": "emu", "providerID": "emu", "name": "Emulator"}}})
	})
	mux.HandleFunc("GET /api/agent", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"data": agents(r.URL.Query().Get("location[directory]"))})
	})
	mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		out := []map[string]any{}
		for _, ss := range s.st.Sessions {
			out = append(out, ss.Info)
		}
		reply(w, 200, map[string]any{"data": out})
	})
	mux.HandleFunc("POST /api/session", func(w http.ResponseWriter, r *http.Request) {
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
	})
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
	mux.HandleFunc("GET /api/event", func(w http.ResponseWriter, r *http.Request) {
		ch := make(chan []byte, 1024)
		s.mu.Lock()
		s.subs[ch] = true
		s.mu.Unlock()
		defer func() { s.mu.Lock(); delete(s.subs, ch); s.mu.Unlock() }()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		for {
			select {
			case b := <-ch:
				_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
				w.(http.Flusher).Flush()
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
	none := s.h(func(w http.ResponseWriter, _ *http.Request, _ *session, _ map[string]any) {
		reply(w, 200, map[string]any{"data": []any{}}) // the core raises no asks
	})
	mux.HandleFunc("GET /api/session/{id}/permission", none)
	mux.HandleFunc("GET /api/session/{id}/form", none)
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
