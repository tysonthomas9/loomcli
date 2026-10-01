package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
	agents   map[string]bool // agent ids the service offers
	agentDir []string        // location[directory] of each agent lookup
	loading  bool            // the location lists no agents yet
}

func newStore() *store {
	return &store{sessions: map[string]map[string]any{}, messages: map[string][]map[string]any{}, active: map[string]string{},
		envs: map[string]map[string]string{}}
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
