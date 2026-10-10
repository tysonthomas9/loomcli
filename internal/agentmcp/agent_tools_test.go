package agentmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomagent/client"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentsv1"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

var leadTools = []string{"agent_create", "agent_list", "agent_get", "agent_send", "agent_archive"}

// agentAPI serves the real Agent API routes for workspace "ws" with leads
// a1 and b1, each with one busy task child (c1 under a1, c2 under b1).
func agentAPI(t *testing.T, github ...agentsv1.GitHubReader) (*httptest.Server, *agentsv1.Tokens) {
	t.Helper()
	ctx := context.Background()
	st, err := loomstore.Open(ctx, filepath.Join(t.TempDir(), "loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	agent := func(id, parent string) loomstore.Agent {
		a := loomstore.Agent{AgentID: id, WorkspaceID: "ws", Name: id, ProfileKey: id, Preset: "lead",
			PresetVersion: "1", Mode: "persistent", InteractionMode: "interactive", RoleKind: "interactive",
			SpecJSON: "{}", SpecVersion: 1, OwnerKind: "user", OwnerID: "local", CreatedByKind: "user",
			CreatedByID: "local", CreateRequestID: "req-" + id, Repo: "/repo", Harness: "fake",
			State: loomagent.StateIdle, Attempt: 1}
		if parent != "" {
			turn := "turn_" + id
			a.Preset, a.Mode, a.State, a.ParentAgentID, a.RootAgentID, a.RunningTurnID =
				"task", "single_task", loomagent.StateActive, &parent, &parent, &turn
		}
		return a
	}
	for _, a := range []loomstore.Agent{agent("a1", ""), agent("b1", ""), agent("c1", "a1"), agent("c2", "b1")} {
		if err := st.InsertAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	svc := loomagent.New(loomagent.ServiceConfig{Store: st, WorkspaceID: "ws"})
	tokens := agentsv1.NewTokens([]byte(strings.Repeat("k", 32)))
	mux := http.NewServeMux()
	ws := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(middleware.WithWorkspace(r.Context(), r.PathValue("ws"))))
		})
	}
	h := agentsv1.New(func(string) *loomagent.Service { return svc }, nil).WithTokens(tokens)
	if len(github) > 0 {
		h.WithGitHub(github[0])
	}
	h.Register(mux, ws, nil)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, tokens
}

