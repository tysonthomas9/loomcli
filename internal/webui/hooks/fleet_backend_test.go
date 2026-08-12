package hooks

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
)

const testFleetURL = "http://localhost:0"

func newTestFleetBackendHook(t *testing.T) *FleetBackendHook {
	t.Helper()
	return NewFleetBackendHook(testFleetURL, "test-key", "test-actor", slog.Default())
}

func TestFleetBackendHook_Name(t *testing.T) {
	hook := newTestFleetBackendHook(t)
	if got := hook.Name(); got != "fleet-backend" {
		t.Errorf("Name() = %q, want %q", got, "fleet-backend")
	}
}

func TestFleetBackendHook_Critical(t *testing.T) {
	hook := newTestFleetBackendHook(t)
	if hook.Critical() {
		t.Error("Critical() = true, want false")
	}
}

func TestFleetBackendHook_OnRegister_CreatesBackend(t *testing.T) {
	hook := newTestFleetBackendHook(t)

	ctx := regCtx("ws-fleet-be-1", "/tmp/ws1")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister returned error: %v", err)
	}

	res, ok := ctx.Resolve(coordinator.ResourceKeyFleetBackend)
	if !ok {
		t.Fatal("expected ResourceKeyFleetBackend to be provided after OnRegister")
	}
	be, ok := res.(backend.IssueBackend)
	if !ok {
		t.Fatal("expected resource to implement backend.IssueBackend")
	}
	if be == nil {
		t.Fatal("expected non-nil backend")
	}
	if got := be.BackendName(); got != "fleet" {
		t.Errorf("BackendName() = %q, want %q", got, "fleet")
	}
}

func TestFleetBackendHook_OnRegisterScopesBackendToRegisteredWorkspace(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data":    []any{},
		})
	}))
	defer srv.Close()

	hook := NewFleetBackendHook(srv.URL, "", "test-actor", slog.Default())
	ctx := regCtx("DEMO-WS", "/tmp/demo")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister returned error: %v", err)
	}

	res, ok := ctx.Resolve(coordinator.ResourceKeyFleetBackend)
	if !ok {
		t.Fatal("expected fleet backend resource")
	}
	be := res.(backend.IssueBackend)
	ready, ok := be.(workitems.ReadyQueries)
	if !ok {
		t.Fatal("expected Work Items ready queries")
	}
	if _, err := ready.Ready(context.Background(), workitems.AvailabilityQuery{}); err != nil {
		t.Fatalf("Ready returned error: %v", err)
	}
	if gotPath != "/api/v1/DEMO-WS/issues/ready" {
		t.Fatalf("request path = %q, want DEMO-WS scoped backend", gotPath)
	}
}

func TestFleetBackendHook_V2MutationWaitCarriesAPIKey(t *testing.T) {
	const serviceCredential = "embedded-service-credential"
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/SECURE/events/mutations" {
			t.Errorf("request = %s %s, want GET /api/v2/SECURE/events/mutations", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-API-Key") != serviceCredential || r.Header.Get("X-Fleet-API-Key") != serviceCredential {
			t.Error("v2 mutation wait did not carry the FleetBackendHook API key")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"events": []any{}, "cursor": "0"})
	}))
	defer server.Close()

	hook := NewFleetBackendHook(server.URL, serviceCredential, "local-service", slog.Default())
	ctx := regCtx("SECURE", "/tmp/secure")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister: %v", err)
	}
	resource, ok := ctx.Resolve(coordinator.ResourceKeyFleetBackend)
	if !ok {
		t.Fatal("expected FleetBackend resource")
	}
	cursorBackend, ok := resource.(workitems.MutationStream)
	if !ok {
		t.Fatalf("resource %T does not implement workitems.MutationStream", resource)
	}
	if _, err := cursorBackend.WaitForMutationsAfter(context.Background(), "0", 1); err != nil {
		t.Fatalf("WaitForMutationsAfter: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestFleetBackendHook_EmptyWorkspaceRequiresExplicitWorkspace(t *testing.T) {
	hook := NewFleetBackendHook(testFleetURL, "", "test-actor", slog.Default())
	ctx := regCtx("", "/tmp/demo")

	if err := hook.OnRegister(ctx); err == nil {
		t.Fatal("OnRegister returned nil, want missing workspace error")
	}
	if _, ok := ctx.Resolve(coordinator.ResourceKeyFleetBackend); ok {
		t.Fatal("unexpected fleet backend resource for empty workspace")
	}
}

func TestFleetBackendHook_OnRegister_Error(t *testing.T) {
	// Empty baseURL causes fleet.New to fail.
	hook := NewFleetBackendHook("", "test-key", "test-actor", slog.Default())

	ctx := regCtx("ws-fleet-err", "/tmp/ws-err")
	if err := hook.OnRegister(ctx); err == nil {
		t.Fatal("expected OnRegister to return error with empty baseURL")
	}

	// Resource should not be provided on failure.
	if _, ok := ctx.Resolve(coordinator.ResourceKeyFleetBackend); ok {
		t.Error("expected ResourceKeyFleetBackend to NOT be provided after error")
	}
}

func TestFleetBackendHook_OnDeregister_RunsCleanly(t *testing.T) {
	hook := newTestFleetBackendHook(t)

	ctx := regCtx("ws-fleet-dereg", "/tmp/ws-dereg")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister: %v", err)
	}

	// OnDeregister should complete without panic.
	hook.OnDeregister(deregCtx("ws-fleet-dereg"))
}

func TestFleetBackendHook_OnRollback_RunsCleanly(t *testing.T) {
	hook := newTestFleetBackendHook(t)

	ctx := regCtx("ws-fleet-rb", "/tmp/ws-rb")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister: %v", err)
	}

	// OnRollback should complete without panic.
	hook.OnRollback(deregCtx("ws-fleet-rb"))
}

func TestFleetBackendHook_MultipleWorkspaces(t *testing.T) {
	hook := newTestFleetBackendHook(t)

	type wsEntry struct {
		id  string
		ctx *coordinator.RegistrationContext
	}

	workspaces := []wsEntry{
		{"ws-alpha", regCtx("ws-alpha", "/tmp/ws-alpha")},
		{"ws-beta", regCtx("ws-beta", "/tmp/ws-beta")},
		{"ws-gamma", regCtx("ws-gamma", "/tmp/ws-gamma")},
	}
	for _, ws := range workspaces {
		if err := hook.OnRegister(ws.ctx); err != nil {
			t.Fatalf("OnRegister(%q): %v", ws.id, err)
		}
	}

	// All three should have fleet backends via their registration contexts.
	for _, ws := range workspaces {
		res, ok := ws.ctx.Resolve(coordinator.ResourceKeyFleetBackend)
		if !ok {
			t.Errorf("expected resource for %q", ws.id)
			continue
		}
		be, ok := res.(backend.IssueBackend)
		if !ok || be == nil {
			t.Errorf("expected non-nil backend.IssueBackend for %q", ws.id)
		}
	}

	// Deregister one; others' contexts should still resolve.
	hook.OnDeregister(deregCtx("ws-beta"))

	for _, wsID := range []string{"ws-alpha", "ws-gamma"} {
		for _, ws := range workspaces {
			if ws.id == wsID {
				if _, ok := ws.ctx.Resolve(coordinator.ResourceKeyFleetBackend); !ok {
					t.Errorf("expected %q resource to remain after deregistering ws-beta", wsID)
				}
			}
		}
	}
}
