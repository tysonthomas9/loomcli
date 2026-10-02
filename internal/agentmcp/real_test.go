package agentmcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/serve/agentwire"
	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomagent/client"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/testutil/realloom"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentsv1"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// TestRealOpenCodeLeadCreatesChildren runs serve's Agent API wiring
// (agentwire) in process on the real OpenCode build, with a fake model and a
// freshly built `loom agent mcp-bridge`; see leadCreatesChildren.
// LOOM_REAL_OPENCODE=1 runs it.
func TestRealOpenCodeLeadCreatesChildren(t *testing.T) {
	realloom.Skip(t)
	model := newLeadModel(t)
	sbx := realloom.NewSandbox(t, model.URL)
	loom := sbx.BuildLoom(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	api, err := agentwire.Start(ctx, agentwire.Config{Dir: filepath.Join(sbx.Dir, "loom"), OpenCodeBin: realloom.OpenCodeBin(),
		OpenCodeEnv: sbx.Env(), APIBase: "http://" + l.Addr().String(), LoomBin: loom,
		GitHubRead: func(context.Context, string, string, string, string, map[string]any) (map[string]any, error) {
			return nil, errors.New("this test has no GitHub")
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Stop)
	mux := http.NewServeMux()
	api.Register(mux, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(middleware.WithWorkspace(r.Context(), r.PathValue("ws"))))
		})
	}, nil)
	srv := httptest.NewUnstartedServer(mux)
	srv.Listener.Close()
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	leadCreatesChildren(ctx, t, client.New(client.Config{BaseURL: srv.URL, Workspace: "ws"}), sbx, model)
}

// TestRealServeLeadCreatesChildren is the same on a real `loom serve`
// process with an owned fleet-db: creating a lead works, and its bridge
// reaches serve. LOOM_REAL_OPENCODE=1 runs it.
func TestRealServeLeadCreatesChildren(t *testing.T) {
	realloom.Skip(t)
	model := newLeadModel(t)
	sbx := realloom.NewSandbox(t, model.URL)
	base := sbx.Serve(t, sbx.BuildLoom(t))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	leadCreatesChildren(ctx, t, client.New(client.Config{BaseURL: base, Workspace: realloom.Workspace}), sbx, model)
}