// connect returns an MCP client session on the bridge for cfg.
func connect(t *testing.T, cfg Config) *mcp.ClientSession {
	t.Helper()
	s, err := NewServer(cfg, client.New(client.Config{BaseURL: cfg.API, Workspace: cfg.Workspace,
		Token: func(context.Context) (string, error) { return cfg.Token, nil }}))
	if err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// call calls tool and returns its structured result, or its error text.
func call(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (map[string]any, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	if res.IsError {
		return nil, res.Content[0].(*mcp.TextContent).Text
	}
	raw, _ := json.Marshal(res.StructuredContent)
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s result: %v", tool, err)
	}
	return out, ""
}

// TestAgentToolsActOnOwnChildrenOnly: the bridge acts as the agent its token
// names. A lead lists, gets, messages and archives only its own children;
// another lead's child is agent_not_found; a forged token reaches nothing.
func TestAgentToolsActOnOwnChildrenOnly(t *testing.T) {
	srv, tokens := agentAPI(t)
	a1 := connect(t, Config{API: srv.URL, Workspace: "ws", Token: tokens.Agent("ws", "a1"), Repo: "/repo", Tools: leadTools})

	out, msg := call(t, a1, "agent_list", nil)
	if msg != "" || len(out["agents"].([]any)) != 1 || out["agents"].([]any)[0].(map[string]any)["agent_id"] != "c1" {
		t.Fatalf("agent_list = %v %q; want only c1", out, msg)
	}
	if out, msg := call(t, a1, "agent_get", map[string]any{"agent": "c1"}); msg != "" || out["agent_id"] != "c1" {
		t.Fatalf("agent_get c1 = %v %q", out, msg)
	}
	for tool, args := range map[string]map[string]any{
		"agent_get":     {"agent": "c2"},
		"agent_send":    {"agent": "c2", "text": "hi"},
		"agent_archive": {"agent": "c2", "cancel": true},
	} {
		if _, msg := call(t, a1, tool, args); !strings.Contains(msg, string(loomagent.CodeAgentNotFound)) {
			t.Errorf("%s on another lead's child = %q; want agent_not_found", tool, msg)
		}
	}
	forged := connect(t, Config{API: srv.URL, Workspace: "ws", Token: agentsv1.NewTokens([]byte(strings.Repeat("x", 32))).Agent("ws", "a1"),
		Tools: leadTools})
	if _, msg := call(t, forged, "agent_list", nil); !strings.Contains(msg, "401") {
		t.Errorf("agent_list with a forged token = %q; want 401", msg)
	}
}

// TestAgentToolsArgumentsNeverSetIdentity: no tool takes a caller, parent or
// workspace; an argument naming one is refused before any call.
func TestAgentToolsArgumentsNeverSetIdentity(t *testing.T) {
	srv, tokens := agentAPI(t)
	a1 := connect(t, Config{API: srv.URL, Workspace: "ws", Token: tokens.Agent("ws", "a1"), Repo: "/repo", Tools: leadTools})
	for _, field := range []string{"parent", "caller", "actor", "workspace", "token"} {
		_, msg := call(t, a1, "agent_create", map[string]any{"name": "x", "brief": "b", field: "b1"})
		if msg == "" || !strings.Contains(msg, field) {
			t.Errorf("agent_create with %s = %q; want refused", field, msg)
		}
		if _, msg := call(t, a1, "agent_list", map[string]any{field: "b1"}); msg == "" {
			t.Errorf("agent_list with %s was accepted", field)
		}
	}
	if _, msg := call(t, a1, "agent_create", map[string]any{"preset": "lead", "name": "x", "brief": "b"}); msg == "" {
		t.Error("agent_create made a non-task agent")
	}
}

// TestAgentSendReportsReplaced: a second message to a busy child replaces
// the lead's first one still waiting, and agent_send says so.
func TestAgentSendReportsReplaced(t *testing.T) {
	srv, tokens := agentAPI(t)
	a1 := connect(t, Config{API: srv.URL, Workspace: "ws", Token: tokens.Agent("ws", "a1"), Repo: "/repo", Tools: leadTools})
	for i, want := range []bool{false, true} {
		out, msg := call(t, a1, "agent_send", map[string]any{"agent": "c1", "text": "step " + string(rune('1'+i))})
		if msg != "" || out["replaced"] != want {
			t.Fatalf("send %d = %v %q; want replaced %v", i+1, out, msg, want)
		}
	}
}

// TestAgentBridgeSettings: the settings come only from the environment, and
// with no token the bridge fails closed; only the named tools are listed; an
// unknown tool is refused.
func TestAgentBridgeSettings(t *testing.T) {
	want := Config{API: "http://x", Workspace: "ws", Token: "tok", Repo: "/repo", Tools: leadTools}
	for k := range want.Env() {
		t.Setenv(k, "")
	}
	t.Setenv(EnvAPI, "http://x")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), EnvToken) {
		t.Fatalf("Load with no token = %v; want a missing-token error", err)
	}
	for k, v := range want.Env() {
		t.Setenv(k, v)
	}
	if got, err := Load(); err != nil || !slices.Equal(got.Tools, leadTools) || got.Token != "tok" || got.Repo != "/repo" || got.API != "http://x" {
		t.Fatalf("Load = %+v, %v", got, err)
	}
	if got := names(t, connect(t, Config{})); len(got) != 0 {
		t.Fatalf("tools with no settings = %v", got)
	}
	if got := names(t, connect(t, want)); !slices.Equal(got, []string{"agent_archive", "agent_create", "agent_get", "agent_list", "agent_send"}) {
		t.Fatalf("lead tools = %v", got)
	}
	if err := Check([]string{"review_post"}); err == nil {
		t.Fatal("Check accepted review_post, which the bridge does not serve yet")
	}
}

// TestAgentBridgeVerifiesToken: a bridge starts only with a token the Agent
// API accepts; a forged token, or one of an archived agent, fails closed
// without the token in the error.
func TestAgentBridgeVerifiesToken(t *testing.T) {
	srv, tokens := agentAPI(t)
	api := func(tok string) API {
		return client.New(client.Config{BaseURL: srv.URL, Workspace: "ws",
			Token: func(context.Context) (string, error) { return tok, nil }})
	}
	ctx := context.Background()
	if err := Verify(ctx, api(tokens.Agent("ws", "a1"))); err != nil {
		t.Fatalf("Verify with a1's token: %v", err)
	}
	forged := agentsv1.NewTokens([]byte(strings.Repeat("x", 32))).Agent("ws", "a1")
	if err := Verify(ctx, api(forged)); err == nil || !strings.Contains(err.Error(), "refused") || strings.Contains(err.Error(), forged) {
		t.Fatalf("Verify with a forged token = %v; want a clear refusal without the token", err)
	}
	if err := api(tokens.Daemon("ws")).Archive(ctx, "arch-a1", "a1", "done"); err != nil {
		t.Fatal(err)
	}
	if err := Verify(ctx, api(tokens.Agent("ws", "a1"))); err == nil {
		t.Fatal("Verify accepted the token of an archived agent")
	}
}

func names(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tl := range res.Tools {
		got = append(got, tl.Name)
	}
	slices.Sort(got)
	return got
}
