package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/serve/agentwire"
	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/testutil/realloom"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentsv1"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/subscription"
)

// TestRealServeAgentAPI runs the Agent API as serve wires it, on the real
// OpenCode build in a /tmp sandbox with a fake model: in each of two
// workspaces, Create, Send and the event stream reach idle with no bridge
// token, and neither workspace sees the other's agent. LOOM_REAL_OPENCODE=1
// runs it.
func TestRealServeAgentAPI(t *testing.T) {
	realloom.Skip(t)
	model := httptest.NewServer(http.HandlerFunc(fakeModel))
	t.Cleanup(model.Close)
	sbx := realloom.NewSandbox(t, model.URL)

	ctx := context.Background()
	api, err := agentwire.Start(ctx, agentwire.Config{Dir: filepath.Join(sbx.Dir, "loom"),
		OpenCodeBin: realloom.OpenCodeBin(), OpenCodeEnv: sbx.Env()})
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
			ctx := middleware.WithWorkspace(r.Context(), r.PathValue("ws"))
			if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
				ctx = middleware.WithUserIdentity(ctx, middleware.UserIdentity{UserID: tok})
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	mux.Handle("GET /api/workspaces/{ws}/events/token", ws(subscription.HandleSSEToken(tokens)))
	api.Register(mux, ws, tokens.Validate)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	ids := map[string]string{}
	for _, ws := range []string{"ws", "ws2"} {
		c := newClient(srv, ws, "alice")
		a, err := c.Create(ctx, "r1", agentsv1.CreateBody{Preset: "pr-review-interactive", Name: "rev", Repo: sbx.Repo,
			BaseRef: sbx.Head, Overrides: agentsv1.Overrides{Harness: "opencode"}})
		if err != nil {
			t.Fatalf("%s Create: %v", ws, err)
		}
		ids[ws] = a.AgentID
		eventually(t, ws+" idle after create", func() bool {
			got, err := c.Get(ctx, a.AgentID)
			return err == nil && got.State == loomagent.StateIdle
		})
		streamCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		stream, err := c.Subscribe(streamCtx, SubscribeRequest{Agents: []string{a.AgentID}})
		if err != nil {
			cancel()
			t.Fatalf("%s Subscribe: %v", ws, err)
		}
		if _, err := c.Send(ctx, "s1", a.AgentID, "hello"); err != nil {
			t.Fatalf("%s Send: %v", ws, err)
		}
		for {
			e, err := stream.Next()
			if err != nil {
				t.Fatalf("%s stream ended before idle: %v", ws, err)
			}
			if e.Kind == loomagent.EventIdle {
				break
			}
		}
		_ = stream.Close()
		cancel()
	}
	if ids["ws"] == ids["ws2"] {
		t.Fatalf("both workspaces got agent %s", ids["ws"])
	}
	for ws, other := range map[string]string{"ws": "ws2", "ws2": "ws"} {
		if _, err := newClient(srv, ws, "alice").Get(ctx, ids[other]); code(err) != loomagent.CodeAgentNotFound {
			t.Errorf("%s Get of %s's agent = %v; want agent_not_found", ws, other, err)
		}
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

// eventually waits up to 10s for ok. Only the real-process tests poll: they
// wait on a real harness and model over HTTP, which nothing here can drain
// (allowlisted in scripts/sleep-allowlist.txt).
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); !ok(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}