// leadCreatesChildren: in one busy turn the lead calls agent_create for
// child one, retries that exact call, and creates child two: the retry
// returns the same child, so there are two children. Each child's attempt
// ends while the lead is still busy, and the lead gets two distinct
// task_completed records, one per child, both delivered to it. The tools
// are listed for the lead and not for its task children.
func leadCreatesChildren(ctx context.Context, t *testing.T, user *client.Client, sbx realloom.Sandbox, model *leadModel) {
	t.Helper()
	lead, err := user.Create(ctx, "lead-1", agentsv1.CreateBody{Preset: "lead", Name: "lead", Repo: sbx.Repo,
		BaseRef: sbx.Head, Overrides: agentsv1.Overrides{Harness: "opencode"}})
	if err != nil {
		t.Fatalf("Create lead: %v", err)
	}
	wait(ctx, t, "lead idle", func() bool {
		a, err := user.Get(ctx, lead.AgentID)
		return err == nil && a.State == loomagent.StateIdle
	})
	if _, err := user.Send(ctx, "go-1", lead.AgentID, "split the work"); err != nil {
		t.Fatal(err)
	}
	wait(ctx, t, "two task_completed records", func() bool {
		p, err := user.ListEvents(ctx, loomstore.EventQuery{AgentID: lead.AgentID, Kinds: []string{loomagent.KindTaskCompleted}})
		return err == nil && len(p.Events) >= 2
	})
	l, err := user.List(ctx, loomstore.AgentFilter{Parent: lead.AgentID})
	if err != nil {
		t.Fatal(err)
	}
	kids := l.Agents
	if len(kids) != 2 {
		t.Fatalf("lead has %d children; want 2 (the retried create returns the same child)", len(kids))
	}
	p, err := user.ListEvents(ctx, loomstore.EventQuery{AgentID: lead.AgentID, Kinds: []string{loomagent.KindTaskCompleted}})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range p.Events {
		seen[e.EventID] = true
	}
	for _, k := range kids {
		key := fmt.Sprintf("%s:%s:%d", loomagent.KindTaskCompleted, k.AgentID, k.Attempt)
		if !seen[key] {
			t.Errorf("no %s record on the lead; have %v", key, seen)
		}
		if k.ParentAgentID == nil || *k.ParentAgentID != lead.AgentID || k.Preset != "task" {
			t.Errorf("child %s: parent %v preset %s", k.AgentID, k.ParentAgentID, k.Preset)
		}
		wait(ctx, t, "lead reads "+key, func() bool { return model.leadSaw(key) })
	}
	if len(p.Events) != 2 {
		t.Errorf("lead has %d task_completed records; want 2", len(p.Events))
	}
	if !model.childrenDoneWhileLeadBusy() {
		t.Error("the children finished after the lead's turn ended")
	}
	if model.taskSawTools {
		t.Error("a task child was offered the agent tools")
	}

	// The lead's bridge, with its token, is registered with OpenCode only
	// while it is live, and never written to disk: archiving removes it, and
	// its next turn after an Unarchive registers it again before the prompt.
	got, err := user.Get(ctx, lead.AgentID)
	if err != nil || got.WorktreePath == nil {
		t.Fatalf("lead worktree: %+v, %v", got.WorktreePath, err)
	}
	dir := *got.WorktreePath
	if s := loomMCP(ctx, t, sbx, dir); s != "connected" {
		t.Fatalf("live lead's loom MCP server = %q; want connected", s)
	}
	assertNoTokenAtRest(t, sbx, dir)
	wait(ctx, t, "lead idle after its turn", func() bool {
		a, err := user.Get(ctx, lead.AgentID)
		return err == nil && a.State == loomagent.StateIdle
	})
	if err := user.Archive(ctx, "arch-1", lead.AgentID, "done"); err != nil {
		t.Fatal(err)
	}
	if s := loomMCP(ctx, t, sbx, dir); s != "" {
		t.Fatalf("archived lead's loom MCP server = %q; want removed", s)
	}
	if err := user.Unarchive(ctx, "unarch-1", lead.AgentID); err != nil {
		t.Fatal(err)
	}
	model.watch()
	if _, err := user.Send(ctx, "go-2", lead.AgentID, "once more"); err != nil {
		t.Fatal(err)
	}
	wait(ctx, t, "lead's turn after unarchive", func() bool { return model.leadSaw("once more") })
	if !model.toolsAtPrompt() {
		t.Error("the unarchived lead's prompt came before its bridge was registered again")
	}
}

// leadModel is an OpenAI-compatible streaming chat model. To the lead it
// answers create one, create one again (a retry), create two, then waits
// for both children to finish before ending its turn; to a task child it
// answers done.
type leadModel struct {
	*httptest.Server
	childDone chan struct{}

	mu           sync.Mutex
	watching     bool // watch: whether the next "once more" prompt lists the loom tools
	sawTools     bool
	leadBodies   []string
	leadEnded    bool
	childrenSeen int
	doneInTurn   bool
	taskSawTools bool
}

func newLeadModel(t *testing.T) *leadModel {
	m := &leadModel{childDone: make(chan struct{}, 8)}
	m.Server = httptest.NewServer(m)
	t.Cleanup(m.Close)
	return m
}

