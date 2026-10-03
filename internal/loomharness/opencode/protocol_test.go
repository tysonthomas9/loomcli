package opencode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// fakeServer is the OpenCode HTTP surface the adapter uses, kept in memory.
// Its state lives in store so a second server can play a restarted OpenCode.
type store struct {
	mu       sync.Mutex
	sessions map[string]map[string]any
	messages map[string][]map[string]any
	active   map[string]string
	deleted  []string
	streams  [][]string // one scripted /api/event stream per connection
	conns    int
	envs     map[string]map[string]string // in memory only in OpenCode: lost on restart
	envFail  bool
	bareRuns int // prompts accepted while the session had no environment
	patchErr bool
	delErr   bool                      // DELETE /api/session/{id} fails
	patchLie int                       // the next n session PATCHes commit, then answer 500
	hangLie  bool                      // a PATCH that commits (patchLie) never answers instead
	stops    int                       // POST /interrupt calls
	lateRuns int                       // prompts accepted after an interrupt
	stopErr  bool                      // POST /interrupt answers 500
	stopHang bool                      // POST /interrupt never answers
	postErr  bool                      // POST /api/session creates the session, then fails
	race     *openRace                 // pairs two concurrent session GETs, counts creates
	perms    map[string]permReq        // pending permission asks by id, readable and answerable
	replies  map[string]string         // permission ask id -> the decision Loom sent
	agents   map[string]bool           // agent ids the service offers
	agentDir []string                  // location[directory] of each agent lookup
	loading  bool                      // the location lists no agents yet
	asks     map[string][]string       // pending per_/frm_ ask ids, per session
	forms    map[string]form           // pending forms by id, as GET form/{id} and the form list give them
	answers  map[string]map[string]any // form id -> the answer Loom sent
	mcp      string                    // a registered loom MCP server's /api/mcp status; "" is connected
	bridges  map[string]map[string]any // location dir -> the loom MCP config PUT there
	puts     int                       // PUT /api/experimental/mcp/loom calls
}

func newStore() *store {
	return &store{sessions: map[string]map[string]any{}, messages: map[string][]map[string]any{}, active: map[string]string{},
		envs: map[string]map[string]string{}, perms: map[string]permReq{}, replies: map[string]string{}}
}

func fakeServer(t *testing.T, st *store) *Client {
	t.Helper()
	const password = "pw"
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	missing := func(w http.ResponseWriter, id string) {
		reply(w, 404, map[string]string{"_tag": "SessionNotFoundError", "message": "Session not found: " + id})
	}
	mux.HandleFunc("POST /api/session", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		st.mu.Lock()
		defer st.mu.Unlock()
		id, _ := body["id"].(string)
		if st.race != nil {
			if st.race.posts++; st.race.posts == st.race.failPost {
				st.sessions[id] = body // committed, then the reply is lost
				reply(w, 504, map[string]string{"_tag": "UnknownError", "message": "timeout"})
				return
			}
		}
		if _, ok := st.sessions[id]; ok {
			reply(w, 409, map[string]string{"_tag": "ConflictError", "message": "exists"})
			return
		}
		st.sessions[id] = body
		if st.postErr {
			reply(w, 500, map[string]string{"_tag": "UnknownError", "message": "boom after commit"})
			return
		}
		reply(w, 200, map[string]any{"data": body})
	})
	mux.HandleFunc("GET /api/mcp", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		data := []map[string]any{}
		if _, ok := st.bridges[r.URL.Query().Get("location[directory]")]; ok {
			status := st.mcp
			if status == "" {
				status = "connected"
			}
			data = append(data, map[string]any{"name": "loom", "status": map[string]string{"status": status}})
		}
		reply(w, 200, map[string]any{"data": data})
	})
	mux.HandleFunc("PUT /api/experimental/mcp/loom", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Config map[string]any `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Config["type"] != "local" || body.Config["command"] == nil {
			reply(w, 400, map[string]string{"_tag": "BadRequest", "message": "bad config"})
			return
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		if st.bridges == nil {
			st.bridges = map[string]map[string]any{}
		}
		st.bridges[r.URL.Query().Get("location[directory]")] = body.Config
		st.puts++
		w.WriteHeader(204)
	})
	mux.HandleFunc("DELETE /api/experimental/mcp/loom", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		dir := r.URL.Query().Get("location[directory]")
		if _, ok := st.bridges[dir]; !ok {
			reply(w, 404, map[string]string{"_tag": "McpServerNotFoundError", "message": "loom"})
			return
		}
		delete(st.bridges, dir)
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /api/agent", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		st.agentDir = append(st.agentDir, r.URL.Query().Get("location[directory]"))
		data := []map[string]string{{"id": "build"}}
		if st.loading {
			data = nil
		}
		for id := range st.agents {
			data = append(data, map[string]string{"id": id})
		}
		reply(w, 200, map[string]any{"data": data})
	})
	mux.HandleFunc("GET /api/session/active", func(w http.ResponseWriter, _ *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		data := map[string]any{}
		for id, typ := range st.active {
			data[id] = map[string]string{"type": typ}
		}
		reply(w, 200, map[string]any{"data": data})
	})
	mux.HandleFunc("GET /api/session/{id}", func(w http.ResponseWriter, r *http.Request) {
		if st.race != nil {
			st.race.pair()
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		if s, ok := st.sessions[r.PathValue("id")]; ok {
			reply(w, 200, map[string]any{"data": s})
			return
		}
		missing(w, r.PathValue("id"))
	})
	mux.HandleFunc("PATCH /api/session/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		st.mu.Lock()
		defer st.mu.Unlock()
		s, ok := st.sessions[r.PathValue("id")]
		switch {
		case !ok:
			missing(w, r.PathValue("id"))
		case st.patchErr:
			reply(w, 500, map[string]string{"_tag": "UnknownError", "message": "boom"})
		case st.patchLie > 0:
			st.patchLie--
			s["permissions"] = body["permissions"]
			if st.hangLie && st.patchLie == 0 {
				st.mu.Unlock()
				<-r.Context().Done()
				st.mu.Lock()
				return
			}
			reply(w, 500, map[string]string{"_tag": "UnknownError", "message": "boom after commit"})
		default:
			if p, ok := body["permissions"]; ok {
				s["permissions"] = p
			}
			w.WriteHeader(204)
		}
	})
	mux.HandleFunc("DELETE /api/session/{id}", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		id := r.PathValue("id")
		st.deleted = append(st.deleted, id)
		if st.delErr {
			reply(w, 500, map[string]string{"_tag": "UnknownError", "message": "disk full"})
			return
		}
		if _, ok := st.sessions[id]; !ok {
			missing(w, id)
			return
		}
		delete(st.sessions, id)
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /api/session/{id}/prompt", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		st.mu.Lock()
		defer st.mu.Unlock()
		id := r.PathValue("id")
		if _, ok := st.sessions[id]; !ok {
			missing(w, id)
			return
		}
		for _, m := range st.messages[id] {
			if m["id"] == body["id"] {
				reply(w, 200, map[string]any{"data": m})
				return
			}
		}
		if st.envs[id] == nil {
			st.bareRuns++
		}
		if st.stops > 0 {
			st.lateRuns++
		}
		m := map[string]any{"id": body["id"], "type": "user", "text": body["text"], "time": map[string]int64{"created": 1}}
		st.messages[id] = append(st.messages[id], m)
		reply(w, 200, map[string]any{"data": m})
	})
	mux.HandleFunc("PUT /api/session/{id}/environment", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]string `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		st.mu.Lock()
		defer st.mu.Unlock()
		if st.envFail {
			reply(w, 500, map[string]string{"_tag": "UnknownError", "message": "boom"})
			return
		}
		st.envs[r.PathValue("id")] = body.Variables
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /api/session/{id}/move", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		st.mu.Lock()
		defer st.mu.Unlock()
		if body["directory"] == "" || strings.Contains(body["directory"], "missing") {
			reply(w, 400, map[string]string{"_tag": "InvalidRequestError", "message": "Directory does not exist"})
			return
		}
		st.sessions[r.PathValue("id")]["location"] = map[string]any{"directory": body["directory"]}
		w.WriteHeader(204)
	})
	// The message list pages like OpenCode's: the cursor is base64url JSON
	// {id, order, direction} of the page's last message, and a cursor on an
	// unknown message gives an empty page.
	mux.HandleFunc("GET /api/session/{id}/message", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		all := st.messages[r.PathValue("id")]
		order, from := r.URL.Query().Get("order"), 0
		if order == "" {
			order = "desc"
		}
		if c := r.URL.Query().Get("cursor"); c != "" {
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
				reply(w, 200, map[string]any{"data": []any{}, "cursor": map[string]string{}})
				return
			}
			if order == "asc" {
				from++
			} else {
				from = len(all) - from
			}
		}
		seq := make([]map[string]any, 0, len(all))
		for i := range all {
			if order == "asc" {
				seq = append(seq, all[i])
			} else {
				seq = append(seq, all[len(all)-1-i])
			}
		}
		to := len(seq)
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && from+n < to {
			to = from + n
		}
		page := seq[from:to]
		next := ""
		if len(page) > 0 {
			raw, _ := json.Marshal(map[string]any{"id": page[len(page)-1]["id"], "order": order, "direction": "next"})
			next = base64.RawURLEncoding.EncodeToString(raw)
		}
		reply(w, 200, map[string]any{"data": page, "cursor": map[string]string{"next": next}})
	})
	pending := func(prefix string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			st.mu.Lock()
			defer st.mu.Unlock()
			out := []any{}
			for _, id := range st.asks[r.PathValue("id")] {
				switch p, isPerm := st.perms[id]; {
				case !strings.HasPrefix(id, prefix):
				case isPerm:
					out = append(out, struct {
						ID string `json:"id"`
						permReq
					}{id, p})
				case st.forms[id].ID != "":
					out = append(out, st.forms[id])
				default:
					out = append(out, map[string]string{"id": id})
				}
			}
			reply(w, 200, map[string]any{"data": out})
		}
	}
	mux.HandleFunc("POST /api/session/{id}/interrupt", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		st.stops++
		fail, hang := st.stopErr, st.stopHang
		st.mu.Unlock()
		switch {
		case hang:
			<-r.Context().Done()
		case fail:
			reply(w, 500, map[string]string{"_tag": "UnknownError", "message": "stop failed"})
		default:
			reply(w, 200, map[string]any{"data": map[string]bool{"interrupted": true}})
		}
	})
	mux.HandleFunc("GET /api/session/{id}/permission", pending("per_"))
	mux.HandleFunc("GET /api/session/{id}/permission/{rid}", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		p, ok := st.perms[r.PathValue("rid")]
		if !ok || p.Session != r.PathValue("id") {
			reply(w, 404, map[string]string{"_tag": "PermissionNotFoundError", "message": "no request"})
			return
		}
		reply(w, 200, map[string]any{"data": p})
	})
	mux.HandleFunc("POST /api/session/{id}/permission/{rid}/reply", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		st.mu.Lock()
		defer st.mu.Unlock()
		rid := r.PathValue("rid")
		if p, ok := st.perms[rid]; !ok || p.Session != r.PathValue("id") {
			reply(w, 404, map[string]string{"_tag": "PermissionNotFoundError", "message": "no request"})
			return
		}
		delete(st.perms, rid)
		st.replies[rid] = body["decision"]
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /api/session/{id}/form", pending("frm_"))
	mux.HandleFunc("GET /api/session/{id}/form/{fid}", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		reply(w, 200, map[string]any{"data": st.forms[r.PathValue("fid")]})
	})
	mux.HandleFunc("POST /api/session/{id}/form/{fid}/reply", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Answer map[string]any }
		_ = json.NewDecoder(r.Body).Decode(&body)
		st.mu.Lock()
		defer st.mu.Unlock()
		if st.answers == nil {
			st.answers = map[string]map[string]any{}
		}
		st.answers[r.PathValue("fid")] = body.Answer
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /api/event", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		var lines []string
		if st.conns < len(st.streams) {
			lines = st.streams[st.conns]
		}
		st.conns++
		st.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		for _, l := range lines {
			_, _ = w.Write([]byte("data: " + l + "\n\n"))
		}
		if len(lines) == 0 {
			<-r.Context().Done() // the last stream stays open until the client goes
		}
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "opencode" || p != password {
			w.WriteHeader(401)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, password)
}

