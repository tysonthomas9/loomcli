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

	resp := do(http.MethodPatch, "/api/workspaces/ws/v1/agents/a1", `{"Name":"renamed"}`, map[string]string{
		"Origin": origin, "Content-Type": "application/json", "Idempotency-Key": "u1"})
	var got loomagent.AgentInfo
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || resp.StatusCode != http.StatusOK ||
		got.Name != "renamed" || resp.Header.Get("Access-Control-Allow-Origin") != origin {
		t.Fatalf("patch = %d %+v %v", resp.StatusCode, got, err)
	}

	if resp := do(http.MethodGet, "/api/workspaces/nope/v1/agents", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown workspace = %d", resp.StatusCode)
	}
}