func (m *leadModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var req struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(raw, &req)
	body := string(raw)
	// OpenCode lists MCP servers in its Code Mode catalog ("- loom (5
	// tools, ...)"); their tools are called as tools.loom.<tool> from code
	// passed to its execute tool.
	bridged := strings.Contains(body, "- loom (")
	switch {
	case strings.Contains(body, "You are a lead agent"):
		m.mu.Lock()
		m.leadBodies = append(m.leadBodies, body)
		if m.watching && strings.Contains(body, "once more") {
			m.sawTools, m.watching = bridged, false
		}
		ended := m.leadEnded
		m.mu.Unlock()
		tools := 0
		for _, msg := range req.Messages {
			if msg.Role == "tool" {
				tools++
			}
		}
		switch {
		case ended || tools >= 3:
			if !ended {
				for range 2 {
					select {
					case <-m.childDone:
					case <-time.After(2 * time.Minute):
					}
				}
				m.mu.Lock()
				m.doneInTurn, m.leadEnded = m.childrenSeen >= 2, true
				m.mu.Unlock()
			}
			stream(w, `{"role":"assistant","content":"ok"}`, "stop")
		case !bridged:
			stream(w, `{"role":"assistant","content":"no agent tools"}`, "stop")
		default:
			name := map[int]string{0: "one", 1: "one", 2: "two"}[tools]
			code, _ := json.Marshal(map[string]string{"code": fmt.Sprintf(
				`return await tools.loom.agent_create({name: %q, brief: "do %s"})`, name, name)})
			call, _ := json.Marshal(map[string]any{"role": "assistant", "tool_calls": []map[string]any{{"index": 0,
				"id": fmt.Sprintf("call_%d", tools), "type": "function",
				"function": map[string]string{"name": "execute", "arguments": string(code)}}}})
			stream(w, string(call), "tool_calls")
		}
	case strings.Contains(body, "You are a task agent"):
		m.mu.Lock()
		m.taskSawTools = m.taskSawTools || bridged
		m.childrenSeen++
		m.mu.Unlock()
		stream(w, `{"role":"assistant","content":"done"}`, "stop")
		m.childDone <- struct{}{}
	default: // titles and other helper calls
		stream(w, `{"role":"assistant","content":"title"}`, "stop")
	}
}

func (m *leadModel) leadSaw(text string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.leadBodies {
		if strings.Contains(b, text) {
			return true
		}
	}
	return false
}

func (m *leadModel) watch() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.watching = true
}

func (m *leadModel) toolsAtPrompt() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sawTools
}

// loomMCP is the status the sandbox's OpenCode service reports for the
// "loom" MCP server of location dir; "" when it lists none.
func loomMCP(ctx context.Context, t *testing.T, sbx realloom.Sandbox, dir string) string {
	t.Helper()
	var reg struct{ URL, Password string }
	raw, err := os.ReadFile(filepath.Join(sbx.Dir, "state/opencode/service.json"))
	if err == nil {
		err = json.Unmarshal(raw, &reg)
	}
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", reg.URL+"/api/mcp?location[directory]="+url.QueryEscape(dir), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("opencode", reg.Password)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var r struct {
		Data []struct {
			Name   string
			Status struct{ Status string }
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/mcp: %d, %v", resp.StatusCode, err)
	}
	for _, m := range r.Data {
		if m.Name == "loom" {
			return m.Status.Status
		}
	}
	return ""
}

// assertNoTokenAtRest fails if Loom wrote bridge settings in the lead's
// worktree's private git dir or an MCP entry under its worktrees root, as
// the file design did.
func assertNoTokenAtRest(t *testing.T, sbx realloom.Sandbox, dir string) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(sbx.Repo, ".git", "worktrees", "*", "loom-bridge.json"))
	if len(files) != 0 {
		t.Errorf("bridge settings at rest: %v", files)
	}
	if raw, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(dir)), ".opencode", "opencode.json")); err == nil && strings.Contains(string(raw), "mcp") {
		t.Errorf("an MCP entry at rest under the worktrees root: %s", raw)
	}
}

func (m *leadModel) childrenDoneWhileLeadBusy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.doneInTurn
}

// stream writes delta as one chunk, then the finish chunk.
func stream(w http.ResponseWriter, delta, finish string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, c := range []string{delta, `{}`} {
		f := "null"
		if c == `{}` {
			f = `"` + finish + `"`
		}
		_, _ = fmt.Fprintf(w, `data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":%s,"finish_reason":%s}]}`+"\n\n", c, f)
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

func wait(ctx context.Context, t *testing.T, what string, ok func() bool) {
	t.Helper()
	for !ok() {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", what)
		case <-time.After(100 * time.Millisecond):
		}
	}
}
