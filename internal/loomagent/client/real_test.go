package client

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
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/serve/agentwire"
	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentsv1"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/subscription"
)

// TestRealServeAgentAPI runs the Agent API as serve wires it, on the real
// OpenCode build in a /tmp sandbox with a fake model: Create, Send and the
// event stream reach idle with no bridge token. LOOM_REAL_OPENCODE=1 runs it.
func TestRealServeAgentAPI(t *testing.T) {
	if os.Getenv("LOOM_REAL_OPENCODE") != "1" {
		t.Skip("set LOOM_REAL_OPENCODE=1 to run against the real OpenCode build")
	}
	bin := os.Getenv("LOOM_OPENCODE_BIN")
	if bin == "" {
		home, _ := os.UserHomeDir()
		bin = filepath.Join(home, ".loom/harness/opencode/2.0.19/opencode")
	}
	sbx := realSandbox(t)
	model := httptest.NewServer(http.HandlerFunc(fakeModel))
	t.Cleanup(model.Close)
	writeFile(t, filepath.Join(sbx, "config/opencode/opencode.json"), fmt.Sprintf(`{"provider":{"fake":{"name":"Fake",
		"npm":"@ai-sdk/openai-compatible","options":{"baseURL":%q,"apiKey":"x"},
		"models":{"m":{"name":"M","limit":{"context":100000,"output":4000}}}}},"model":"fake/m"}`, model.URL+"/v1"))
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

	ctx := context.Background()
	api, err := agentwire.Start(ctx, agentwire.Config{WorkspaceID: "ws", Dir: filepath.Join(sbx, "loom"),
		OpenCodeBin: bin, OpenCodeEnv: env})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Stop)
	tokens, err := realtime.NewTokenStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tokens.Stop)
	mux := http.NewServeMux()
	ws := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(middleware.WithWorkspace(r.Context(), r.PathValue("ws"))))
		})
	}
	mux.Handle("GET /api/workspaces/{ws}/events/token", ws(subscription.HandleSSEToken(tokens)))
	api.Register(mux, ws, tokens.Validate)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := newClient(srv, "ws", "")

	a, err := c.Create(ctx, "r1", agentsv1.CreateBody{Preset: "pr-review-interactive", Name: "rev", Repo: repo,
		BaseRef: strings.TrimSpace(string(head)), Overrides: agentsv1.Overrides{Harness: "opencode"}})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "idle after create", func() bool {
		got, err := c.Get(ctx, a.AgentID)
		return err == nil && got.State == loomagent.StateIdle
	})
	streamCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	stream, err := c.Subscribe(streamCtx, SubscribeRequest{Agents: []string{a.AgentID}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if _, err := c.Send(ctx, "s1", a.AgentID, "hello"); err != nil {
		t.Fatal(err)
	}
	for {
		e, err := stream.Next()
		if err != nil {
			t.Fatalf("stream ended before idle: %v", err)
		}
		if e.Kind == loomagent.EventIdle {
			break
		}
	}
}

// realSandbox is an owned /tmp dir for one OpenCode user, with a service
// config on a free loopback port. Cleanup stops the service registered there
// (Loom leaves the one it starts running) and removes the dir.
func realSandbox(t *testing.T) string {
	t.Helper()
	sbx, err := os.MkdirTemp("/tmp", "agentapi-real-")
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
	writeFile(t, filepath.Join(sbx, "config/opencode/service.json"), fmt.Sprintf(`{"port":%d}`, port))
	return sbx
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fakeModel is an OpenAI-compatible chat completion that streams "done".
func fakeModel(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	w.Header().Set("Content-Type", "text/event-stream")
	for _, c := range []string{`{"role":"assistant","content":"done"}`, `{}`} {
		finish := "null"
		if c == `{}` {
			finish = `"stop"`
		}
		_, _ = fmt.Fprintf(w, `data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":%s,"finish_reason":%s}]}`+"\n\n", c, finish)
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}