func TestProtocolDerivedIDs(t *testing.T) {
	if SessionID("agent-1") != SessionID("agent-1") || PromptID("agent-1", "req-1") != PromptID("agent-1", "req-1") {
		t.Fatal("derived ids are not stable")
	}
	if !strings.HasPrefix(SessionID("a"), "ses_") || !strings.HasPrefix(PromptID("a", "r"), "msg_") {
		t.Fatalf("bad prefixes: %s %s", SessionID("a"), PromptID("a", "r"))
	}
	if SessionID("a") == SessionID("b") || PromptID("a", "r1") == PromptID("a", "r2") || PromptID("a", "r") == PromptID("b", "r") {
		t.Fatal("derived ids collide")
	}
}

func TestProtocolSessionMethods(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	st.agents = map[string]bool{"loom-lead": true}
	c := fakeServer(t, st)
	c.presets = "/"
	spec := loomharness.OpenSpec{
		Key: "agent-1", Launch: loomharness.Launch{Root: "/root"}, Dir: "/repo", Model: "openai/gpt-x",
		Preset: loomharness.PresetConfig{Name: "lead"}, Metadata: map[string]string{"agent_id": "agent-1"},
		Rules: []loomharness.PermissionRule{{Action: "bash", Resource: "*", Effect: "ask"}},
	}
	ref, err := c.Open(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if ref != (loomharness.NativeRef{Root: "/root", NativeID: SessionID("agent-1")}) {
		t.Fatalf("ref = %+v", ref)
	}
	body := st.sessions[ref.NativeID]
	if body["agent"] != "loom-lead" || body["model"].(map[string]any)["providerID"] != "openai" || body["permissions"] == nil || body["metadata"] == nil {
		t.Fatalf("open body = %v", body)
	}
	if again, err := c.Open(ctx, spec); err != nil || again != ref {
		t.Fatalf("repeat Open = %+v, %v", again, err)
	}

	s := c.Session(ref)
	key := PromptID("agent-1", "req-1")
	if err := s.Prompt(ctx, loomharness.Input{Key: key, Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Prompt(ctx, loomharness.Input{Key: key, Text: "changed"}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.HasInput(ctx, key); err != nil || got != loomharness.LandedFound {
		t.Fatalf("HasInput(sent) = %v, %v", got, err)
	}
	if got, err := s.HasInput(ctx, PromptID("agent-1", "req-2")); err != nil || got != loomharness.LandedNotFound {
		t.Fatalf("HasInput(unsent) = %v, %v", got, err)
	}

	st.active[ref.NativeID] = "running"
	if got, err := s.Status(ctx); err != nil || !got.Running {
		t.Fatalf("Status = %+v, %v", got, err)
	}
	delete(st.active, ref.NativeID)
	if got, _ := s.Status(ctx); got.Running {
		t.Fatal("Status running after the turn ended")
	}

	if err := s.Move(ctx, "/repo2"); err != nil {
		t.Fatal(err)
	}
	if err := s.Move(ctx, "/missing"); err == nil || !isCode(err, "bad_request") {
		t.Fatalf("Move to a missing dir = %v", err)
	}

	if got, err := s.Resume(ctx, loomharness.Launch{Root: "/root"}, nil); err != nil || got != ref {
		t.Fatalf("Resume = %+v, %v", got, err)
	}
	gone := c.Session(loomharness.NativeRef{NativeID: SessionID("agent-gone")})
	if _, err := gone.Resume(ctx, loomharness.Launch{}, nil); !errors.Is(err, loomharness.ErrSessionNotFound) {
		t.Fatalf("Resume of a missing session = %v", err)
	}
}

func TestProtocolMessagesPagesAndMapsItems(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	c := fakeServer(t, st)
	ref, _ := c.Open(ctx, loomharness.OpenSpec{Key: "agent-1", Launch: loomharness.Launch{Root: "/root-a"}, Dir: "/repo"})
	st.messages[ref.NativeID] = []map[string]any{
		{"id": "msg_u1", "type": "user", "text": "hi", "time": map[string]int64{"created": 1}},
		{"id": "msg_a1", "type": "assistant", "finish": "tool-calls", "time": map[string]int64{"created": 2}, "content": []map[string]any{
			{"type": "reasoning", "text": "think"},
			{"type": "tool", "id": "call_1", "name": "shell", "state": map[string]string{"status": "completed"}},
			{"type": "text", "text": "done"},
		}},
		{"id": "msg_s1", "type": "synthetic", "text": "The server restarted", "metadata": map[string]string{"notice": "restart"}},
		{"id": "msg_i1", "type": "idle", "outcome": "interrupted"},
		{"id": "msg_x1", "type": "instructions"},
		{"id": "msg_u2", "type": "user", "text": "again"},
		{"id": "msg_a2", "type": "assistant", "content": []map[string]any{
			{"type": "tool", "id": "call_2", "state": map[string]string{"status": "running"}},
			{"type": "text", "text": "stream"},
		}},
	}
	st.asks = map[string][]string{ref.NativeID: {"frm_1", "per_1"}}
	s := c.Session(ref)
	var pages []loomharness.MessagePage
	for after := ""; ; {
		p, err := s.Messages(ctx, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, p)
		if after = p.Next; after == "" {
			break
		}
	}
	if len(pages) != 4 {
		t.Fatalf("%d pages", len(pages))
	}
	var got []string
	for _, p := range pages {
		for _, e := range p.Events {
			if e.Session.Root != "/root-a" {
				t.Errorf("%s: Root %q", e.Type, e.Session.Root)
			}
			got = append(got, strings.Join([]string{string(e.Type), e.TurnID, e.ItemKind, e.ItemID, e.InputKey, e.AskID, e.StopReason}, " "))
		}
	}
	want := []string{
		"turn.started msg_u1   msg_u1  ",
		"message.delivered msg_u1 message msg_u1 msg_u1  ",
		"item.completed msg_u1 reasoning msg_a1/reasoning/0   ",
		"item.completed msg_u1 tool msg_a1/tool/call_1   ",
		"item.completed msg_u1 message msg_a1/text/0   ",
		"usage msg_u1  msg_a1   ",
		"turn.resumed msg_u1  msg_s1   ",
		"turn.completed msg_u1     cancelled",
		"turn.started msg_u2   msg_u2  ",
		"message.delivered msg_u2 message msg_u2 msg_u2  ",
		"ask.opened msg_u2    per_1 ",
		"ask.opened msg_u2 question   frm_1 ",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// A bare OpenCode cursor (no open turn recorded) reads the open turn back.
	q, _ := url.ParseQuery(pages[0].Next)
	p, err := s.Messages(ctx, q.Get("c"), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Events) != 2 || p.Events[0].Type != loomharness.EventTurnResumed || p.Events[0].TurnID != "msg_u1" {
		t.Fatalf("bare cursor page = %+v", p.Events)
	}
}

// After an OpenCode restart (a new server on a new port with the same data)
// the derived ids still find the same session and the same prompt.
func TestProtocolIDsStableAcrossRestart(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	spec := loomharness.OpenSpec{Key: "agent-1", Dir: "/repo"}
	key := PromptID("agent-1", "req-1")
	before := fakeServer(t, st)
	ref, err := before.Open(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := before.Session(ref).Prompt(ctx, loomharness.Input{Key: key, Text: "hi"}); err != nil {
		t.Fatal(err)
	}

	after := fakeServer(t, st)
	again, err := after.Open(ctx, spec)
	if err != nil || again != ref {
		t.Fatalf("Open after restart = %+v, %v (was %+v)", again, err, ref)
	}
	if got, err := after.Session(again).HasInput(ctx, PromptID("agent-1", "req-1")); err != nil || got != loomharness.LandedFound {
		t.Fatalf("HasInput after restart = %v, %v", got, err)
	}
	if len(st.sessions) != 1 || len(st.messages[ref.NativeID]) != 1 {
		t.Fatalf("restart created duplicates: %d sessions, %d messages", len(st.sessions), len(st.messages[ref.NativeID]))
	}
}

func TestProtocolPurgeDeletesOnlyRecordedIDs(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	c := fakeServer(t, st)
	mine, _ := c.Open(ctx, loomharness.OpenSpec{Key: "agent-1", Dir: "/repo"})
	sibling, _ := c.Open(ctx, loomharness.OpenSpec{Key: "agent-2", Dir: "/repo"})
	gone := loomharness.NativeRef{NativeID: SessionID("agent-gone")}
	if err := c.Purge(ctx, []loomharness.NativeRef{mine, gone}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(st.deleted, ",") != mine.NativeID+","+gone.NativeID {
		t.Fatalf("deleted %v", st.deleted)
	}
	if _, ok := st.sessions[sibling.NativeID]; !ok {
		t.Fatal("Purge deleted a sibling session")
	}
}

func TestProtocolErrorTranslation(t *testing.T) {
	cases := []struct {
		status   int
		body     string
		code     string
		sentinel error
	}{
		{503, `{}`, "harness_down", loomharness.ErrUnavailable},
		{404, `{"_tag":"SessionNotFoundError"}`, "session_missing", loomharness.ErrSessionNotFound},
		{404, `{"_tag":"PermissionNotFoundError"}`, "ask_missing", nil},
		{409, `{"_tag":"ConflictError"}`, "input_id_conflict", nil},
		{400, `{"_tag":"InvalidRequestError"}`, "bad_request", nil},
	}
	for _, tc := range cases {
		err := translate(tc.status, []byte(tc.body))
		if !isCode(err, tc.code) || (tc.sentinel != nil && !errors.Is(err, tc.sentinel)) {
			t.Errorf("%d %s -> %v", tc.status, tc.body, err)
		}
	}
	down := NewClient("http://127.0.0.1:1", "pw")
	if _, err := down.Session(loomharness.NativeRef{NativeID: "ses_x"}).Status(context.Background()); !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("connection refused = %v", err)
	}
}

func TestProtocolPolicyTranslation(t *testing.T) {
	got, err := nativeRules([]loomharness.PermissionRule{
		{Action: "*", Resource: "*", Effect: "deny"},
		{Action: "read", Resource: "*", Effect: "allow"},
		{Action: "bash", Resource: "gh *", Effect: "deny"},
		{Action: "edit", Resource: "*", Effect: "ask"},
		{Action: "subagent", Resource: "*", Effect: "deny"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var flat []string
	for _, r := range got {
		flat = append(flat, r["action"]+":"+r["resource"]+":"+r["effect"])
	}
	want := []string{"*:*:deny", "read:*:allow", "grep:*:allow", "glob:*:allow", "shell:gh *:deny", "edit:*:ask", "subagent:*:deny"}
	if strings.Join(flat, ",") != strings.Join(want, ",") {
		t.Fatalf("native rules = %v; want %v (order kept)", flat, want)
	}

	st := newStore()
	c := fakeServer(t, st)
	_, err = c.Open(context.Background(), loomharness.OpenSpec{Key: "a", Rules: []loomharness.PermissionRule{
		{Action: "bash", Resource: "*", Effect: "allow"}, {Action: "agent_create", Resource: "*", Effect: "deny"},
	}})
	if !isCode(err, "bad_request") || !strings.Contains(err.Error(), "agent_create") || len(st.sessions) != 0 {
		t.Fatalf("Open with an unmappable rule = %v, sessions %d; want refused before any call", err, len(st.sessions))
	}
}

func TestProtocolAuthErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		err := translate(status, []byte(`{"_tag":"UnauthorizedError","message":"Authentication required"}`))
		if !isCode(err, "auth_failed") || !errors.Is(err, loomharness.ErrUnavailable) {
			t.Fatalf("%d -> %v; want auth_failed", status, err)
		}
	}
}

// TestProtocolSessionEnvironment: the filtered environment is set before any
// prompt can run, on Open, Resume and every Prompt (OpenCode loses it on
// restart), and a failure to set it stops the prompt instead of letting it
// run with the server's inherited environment.
func TestProtocolSessionEnvironment(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	c := fakeServer(t, st)
	var envErr error
	c.shellEnv = func() ([]string, error) { return []string{"LOOM_MARK=1", "PATH=/bin"}, envErr }
	restart := func() { st.mu.Lock(); clear(st.envs); st.mu.Unlock() }

	ref, err := c.Open(ctx, loomharness.OpenSpec{Key: "agent-1", Dir: "/repo"})
	if err != nil || st.envs[ref.NativeID]["LOOM_MARK"] != "1" {
		t.Fatalf("Open did not set the session environment: %v", err)
	}
	s := c.Session(ref)
	restart()
	if _, err := s.Resume(ctx, loomharness.Launch{}, nil); err != nil || st.envs[ref.NativeID] == nil {
		t.Fatalf("Resume did not set the session environment: %v", err)
	}
	restart()
	if err := s.Prompt(ctx, loomharness.Input{Key: PromptID("agent-1", "r1"), Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if st.bareRuns != 0 {
		t.Fatal("a prompt ran with the server's inherited environment")
	}

	for name, fail := range map[string]func(){
		"PUT fails":     func() { st.envFail, envErr = true, nil },
		"env not built": func() { st.envFail, envErr = false, errors.New("bad presets") },
	} {
		fail()
		restart()
		n := len(st.messages[ref.NativeID])
		err := s.Prompt(ctx, loomharness.Input{Key: PromptID("agent-1", name), Text: "hi"})
		if err == nil || !strings.Contains(err.Error(), "session environment") {
			t.Fatalf("%s: Prompt = %v; want a session environment error", name, err)
		}
		if len(st.messages[ref.NativeID]) != n || st.bareRuns != 0 {
			t.Fatalf("%s: the prompt reached OpenCode without a session environment", name)
		}
		if _, err := s.Resume(ctx, loomharness.Launch{}, nil); err == nil {
			t.Fatalf("%s: Resume succeeded without a session environment", name)
		}
		if _, err := c.Open(ctx, loomharness.OpenSpec{Key: "agent-2-" + name, Dir: "/repo"}); err == nil {
			t.Fatalf("%s: Open succeeded without a session environment", name)
		}
	}
}

func TestProtocolResumeInstallsPermissions(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	c := fakeServer(t, st)
	perms := func(id string) any {
		st.mu.Lock()
		defer st.mu.Unlock()
		b, _ := json.Marshal(st.sessions[id]["permissions"])
		return string(b)
	}
	allow := []loomharness.PermissionRule{{Action: "bash", Resource: "*", Effect: "allow"}}
	deny := append(slices.Clone(allow), loomharness.PermissionRule{Action: "bash", Resource: "gh *", Effect: "deny"})
	ref, err := c.Open(ctx, loomharness.OpenSpec{Key: "agent-1", Dir: "/repo", Rules: allow})
	if err != nil {
		t.Fatal(err)
	}
	s := c.Session(ref)
	if _, err := s.Resume(ctx, loomharness.Launch{}, deny); err != nil {
		t.Fatal(err)
	}
	if got := perms(ref.NativeID); got != `[{"action":"shell","effect":"allow","resource":"*"},{"action":"shell","effect":"deny","resource":"gh *"}]` {
		t.Fatalf("Resume installed %s", got)
	}
	if _, err := s.Resume(ctx, loomharness.Launch{}, nil); err != nil || perms(ref.NativeID) != `[]` {
		t.Fatalf("Resume with no rules left %s, %v; want them replaced", perms(ref.NativeID), err)
	}
	if _, err := c.Open(ctx, loomharness.OpenSpec{Key: "agent-1", Dir: "/repo", Rules: deny}); err != nil ||
		!strings.Contains(perms(ref.NativeID).(string), `"gh *"`) {
		t.Fatalf("an Open repeat left %s, %v", perms(ref.NativeID), err)
	}

	st.patchErr = true
	n := len(st.messages[ref.NativeID])
	if _, err := s.Resume(ctx, loomharness.Launch{}, allow); err == nil || !strings.Contains(err.Error(), "install session permissions") {
		t.Fatalf("Resume with a failed install = %v", err)
	}
	if _, err := c.Open(ctx, loomharness.OpenSpec{Key: "agent-1", Dir: "/repo", Rules: allow}); err == nil {
		t.Fatal("an Open repeat succeeded with a failed install")
	}
	if !strings.Contains(perms(ref.NativeID).(string), `"gh *"`) || len(st.messages[ref.NativeID]) != n {
		t.Fatalf("a failed install changed the session: %s", perms(ref.NativeID))
	}
	if _, err := s.Resume(ctx, loomharness.Launch{}, []loomharness.PermissionRule{{Action: "webfetch", Resource: "*", Effect: "deny"}}); err == nil {
		t.Fatal("Resume accepted a rule with no OpenCode action")
	}
}

// TestProtocolPresetFailsClosed: a preset session needs a configured
// worktrees root, a directory under it, and the loom-<name> agent on the
// running service for that directory; otherwise Open and Resume refuse
// before anything runs, instead of running as OpenCode's default agent.
func TestProtocolPresetFailsClosed(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	c := fakeServer(t, st)
	defer func(d time.Duration) { agentWait = d }(agentWait)
	agentWait = 300 * time.Millisecond
	spec := loomharness.OpenSpec{Key: "agent-1", Dir: "/wt/repo dir/k", Preset: loomharness.PresetConfig{Name: "lead"}}
	refused := func(what, want string) {
		t.Helper()
		if _, err := c.Open(ctx, spec); !isCode(err, "bad_request") || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: Open = %v; want bad_request with %q", what, err, want)
		}
		if len(st.sessions) != 0 {
			t.Fatalf("%s: a refused preset created a session", what)
		}
	}
	refused("no root", "no Loom worktrees root")
	c.presets = "/wt"
	for _, dir := range []string{"/elsewhere/repo", "/wt", "/wtx/repo", "/wt/../etc", "wt/relative"} {
		spec.Dir = dir
		refused(dir, "not under the Loom worktrees root")
	}
	spec.Dir = "/wt/repo dir/k"
	refused("missing agent", "may disable project config")
	st.loading = true
	if _, err := c.Open(ctx, spec); !errors.Is(err, loomharness.ErrUnavailable) || len(st.sessions) != 0 {
		t.Fatalf("Open while the location loads = %v; want ErrUnavailable and no session", err)
	}
	st.loading = false
	st.agents = map[string]bool{"loom-lead": true}
	ref, err := c.Open(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if st.sessions[ref.NativeID]["agent"] != "loom-lead" || st.agentDir[len(st.agentDir)-1] != "/wt/repo dir/k" {
		t.Fatalf("session agent %v, looked up in %q", st.sessions[ref.NativeID]["agent"], st.agentDir)
	}
	s := c.Session(ref)
	if _, err := s.Resume(ctx, loomharness.Launch{}, nil); err != nil {
		t.Fatal(err)
	}
	// Loom dropped the preset: Resume refuses before installing anything.
	st.agents = nil
	n := len(st.messages[ref.NativeID])
	if _, err := s.Resume(ctx, loomharness.Launch{}, nil); !isCode(err, "bad_request") || len(st.messages[ref.NativeID]) != n {
		t.Fatalf("Resume of a session whose preset is gone = %v; want bad_request", err)
	}
}

// TestProtocolPromptReappliesRules: every Prompt installs the rules Loom last
// installed before the prompt runs, so a turn Loom starts never runs under
// rules someone else (or a boot-swept turn) left on the session row. A failed
// install sends nothing.
func TestProtocolPromptReappliesRules(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	c := fakeServer(t, st)
	deny := []loomharness.PermissionRule{{Action: "bash", Resource: "gh *", Effect: "deny"}}
	ref, err := c.Open(ctx, loomharness.OpenSpec{Key: "agent-1", Dir: "/repo", Rules: deny})
	if err != nil {
		t.Fatal(err)
	}
	perms := func() string {
		st.mu.Lock()
		defer st.mu.Unlock()
		b, _ := json.Marshal(st.sessions[ref.NativeID]["permissions"])
		return string(b)
	}
	want := perms()
	st.mu.Lock()
	st.sessions[ref.NativeID]["permissions"] = []map[string]string{{"action": "*", "resource": "*", "effect": "allow"}}
	st.mu.Unlock()
	s := c.Session(ref)
	if err := s.Prompt(ctx, loomharness.Input{Key: PromptID("agent-1", "r1"), Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if got := perms(); got != want {
		t.Fatalf("Prompt ran under %s; want Loom's %s", got, want)
	}

	// A client that never installed rules for the session refuses.
	fresh := NewClient(c.base, "pw")
	n := len(st.messages[ref.NativeID])
	if err := fresh.Session(ref).Prompt(ctx, loomharness.Input{Key: PromptID("agent-1", "r0"), Text: "hi"}); !isCode(err, "bad_request") || len(st.messages[ref.NativeID]) != n {
		t.Fatalf("Prompt with no installed rules = %v; want bad_request and nothing sent", err)
	}

	st.patchErr = true
	if err := s.Prompt(ctx, loomharness.Input{Key: PromptID("agent-1", "r2"), Text: "hi"}); err == nil {
		t.Fatal("Prompt succeeded with a failed rules install")
	}
	if len(st.messages[ref.NativeID]) != n {
		t.Fatal("the prompt reached OpenCode without Loom's rules")
	}
}

// TestProtocolOpenLeavesNothingOnError: when Open fails after creating the
// session (a create POST that errors after OpenCode made it, rules install
// or environment), it deletes the session and returns
// the zero ref with the error; when that delete fails too, it returns the
// session's ref with both errors, for the caller to record and Purge. A
// failed repeat Open never removes the session an earlier Open made.
func TestProtocolOpenLeavesNothingOnError(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	c := fakeServer(t, st)
	c.shellEnv = func() ([]string, error) { return []string{"PATH=/bin"}, nil }
	exists := func(id string) bool { st.mu.Lock(); defer st.mu.Unlock(); _, ok := st.sessions[id]; return ok }
	spec := loomharness.OpenSpec{Key: "agent-1", Launch: loomharness.Launch{Root: "/root-a"}, Dir: "/repo"}
	id := SessionID(spec.Key)

	for name, fail := range map[string]*bool{"create": &st.postErr, "install": &st.patchErr, "environment": &st.envFail} {
		*fail = true
		ref, err := c.Open(ctx, spec)
		if err == nil || ref != (loomharness.NativeRef{}) || exists(id) {
			t.Fatalf("%s: Open = %+v, %v, session left %v; want the zero ref, the error, no session", name, ref, err, exists(id))
		}
		if c.rootOf(id) != "" {
			t.Fatalf("%s: the failed session's Root is still recorded", name)
		}
		if err := c.Session(loomharness.NativeRef{NativeID: id}).Prompt(ctx, loomharness.Input{Key: "msg_x", Text: "x"}); !isCode(err, "bad_request") {
			t.Fatalf("%s: Prompt after a failed Open = %v; want bad_request (no rules)", name, err)
		}

		st.delErr = true
		ref, err = c.Open(ctx, spec)
		if err == nil || !strings.Contains(err.Error(), "disk full") || ref.NativeID != id || ref.Root != "/root-a" || !exists(id) {
			t.Fatalf("%s: Open with a failed delete = %+v, %v; want the session's ref and both errors", name, ref, err)
		}
		st.delErr, *fail = false, false
		if err := c.Purge(ctx, []loomharness.NativeRef{ref}); err != nil || exists(id) {
			t.Fatalf("%s: Purge of the returned ref: %v", name, err)
		}
	}

	ref, err := c.Open(ctx, spec)
	if err != nil || ref.NativeID != id {
		t.Fatalf("Open after the failures = %+v, %v", ref, err)
	}
	st.patchErr = true
	if again, err := c.Open(ctx, spec); err == nil || again != (loomharness.NativeRef{}) || !exists(id) {
		t.Fatalf("a failed repeat Open = %+v, %v, session kept %v; want the zero ref and the earlier session kept", again, err, exists(id))
	}
}

// openRace replays codex's interleavings (verdicts 8d783c86, 9f81cf39): two
// Opens for one key. pair holds the first session GET until a second arrives
// (or 300ms pass), so without the per-id lock both find no session.
type openRace struct {
	once     sync.Once
	both     chan struct{}
	mu       sync.Mutex
	gets     int
	posts    int // guarded by store.mu
	failPost int // this create commits the session, then answers 504
}

func (r *openRace) pair() {
	r.once.Do(func() { r.both = make(chan struct{}) })
	r.mu.Lock()
	r.gets++
	n := r.gets
	r.mu.Unlock()
	if n == 2 {
		close(r.both)
	}
	if n == 1 {
		select {
		case <-r.both:
		case <-time.After(300 * time.Millisecond):
		}
	}
}

type openResult struct {
	ref loomharness.NativeRef
	err error
}

func openPair(c *Client, spec loomharness.OpenSpec) []openResult {
	out := make(chan openResult, 2)
	for range 2 {
		go func() { ref, err := c.Open(context.Background(), spec); out <- openResult{ref, err} }()
	}
	return []openResult{<-out, <-out}
}

// TestProtocolConcurrentOpenSameRef: two concurrent Opens of one key both
// return its ref, and only the first creates it (a repeat sends no POST).
func TestProtocolConcurrentOpenSameRef(t *testing.T) {
	st := newStore()
	c := fakeServer(t, st)
	st.race = &openRace{}
	spec := loomharness.OpenSpec{Key: "agent-1", Dir: "/repo"}
	for _, r := range openPair(c, spec) {
		if r.err != nil || r.ref.NativeID != SessionID(spec.Key) {
			t.Fatalf("Open = %+v, %v; want the session's ref", r.ref, r.err)
		}
	}
	if st.race.posts != 1 {
		t.Fatalf("%d creates; want 1", st.race.posts)
	}
}

// TestProtocolConcurrentOpenKeepsSession: when the first of two concurrent
// Opens has its create committed and then answered 504, its cleanup never
// deletes the session the other Open returns.
func TestProtocolConcurrentOpenKeepsSession(t *testing.T) {
	st := newStore()
	c := fakeServer(t, st)
	st.race = &openRace{failPost: 1}
	spec := loomharness.OpenSpec{Key: "agent-1", Dir: "/repo"}
	var ok, failed int
	for _, r := range openPair(c, spec) {
		switch {
		case r.err == nil && r.ref.NativeID == SessionID(spec.Key):
			ok++
		case r.err != nil && r.ref == (loomharness.NativeRef{}):
			failed++
		default:
			t.Errorf("Open = %+v, %v", r.ref, r.err)
		}
	}
	st.mu.Lock()
	_, kept := st.sessions[SessionID(spec.Key)]
	st.mu.Unlock()
	if ok != 1 || failed != 1 || !kept {
		t.Fatalf("%d Opens succeeded, %d failed, session kept %v; want 1, 1, true", ok, failed, kept)
	}
}

// pauseRT holds the first request hit matches (after its response when
// after is set, else instead of sending it, answering 500) until release.
type pauseRT struct {
	next    http.RoundTripper
	hit     func(*http.Request) bool
	after   bool
	once    sync.Once
	paused  chan struct{}
	release chan struct{}
}

func (p *pauseRT) RoundTrip(r *http.Request) (*http.Response, error) {
	mine := false
	if p.hit(r) {
		p.once.Do(func() { mine = true })
	}
	if !mine {
		return p.next.RoundTrip(r)
	}
	if p.after {
		resp, err := p.next.RoundTrip(r)
		close(p.paused)
		<-p.release
		return resp, err
	}
	close(p.paused)
	<-p.release
	return &http.Response{StatusCode: 500, Header: http.Header{}, Request: r,
		Body: io.NopCloser(strings.NewReader(`{"_tag":"UnknownError","message":"boom"}`))}, nil
}

func pause(c *Client, after bool, hit func(*http.Request) bool) *pauseRT {
	p := &pauseRT{next: http.DefaultTransport, hit: hit, after: after, paused: make(chan struct{}), release: make(chan struct{})}
	c.http.Transport = p
	return p
}

// notDone fails if done is ready within 100ms: the call should be waiting.
func notDone[T any](t *testing.T, what string, done chan T) {
	t.Helper()
	select {
	case <-done:
		t.Fatalf("%s ran while Open held the session id", what)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestProtocolPurgeWaitsForOpen (codex 9f81cf39 #1): a Purge of a stale ref
// does not run while an Open of the same id is between its rules install
// and its return, so Open never reports a session Purge removed under it.
func TestProtocolPurgeWaitsForOpen(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	c := fakeServer(t, st)
	spec := loomharness.OpenSpec{Key: "agent-1", Dir: "/repo"}
	stale, err := c.Open(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Purge(ctx, []loomharness.NativeRef{stale}); err != nil {
		t.Fatal(err)
	}
	p := pause(c, true, func(r *http.Request) bool { return r.Method == "PATCH" })
	opened := make(chan openResult, 1)
	go func() { ref, err := c.Open(ctx, spec); opened <- openResult{ref, err} }()
	<-p.paused
	purged := make(chan error, 1)
	go func() { purged <- c.Purge(ctx, []loomharness.NativeRef{stale}) }()
	notDone(t, "Purge", purged)
	close(p.release)
	if r := <-opened; r.err != nil {
		t.Fatalf("Open = %v", r.err)
	}
	if err := <-purged; err != nil {
		t.Fatal(err)
	}
}

// TestProtocolResumeWaitsForOpenCleanup (codex 9f81cf39 #2): a Resume of
// the id a failing Open created waits for Open's cleanup, so it never
// returns a session that cleanup then deletes.
func TestProtocolResumeWaitsForOpenCleanup(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	c := fakeServer(t, st)
	c.shellEnv = func() ([]string, error) { return []string{"PATH=/bin"}, nil }
	spec := loomharness.OpenSpec{Key: "agent-1", Dir: "/repo"}
	ref := loomharness.NativeRef{NativeID: SessionID(spec.Key)}
	p := pause(c, false, func(r *http.Request) bool { return r.Method == "PUT" })
	opened := make(chan openResult, 1)
	go func() { ref, err := c.Open(ctx, spec); opened <- openResult{ref, err} }()
	<-p.paused
	resumed := make(chan openResult, 1)
	go func() {
		ref, err := c.Session(ref).Resume(ctx, loomharness.Launch{}, nil)
		resumed <- openResult{ref, err}
	}()
	notDone(t, "Resume", resumed)
	close(p.release)
	if r := <-opened; r.err == nil {
		t.Fatal("Open succeeded with a failed environment")
	}
	r := <-resumed
	st.mu.Lock()
	_, exists := st.sessions[ref.NativeID]
	st.mu.Unlock()
	if r.err == nil && !exists {
		t.Fatal("Resume returned a session Open's cleanup then deleted")
	}
}

// TestProtocolIDLocksFreed (codex 9f81cf39 #3): the per-id lock entries go
// once no Open, Resume or Purge holds or waits on them.
func TestProtocolIDLocksFreed(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	c := fakeServer(t, st)
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ref, err := c.Open(ctx, loomharness.OpenSpec{Key: fmt.Sprintf("k%d", i%10), Dir: "/repo"})
			if err == nil {
				_, _ = c.Session(ref).Resume(ctx, loomharness.Launch{}, nil)
				_ = c.Purge(ctx, []loomharness.NativeRef{ref})
			}
		}()
	}
	wg.Wait()
	c.idsMu.Lock()
	n := len(c.ids)
	c.idsMu.Unlock()
	if n != 0 {
		t.Fatalf("%d per-id lock entries left; want 0", n)
	}
}

// permReq is a pending OpenCode permission request (schema/src/permission.ts).
type permReq struct {
	Session   string   `json:"sessionID"`
	Action    string   `json:"action"`
	Resources []string `json:"resources"`
	Save      []string `json:"save,omitempty"`
	Metadata  struct {
		Files []fileDiff `json:"files,omitempty"`
	} `json:"metadata"`
}

// effect is what OpenCode decides for action on resource under the session's
// stored rules: the last matching rule wins, "*" matches anything, and no
// match asks (core/src/permission.ts:87-97).
func (st *store) effect(sid, action, resource string) string {
	st.mu.Lock()
	defer st.mu.Unlock()
	b, _ := json.Marshal(st.sessions[sid]["permissions"])
	var rules []map[string]string
	_ = json.Unmarshal(b, &rules)
	match := func(pattern, s string) bool {
		re := "^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$"
		ok, _ := regexp.MatchString(re, s)
		return ok
	}
	for i := len(rules) - 1; i >= 0; i-- {
		if match(rules[i]["action"], action) && match(rules[i]["resource"], resource) {
			return rules[i]["effect"]
		}
	}
	return "ask"
}

// TestProtocolReplyAlwaysIsSessionScoped: an allowed Always reply answers
// the ask "once" (never OpenCode's project-wide "always") and adds a grant
// for the ask's save patterns to that session's rules only. A later matching
// request in the session is allowed, Loom's deny rules still win, another
// session is unaffected, every Prompt keeps the grant and a Resume of the
// quarantined session ends it.
// Always on a question or on an ask with no save patterns is an explicit
// error and leaves the ask open, as does a failed grant install (with its
// restore failing too, the session then refuses prompts until Resume).
func TestProtocolReplyAlwaysIsSessionScoped(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	c := fakeServer(t, st)
	ask := []loomharness.PermissionRule{{Action: "bash", Resource: "*", Effect: "ask"}}
	deny := append(slices.Clone(ask), loomharness.PermissionRule{Action: "bash", Resource: "git push*", Effect: "deny"})
	a, err := c.Open(ctx, loomharness.OpenSpec{Key: "agent-a", Dir: "/repo", Rules: deny})
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Open(ctx, loomharness.OpenSpec{Key: "agent-b", Dir: "/repo", Rules: ask})
	if err != nil {
		t.Fatal(err)
	}
	sa := c.Session(a)
	always := loomharness.Reply{Allow: true, Always: true}
	st.perms["per_1"] = permReq{Session: a.NativeID, Action: "shell", Resources: []string{"git status"}, Save: []string{"git *"}}
	if err := sa.Reply(ctx, "per_1", always); err != nil {
		t.Fatal(err)
	}
	if st.replies["per_1"] != "once" {
		t.Fatalf("decision sent %q; want once (OpenCode's always is project-wide)", st.replies["per_1"])
	}
	check := func(when string, sid, resource, want string) {
		t.Helper()
		if got := st.effect(sid, "shell", resource); got != want {
			t.Fatalf("%s: %s in %s = %s; want %s", when, resource, sid, got, want)
		}
	}
	check("after Always", a.NativeID, "git log", "allow")
	check("after Always", a.NativeID, "git push origin", "deny")
	check("after Always", a.NativeID, "rm -rf x", "ask")
	check("after Always", b.NativeID, "git log", "ask")

	if err := sa.Prompt(ctx, loomharness.Input{Key: PromptID("agent-a", "r1"), Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	check("after Prompt", a.NativeID, "git log", "allow")
	check("after Prompt", a.NativeID, "git push origin", "deny")

	refused := func(what, id string, r loomharness.Reply) {
		t.Helper()
		if err := sa.Reply(ctx, id, r); !isCode(err, "bad_request") {
			t.Fatalf("%s: Reply = %v; want bad_request", what, err)
		}
		if _, sent := st.replies[id]; sent {
			t.Fatalf("%s: the ask was answered", what)
		}
	}
	st.perms["per_2"] = permReq{Session: a.NativeID, Action: "shell", Resources: []string{"make"}}
	refused("no save patterns", "per_2", always)
	refused("question", "frm_1", loomharness.Reply{Allow: true, Always: true, Answer: "blue"})
	st.patchErr = true
	st.perms["per_3"] = permReq{Session: a.NativeID, Action: "shell", Resources: []string{"ls"}, Save: []string{"ls *"}}
	if err := sa.Reply(ctx, "per_3", always); err == nil {
		t.Fatal("Reply succeeded with a failed grant install")
	}
	if _, sent := st.replies["per_3"]; sent {
		t.Fatal("the ask was answered though its grant failed")
	}
	st.patchErr = false
	// Every PATCH failed, the restore too, so the session fails closed.
	if err := sa.Prompt(ctx, loomharness.Input{Key: PromptID("agent-a", "r2"), Text: "hi"}); !errors.Is(err, loomharness.ErrQuarantined) {
		t.Fatalf("Prompt after a failed grant and restore = %v; want ErrQuarantined", err)
	}

	if _, err := sa.Resume(ctx, loomharness.Launch{}, deny); err != nil {
		t.Fatal(err)
	}
	check("after Resume", a.NativeID, "git log", "ask")
}

// TestProtocolAlwaysGrantOutlivesTheTurn: Loom resumes the session before
// every hand-over (each new message), so an Always grant stays through a
// Resume that installs the same rules, and only a Resume under a changed
// policy drops it.
func TestProtocolAlwaysGrantOutlivesTheTurn(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	c := fakeServer(t, st)
	rules := []loomharness.PermissionRule{{Action: "bash", Resource: "*", Effect: "ask"}}
	a, err := c.Open(ctx, loomharness.OpenSpec{Key: "agent-a", Dir: "/repo", Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	s := c.Session(a)
	st.perms["per_1"] = permReq{Session: a.NativeID, Action: "shell", Resources: []string{"wc -l a"}, Save: []string{"wc *"}}
	if err := s.Reply(ctx, "per_1", loomharness.Reply{Allow: true, Always: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(ctx, loomharness.Launch{}, rules); err != nil {
		t.Fatal(err)
	}
	if got := st.effect(a.NativeID, "shell", "wc -l b"); got != "allow" {
		t.Fatalf("after a Resume with the same rules: wc -l b = %s; want allow (the grant lasts the session)", got)
	}
	changed := append(slices.Clone(rules), loomharness.PermissionRule{Action: "bash", Resource: "rm *", Effect: "deny"})
	if _, err := s.Resume(ctx, loomharness.Launch{}, changed); err != nil {
		t.Fatal(err)
	}
	if got := st.effect(a.NativeID, "shell", "wc -l b"); got != "ask" {
		t.Fatalf("after a Resume with changed rules: wc -l b = %s; want ask (a new policy drops the grant)", got)
	}
}

// TestProtocolAlwaysGrantUnderADefaultDeny: pr-review-interactive's rules
// start with a catch-all deny that the later allow and ask rules override.
// An Always grant beats the ask, and only deny rules after an allow or ask
// rule (explicit denies) are applied again after it: the leading default
// deny is not, or it would deny every action and OpenCode would drop the
// shell tool.
func TestProtocolAlwaysGrantUnderADefaultDeny(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	c := fakeServer(t, st)
	rules := []loomharness.PermissionRule{
		{Action: "*", Resource: "*", Effect: "deny"},
		{Action: "read", Resource: "*", Effect: "allow"},
		{Action: "bash", Resource: "*", Effect: "ask"},
		{Action: "bash", Resource: "wc -c*", Effect: "deny"},
	}
	a, err := c.Open(ctx, loomharness.OpenSpec{Key: "agent-a", Dir: "/repo", Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	s := c.Session(a)
	st.perms["per_1"] = permReq{Session: a.NativeID, Action: "shell", Resources: []string{"wc -l a"}, Save: []string{"wc *"}}
	if err := s.Reply(ctx, "per_1", loomharness.Reply{Allow: true, Always: true}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ action, resource, want string }{
		{"shell", "wc -l b", "allow"}, // the grant
		{"shell", "wc -c b", "deny"},  // an explicit deny still wins
		{"shell", "rm x", "ask"},      // the rest of shell still asks
		{"read", "README.md", "allow"},
		{"edit", "README.md", "deny"}, // the default deny still covers what nothing allows
	} {
		if got := st.effect(a.NativeID, c.action, c.resource); got != c.want {
			t.Errorf("%s %s after Always = %s; want %s", c.action, c.resource, got, c.want)
		}
	}
}

// TestProtocolAlwaysGrantKeepsTheSubagentDeny (SA1): a lead's policy ends
// with a subagent deny after its allow rules, an explicit deny, so it is
// applied again after the session's Always grants: no grant, even one on
// the subagent action itself, re-enables OpenCode's subagent tool, before
// or after a Resume that keeps the grants.
func TestProtocolAlwaysGrantKeepsTheSubagentDeny(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	c := fakeServer(t, st)
	rules := []loomharness.PermissionRule{
		{Action: "read", Resource: "*", Effect: "allow"},
		{Action: "edit", Resource: "*", Effect: "allow"},
		{Action: "bash", Resource: "*", Effect: "allow"},
		{Action: "subagent", Resource: "*", Effect: "deny"},
	}
	a, err := c.Open(ctx, loomharness.OpenSpec{Key: "agent-a", Dir: "/repo", Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	s := c.Session(a)
	st.perms["per_1"] = permReq{Session: a.NativeID, Action: "external_directory", Resources: []string{"/tmp/x"}, Save: []string{"/tmp/*"}}
	st.perms["per_2"] = permReq{Session: a.NativeID, Action: "subagent", Resources: []string{"general"}, Save: []string{"*"}}
	for _, id := range []string{"per_1", "per_2"} {
		if err := s.Reply(ctx, id, loomharness.Reply{Allow: true, Always: true}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(when string) {
		t.Helper()
		if got := st.effect(a.NativeID, "external_directory", "/tmp/y"); got != "allow" {
			t.Fatalf("%s: external_directory /tmp/y = %s; want allow (the grant holds)", when, got)
		}
		for _, agent := range []string{"general", "explore"} {
			if got := st.effect(a.NativeID, "subagent", agent); got != "deny" {
				t.Fatalf("%s: subagent %s = %s; want deny (the lead's deny beats every grant)", when, agent, got)
			}
		}
	}
	check("after Always")
	if _, err := s.Resume(ctx, loomharness.Launch{}, rules); err != nil {
		t.Fatal(err)
	}
	check("after a Resume with the same rules")
}

// TestProtocolAlwaysGrantRollsBack (codex, 11588cd1e and 863aa26b5): a
// grant PATCH that commits and then answers 500 leaves an unknown outcome.
// When Reply can put the rules back, the ask stays open and asks again.
// When it cannot, the session is quarantined: its active turn is stopped
// (or the error says the stop is unconfirmed), Prompt and Reply refuse it,
// a failed reinstall keeps it blocked, a fresh client (a Loom restart)
// refuses it too, and a successful Resume restores normal asking.
func TestProtocolAlwaysGrantRollsBack(t *testing.T) {
	ctx := context.Background()
	rules := []loomharness.PermissionRule{{Action: "bash", Resource: "*", Effect: "ask"}}
	always := loomharness.Reply{Allow: true, Always: true}
	setup := func(t *testing.T) (*store, *Client, *Session) {
		st := newStore()
		c := fakeServer(t, st)
		ref, err := c.Open(ctx, loomharness.OpenSpec{Key: "agent-a", Dir: "/repo", Rules: rules})
		if err != nil {
			t.Fatal(err)
		}
		st.perms["per_1"] = permReq{Session: ref.NativeID, Action: "shell", Resources: []string{"ls"}, Save: []string{"ls *"}}
		return st, c, c.Session(ref)
	}
	refused := func(t *testing.T, st *store, s *Session, want string) {
		t.Helper()
		if err := s.Prompt(ctx, loomharness.Input{Key: PromptID("agent-a", "p"), Text: "hi"}); !errors.Is(err, loomharness.ErrQuarantined) || !strings.Contains(err.Error(), want) {
			t.Fatalf("Prompt = %v; want ErrQuarantined containing %q", err, want)
		}
		if err := s.Reply(ctx, "per_1", loomharness.Reply{}); !errors.Is(err, loomharness.ErrQuarantined) || !strings.Contains(err.Error(), want) {
			t.Fatalf("Reply (deny) = %v; want ErrQuarantined containing %q", err, want)
		}
		if _, sent := st.replies["per_1"]; sent {
			t.Fatal("a reply reached the ask of a quarantined session")
		}
	}
	works := func(t *testing.T, st *store, s *Session) {
		t.Helper()
		if got := st.effect(s.ref.NativeID, "shell", "ls x"); got != "ask" {
			t.Fatalf("ls x = %s; want ask", got)
		}
		if err := s.Prompt(ctx, loomharness.Input{Key: PromptID("agent-a", "ok"), Text: "hi"}); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if err := s.Reply(ctx, "per_1", loomharness.Reply{}); err != nil || st.replies["per_1"] != "reject" {
			t.Fatalf("Reply (deny) = %v, sent %q", err, st.replies["per_1"])
		}
	}

	t.Run("RestoreSucceeds", func(t *testing.T) {
		st, _, s := setup(t)
		st.patchLie = 1
		if err := s.Reply(ctx, "per_1", always); err == nil {
			t.Fatal("Reply succeeded with a failed grant install")
		}
		if _, sent := st.replies["per_1"]; sent || st.stops != 0 {
			t.Fatalf("answered %v, stops %d; want the ask open and no stop", sent, st.stops)
		}
		if err := s.Reply(ctx, "per_1", loomharness.Reply{}); errors.Is(err, loomharness.ErrQuarantined) {
			t.Fatalf("a restored session reports ErrQuarantined: %v", err)
		}
		delete(st.replies, "per_1")
		st.perms["per_1"] = permReq{Session: s.ref.NativeID, Action: "shell", Resources: []string{"ls"}, Save: []string{"ls *"}}
		works(t, st, s)
	})

	t.Run("RestoreFails", func(t *testing.T) {
		st, c, s := setup(t)
		st.patchLie = 2
		err := s.Reply(ctx, "per_1", always)
		if !errors.Is(err, loomharness.ErrQuarantined) || !strings.Contains(err.Error(), "Always grant on session") || !strings.Contains(err.Error(), "active turn was stopped") {
			t.Fatalf("Reply = %v; want ErrQuarantined naming the unconfirmed grant and the stop", err)
		}
		if st.stops != 1 {
			t.Fatalf("%d stops; want the active turn stopped once", st.stops)
		}
		refused(t, st, s, "quarantined")
		if fresh := NewClient(c.base, "pw"); fresh.Session(s.ref).Reply(ctx, "per_1", loomharness.Reply{}) == nil {
			t.Fatal("a fresh client (a Loom restart) replied on the session")
		}
		st.patchErr = true
		if _, err := s.Resume(ctx, loomharness.Launch{}, rules); err == nil {
			t.Fatal("Resume succeeded with a failed reinstall")
		}
		st.patchErr = false
		refused(t, st, s, "quarantined")
		if _, err := s.Resume(ctx, loomharness.Launch{}, rules); err != nil {
			t.Fatal(err)
		}
		works(t, st, s)
	})

	// Codex (8a80643d3): a restore that runs out its deadline must not
	// spend the stop's; exactly one interrupt is still sent.
	t.Run("RestoreTimesOut", func(t *testing.T) {
		defer func(w time.Duration) { cleanupWait = w }(cleanupWait)
		cleanupWait = 300 * time.Millisecond
		st, _, s := setup(t)
		st.patchLie, st.hangLie = 2, true
		err := s.Reply(ctx, "per_1", always)
		if !errors.Is(err, loomharness.ErrQuarantined) || !strings.Contains(err.Error(), "active turn was stopped") {
			t.Fatalf("Reply = %v; want ErrQuarantined and the stop attempted and confirmed", err)
		}
		st.mu.Lock()
		stops := st.stops
		st.mu.Unlock()
		if stops != 1 {
			t.Fatalf("%d interrupts after the restore timed out; want 1", stops)
		}
		refused(t, st, s, "quarantined")
	})

	for name, set := range map[string]func(*store){
		"StopFails": func(st *store) { st.stopErr = true },
		"StopHangs": func(st *store) { st.stopHang = true },
	} {
		t.Run(name, func(t *testing.T) {
			defer func(w time.Duration) { cleanupWait = w }(cleanupWait)
			cleanupWait = 300 * time.Millisecond
			st, _, s := setup(t)
			set(st)
			st.patchLie = 2
			err := s.Reply(ctx, "per_1", always)
			if !errors.Is(err, loomharness.ErrQuarantined) || !strings.Contains(err.Error(), "native stop unconfirmed") || strings.Contains(err.Error(), "turn was stopped") {
				t.Fatalf("Reply = %v; want ErrQuarantined with native stop unconfirmed", err)
			}
			refused(t, st, s, "quarantined")
		})
	}
}

// TestProtocolPromptRacesQuarantine (codex, cc46d517a): a Prompt paused
// after its check and rules PATCH, while a Reply.Always quarantines the
// session (grant and restore both commit, then answer 500), never sends its
// turn after the quarantine stopped the session. The Reply waits for the
// session's lock (lockWaitHook says so) until the Prompt's POST is done.
func TestProtocolPromptRacesQuarantine(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	c := fakeServer(t, st)
	rules := []loomharness.PermissionRule{{Action: "bash", Resource: "*", Effect: "ask"}}
	ref, err := c.Open(ctx, loomharness.OpenSpec{Key: "agent-a", Dir: "/repo", Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	s := c.Session(ref)
	st.perms["per_1"] = permReq{Session: ref.NativeID, Action: "shell", Resources: []string{"ls"}, Save: []string{"ls *"}}
	waiting := make(chan struct{}, 1)
	lockWaitHook = func(string) { waiting <- struct{}{} }
	defer func() { lockWaitHook = nil }()

	p := pause(c, true, func(r *http.Request) bool { return r.Method == "PATCH" })
	prompted := make(chan error, 1)
	go func() { prompted <- s.Prompt(ctx, loomharness.Input{Key: PromptID("agent-a", "r1"), Text: "hi"}) }()
	<-p.paused
	st.mu.Lock()
	st.patchLie = 2
	st.mu.Unlock()
	replied := make(chan error, 1)
	go func() { replied <- s.Reply(ctx, "per_1", loomharness.Reply{Allow: true, Always: true}) }()
	select {
	case <-waiting: // the Reply waits for the Prompt
	case <-replied: // it did not: the session is quarantined under the Prompt
	}
	close(p.release)
	promptErr := <-prompted
	st.mu.Lock()
	late := st.lateRuns
	st.mu.Unlock()
	if late != 0 {
		t.Fatalf("a turn was sent after the quarantine stopped the session (Prompt = %v)", promptErr)
	}
	if promptErr != nil {
		t.Fatalf("Prompt before the quarantine = %v", promptErr)
	}
	if err := <-replied; err == nil || !strings.Contains(err.Error(), "active turn was stopped") {
		t.Fatalf("Reply = %v; want the session quarantined and stopped", err)
	}
	if err := s.Prompt(ctx, loomharness.Input{Key: PromptID("agent-a", "r2"), Text: "hi"}); err == nil || !strings.Contains(err.Error(), "quarantined") {
		t.Fatalf("Prompt after the quarantine = %v; want quarantined", err)
	}
}

// TestProtocolAsksCarryWhatTheyAsk: a pending permission's ask.opened says
// what it asks about (its action and resources, then any patch) and a
// pending form's carries its fields as questions; a Reply with Answers
// sends each field its value by key, an option's label as its value and a
// multiselect as a list.
func TestProtocolAsksCarryWhatTheyAsk(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	c := fakeServer(t, st)
	ref, _ := c.Open(ctx, loomharness.OpenSpec{Key: "agent-1", Launch: loomharness.Launch{Root: "/root"}, Dir: "/repo"})
	p := permReq{Session: ref.NativeID, Action: "edit", Resources: []string{"a.go"}}
	p.Metadata.Files = []fileDiff{{File: "a.go", Patch: "-a\n+b"}}
	st.perms["per_1"] = p
	f := form{ID: "frm_1", SessionID: ref.NativeID, Title: "Questions", Fields: []formField{
		{Key: "q0", Type: "string", Title: "Color", Description: "Which color?"},
		{Key: "q1", Type: "multiselect", Title: "Sizes", Description: "Which sizes?"},
	}}
	f.Fields[0].Options = append(f.Fields[0].Options, struct {
		Value       string `json:"value"`
		Label       string `json:"label"`
		Description string `json:"description"`
	}{"red", "Red", "warm"})
	st.forms = map[string]form{"frm_1": f}
	st.asks = map[string][]string{ref.NativeID: {"per_1", "frm_1"}}
	page, err := c.Session(ref).Messages(ctx, "", 0)
	if err != nil || len(page.Events) != 2 {
		t.Fatalf("Messages = %+v, %v", page, err)
	}
	if e := page.Events[0]; e.AskID != "per_1" || e.Text != "edit a.go\n-a\n+b" {
		t.Fatalf("permission ask = %+v", e)
	}
	want := []loomharness.Question{
		{ID: "q0", Header: "Color", Question: "Which color?", Options: []loomharness.Choice{{Label: "Red", Description: "warm"}}},
		{ID: "q1", Header: "Sizes", Question: "Which sizes?", MultiSelect: true},
	}
	if e := page.Events[1]; e.ItemKind != "question" || e.Text != "Which color?" || !reflect.DeepEqual(e.Questions, want) {
		t.Fatalf("form ask = %+v", e)
	}
	if err := c.Session(ref).Reply(ctx, "frm_1", loomharness.Reply{Answers: map[string][]string{"q0": {"Red"}, "q1": {"S", "M"}}}); err != nil {
		t.Fatal(err)
	}
	if got := st.answers["frm_1"]; !reflect.DeepEqual(got, map[string]any{"q0": "red", "q1": []any{"S", "M"}}) {
		t.Fatalf("answer sent = %v", got)
	}
}
