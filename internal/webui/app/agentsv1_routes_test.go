package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/appstores"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentsv1"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
)

// TestAgentAPIPatchAndCorsThroughServer: the Agent API routes are on the
// app server's outer mux behind the workspace middleware; a PATCH body is
// read, a cross-origin preflight allows PATCH and Idempotency-Key, and an
// unknown workspace is refused before the handler.
func TestAgentAPIPatchAndCorsThroughServer(t *testing.T) {
	ctx := context.Background()
	st, err := loomstore.Open(ctx, filepath.Join(t.TempDir(), "loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.InsertAgent(ctx, loomstore.Agent{AgentID: "a1", WorkspaceID: "ws", Name: "a1", ProfileKey: "a1",
		Preset: "lead", PresetVersion: "1", Mode: "persistent", InteractionMode: "interactive",
		RoleKind: "interactive", SpecJSON: "{}", SpecVersion: 1, OwnerKind: "user", OwnerID: "local",
		CreatedByKind: "user", CreatedByID: "local", CreateRequestID: "req-a1", Repo: "/repo",
		Harness: "fake", State: loomagent.StateIdle, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	svc := loomagent.New(loomagent.ServiceConfig{Store: st, WorkspaceID: "ws"})
	api := agentsv1.New(func(string) *loomagent.Service { return svc }, nil)
	app := &Server{
		multiPool:  daemon.NewMultiPool(middleware.WorkspaceFromContext, 1),
		config:     webui.ServerConfig{AgentAPIRoutes: api.Register},
		wsExistsFn: func(id string) bool { return id == "ws" },
	}
	app.sessSvc = svcimpl.NewSessionService(nil, nil)
	setupTestRoutes(t, app)
	const origin = "http://localhost:3000"
	srv := httptest.NewServer(middleware.CORS(middleware.CORSConfig{Enabled: true, AllowedOrigins: []string{origin}})(app.mux))
	t.Cleanup(srv.Close)

	do := func(method, path, body string, header map[string]string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range header {
			req.Header.Set(k, v)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	pre := do(http.MethodOptions, "/api/workspaces/ws/v1/agents/a1", "", map[string]string{
		"Origin": origin, "Access-Control-Request-Method": "PATCH",
		"Access-Control-Request-Headers": "content-type, idempotency-key"})
	if pre.StatusCode != http.StatusNoContent ||
		!strings.Contains(pre.Header.Get("Access-Control-Allow-Methods"), "PATCH") ||
		!strings.Contains(pre.Header.Get("Access-Control-Allow-Headers"), "Idempotency-Key") {
		t.Fatalf("preflight = %d %v", pre.StatusCode, pre.Header)
	}

	resp := do(http.MethodPatch, "/api/workspaces/ws/v1/agents/a1", `{"name":"renamed"}`, map[string]string{
		"Origin": origin, "Content-Type": "application/json", "Idempotency-Key": "u1"})
	var got struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || resp.StatusCode != http.StatusOK ||
		got.Name != "renamed" || resp.Header.Get("Access-Control-Allow-Origin") != origin {
		t.Fatalf("patch = %d %+v %v", resp.StatusCode, got, err)
	}

	if resp := do(http.MethodGet, "/api/workspaces/nope/v1/agents", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown workspace = %d", resp.StatusCode)
	}
}

// TestAgentAPISSEUsesServerTokens: the Agent API stream is on the app mux and
// checks the server's one-time SSE tokens.
func TestAgentAPISSEUsesServerTokens(t *testing.T) {
	ctx := context.Background()
	st, err := loomstore.Open(ctx, filepath.Join(t.TempDir(), "loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := loomagent.New(loomagent.ServiceConfig{Store: st, WorkspaceID: "ws"})
	tokens, err := appstores.NewTokenStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tokens.Stop)
	app := &Server{
		multiPool:  daemon.NewMultiPool(middleware.WorkspaceFromContext, 1),
		config:     webui.ServerConfig{AgentAPIRoutes: agentsv1.New(func(string) *loomagent.Service { return svc }, nil).Register},
		wsExistsFn: func(id string) bool { return id == "ws" },
		sseTokens:  tokens,
	}
	app.sessSvc = svcimpl.NewSessionService(nil, nil)
	setupTestRoutes(t, app)
	srv := httptest.NewServer(app.mux)
	t.Cleanup(srv.Close)
	get := func(query string) int {
		resp, err := srv.Client().Get(srv.URL + "/api/workspaces/ws/v1/events?" + query)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if status := get("agents=zz"); status != http.StatusUnauthorized {
		t.Fatalf("no token = %d", status)
	}
	tok, _ := tokens.Generate("u", "ws")
	if status := get("agents=zz&after=zz:0&token=" + tok); status != http.StatusNotFound {
		t.Fatalf("fresh token, unknown agent = %d; want 404 from the handler", status)
	}
}
