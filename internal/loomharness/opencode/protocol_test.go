package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

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
}

func newStore() *store {
	return &store{sessions: map[string]map[string]any{}, messages: map[string][]map[string]any{}, active: map[string]string{}}
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
		if _, ok := st.sessions[id]; ok {
			reply(w, 409, map[string]string{"_tag": "ConflictError", "message": "exists"})
			return
		}
		st.sessions[id] = body
		reply(w, 200, map[string]any{"data": body})
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
		st.mu.Lock()
		defer st.mu.Unlock()
		if s, ok := st.sessions[r.PathValue("id")]; ok {
			reply(w, 200, map[string]any{"data": s})
			return
		}
		missing(w, r.PathValue("id"))
	})
	mux.HandleFunc("DELETE /api/session/{id}", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		id := r.PathValue("id")
		st.deleted = append(st.deleted, id)
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
		m := map[string]any{"id": body["id"], "type": "user", "text": body["text"], "time": map[string]int64{"created": 1}}
		st.messages[id] = append(st.messages[id], m)
		reply(w, 200, map[string]any{"data": m})
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
	// The message list pages by index; the cursor is the next index.
	mux.HandleFunc("GET /api/session/{id}/message", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		all := st.messages[r.PathValue("id")]
		from, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
		to := len(all)
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && from+n < to {
			to = from + n
		}
		reply(w, 200, map[string]any{"data": all[from:to], "cursor": map[string]string{"next": strconv.Itoa(to)}})
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
	c := fakeServer(t, st)
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

	if got, err := s.Resume(ctx, loomharness.Launch{Root: "/root"}); err != nil || got != ref {
		t.Fatalf("Resume = %+v, %v", got, err)
	}
	gone := c.Session(loomharness.NativeRef{NativeID: SessionID("agent-gone")})
	if _, err := gone.Resume(ctx, loomharness.Launch{}); !errors.Is(err, loomharness.ErrSessionNotFound) {
		t.Fatalf("Resume of a missing session = %v", err)
	}
}

func TestProtocolMessagesPagesAndMapsItems(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	c := fakeServer(t, st)
	ref, _ := c.Open(ctx, loomharness.OpenSpec{Key: "agent-1", Dir: "/repo"})
	st.messages[ref.NativeID] = []map[string]any{
		{"id": "msg_u1", "type": "user", "text": "hi", "time": map[string]int64{"created": 1}},
		{"id": "msg_a1", "type": "assistant", "time": map[string]int64{"created": 2}, "content": []map[string]any{
			{"type": "reasoning", "text": "think"},
			{"type": "tool", "id": "call_1", "name": "shell"},
			{"type": "text", "text": "done"},
		}},
		{"id": "msg_s1", "type": "synthetic", "text": "The server restarted", "metadata": map[string]string{"notice": "restart"}},
		{"id": "msg_i1", "type": "idle", "outcome": "interrupted"},
	}
	s := c.Session(ref)
	p1, err := s.Messages(ctx, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if p1.Next == "" || len(p1.Events) != 4 {
		t.Fatalf("page 1 = %+v", p1)
	}
	p2, err := s.Messages(ctx, p1.Next, 2)
	if err != nil {
		t.Fatal(err)
	}
	if p3, _ := s.Messages(ctx, p2.Next, 2); len(p3.Events) != 0 || p3.Next != "" {
		t.Fatalf("page 3 = %+v", p3)
	}
	var got []string
	for _, e := range append(p1.Events, p2.Events...) {
		got = append(got, string(e.Type)+" "+e.ItemKind+" "+e.ItemID+" "+e.InputKey+" "+e.StopReason)
	}
	want := []string{
		"message.delivered message msg_u1 msg_u1 ",
		"item.completed reasoning msg_a1/reasoning/0  ",
		"item.completed tool msg_a1/tool/call_1  ",
		"item.completed message msg_a1/text/0  ",
		"turn.resumed  msg_s1  ",
		"turn.completed  msg_i1  cancelled",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
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
	})
	if err != nil {
		t.Fatal(err)
	}
	var flat []string
	for _, r := range got {
		flat = append(flat, r["action"]+":"+r["resource"]+":"+r["effect"])
	}
	want := []string{"*:*:deny", "read:*:allow", "grep:*:allow", "glob:*:allow", "shell:gh *:deny", "edit:*:ask"}
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
