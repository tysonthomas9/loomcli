package agentmcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/serve/agentwire"
	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomagent/client"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentsv1"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// TestRealOpenCodeLeadCreatesChildren runs serve's Agent API wiring on the
// real OpenCode build in an owned /tmp sandbox with a fake model, and a
// freshly built `loom agent mcp-bridge`. In one busy turn the lead calls
// agent_create for child one, retries that exact call, and creates child
// two: the retry returns the same child, so there are two children. Each
// child's attempt ends while the lead is still busy, and the lead gets two
// distinct task_completed records, one per child, both delivered to it.
// The tools are listed for the lead and not for its task children.
// LOOM_REAL_OPENCODE=1 runs it.
func TestRealOpenCodeLeadCreatesChildren(t *testing.T) {
	if os.Getenv("LOOM_REAL_OPENCODE") != "1" {
		t.Skip("set LOOM_REAL_OPENCODE=1 to run against the real OpenCode build")
	}
	bin := os.Getenv("LOOM_OPENCODE_BIN")
	if bin == "" {
		home, _ := os.UserHomeDir()
		bin = filepath.Join(home, ".loom/harness/opencode/2.0.19/opencode")
	}
	sbx := sandbox(t)
	loom := filepath.Join(sbx, "loom-bin")
	if out, err := exec.Command("go", "build", "-o", loom, "github.com/tysonthomas9/loomcli/cmd/loom").CombinedOutput(); err != nil {
		t.Fatalf("build loom: %v %s", err, out)
	}
	model := &leadModel{childDone: make(chan struct{}, 8)}
	ms := httptest.NewServer(model)
	t.Cleanup(ms.Close)
	write(t, filepath.Join(sbx, "config/opencode/opencode.json"), fmt.Sprintf(`{"provider":{"fake":{"name":"Fake",
		"npm":"@ai-sdk/openai-compatible","options":{"baseURL":%q,"apiKey":"x"},
		"models":{"m":{"name":"M","limit":{"context":100000,"output":4000}}}}},"model":"fake/m","small_model":"fake/m"}`, ms.URL+"/v1"))
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + sbx + "/home", "TMPDIR=" + sbx + "/tmp/",
		"XDG_DATA_HOME=" + sbx + "/data", "XDG_CONFIG_HOME=" + sbx + "/config",
		"XDG_STATE_HOME=" + sbx + "/state", "XDG_CACHE_HOME=" + sbx + "/cache", "OPENCODE_DISABLE_MODELS_FETCH=1"}
	repo := filepath.Join(sbx, "repo")
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.name=t", "-c", "user.email=t@t",
		"commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	api, err := agentwire.Start(ctx, agentwire.Config{Dir: filepath.Join(sbx, "loom"), OpenCodeBin: bin,
		OpenCodeEnv: env, APIBase: "http://" + l.Addr().String(), LoomBin: loom})
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

	user := client.New(client.Config{BaseURL: srv.URL, Workspace: "ws"})
	lead, err := user.Create(ctx, "lead-1", agentsv1.CreateBody{Preset: "lead", Name: "lead", Repo: repo,
		BaseRef: strings.TrimSpace(string(head)), Overrides: agentsv1.Overrides{Harness: "opencode"}})
	if err != nil {
		t.Fatalf("Create lead: %v", err)
	}
	wait(t, ctx, "lead idle", func() bool {
		a, err := user.Get(ctx, lead.AgentID)
		return err == nil && a.State == loomagent.StateIdle
	})
	if _, err := user.Send(ctx, "go-1", lead.AgentID, "split the work"); err != nil {
		t.Fatal(err)
	}

	var kids []agentsv1.Agent
	wait(t, ctx, "two task_completed records", func() bool {
		p, err := user.ListEvents(ctx, loomstore.EventQuery{AgentID: lead.AgentID, Kinds: []string{loomagent.KindTaskCompleted}})
		return err == nil && len(p.Events) >= 2
	})
	l2, err := user.List(ctx, loomstore.AgentFilter{Parent: lead.AgentID})
	if err != nil {
		t.Fatal(err)
	}
	kids = l2.Agents
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
		wait(t, ctx, "lead reads "+key, func() bool { return model.leadSaw(key) })
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
}

// leadModel is an OpenAI-compatible streaming chat model. To the lead it
// answers create one, create one again (a retry), create two, then waits
// for both children to finish before ending its turn; to a task child it
// answers done.
type leadModel struct {
	childDone chan struct{}

	mu           sync.Mutex
	leadBodies   []string
	leadEnded    bool
	childrenSeen int
	doneInTurn   bool
	taskSawTools bool
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

// sandbox is an owned /tmp dir for one OpenCode user, with a service config
// on a free loopback port. Cleanup stops the service registered there and
// removes the dir.
func sandbox(t *testing.T) string {
	t.Helper()
	sbx, err := os.MkdirTemp("/tmp", "agentmcp-real-")
	if err == nil {
		sbx, err = filepath.EvalSymlinks(sbx) // OpenCode reports resolved paths
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		var reg struct{ PID int }
		if b, err := os.ReadFile(filepath.Join(sbx, "state/opencode/service.json")); err == nil && json.Unmarshal(b, &reg) == nil && reg.PID > 0 {
			_ = syscall.Kill(reg.PID, syscall.SIGTERM)
			for end := time.Now().Add(10 * time.Second); syscall.Kill(reg.PID, 0) == nil && time.Now().Before(end); {
				time.Sleep(50 * time.Millisecond)
			}
			_ = syscall.Kill(reg.PID, syscall.SIGKILL)
		}
		_ = os.RemoveAll(sbx)
	})
	for _, d := range []string{"home", "tmp", "config/opencode", "repo"} {
		if err := os.MkdirAll(filepath.Join(sbx, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	write(t, filepath.Join(sbx, "config/opencode/service.json"), fmt.Sprintf(`{"port":%d}`, port))
	return sbx
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func wait(t *testing.T, ctx context.Context, what string, ok func() bool) {
	t.Helper()
	for !ok() {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", what)
		case <-time.After(100 * time.Millisecond):
		}
	}
}
