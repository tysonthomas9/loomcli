package subscription

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

func TestModule_RegisterRoutes(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Stop()

	tokens, err := realtime.NewTokenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer tokens.Stop()

	getMutations := func(_ string, _ string) ([]rpc.MutationEvent, error) { return nil, nil }
	wsFromCtx := func(_ context.Context) string { return "test-ws" }

	mod := NewModule(hub, getMutations, wsFromCtx, nil, tokens)

	mux := http.NewServeMux()
	mod.Register(mux)

	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/workspaces/test-ws/events"},
		{"GET", "/api/workspaces/test-ws/events/token"},
	}

	for _, rt := range routes {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(rt.method, rt.path, nil)
		mux.ServeHTTP(rec, req)

		if rec.Code == http.StatusNotFound {
			t.Errorf("%s %s: got 404, route not registered", rt.method, rt.path)
		}
		if rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s %s: got 405, wrong method registered", rt.method, rt.path)
		}
	}
}

func TestModule_ConditionalRoutes(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Stop()

	getMutations := func(_ string, _ string) ([]rpc.MutationEvent, error) { return nil, nil }
	wsFromCtx := func(_ context.Context) string { return "test-ws" }

	t.Run("nil sseTokens returns disabled token response", func(t *testing.T) {
		mod := NewModule(hub, getMutations, wsFromCtx, nil, nil)

		mux := http.NewServeMux()
		mod.Register(mux)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/workspaces/test-ws/events/token", nil)
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("token route with nil sseTokens: expected 200, got %d", rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", got)
		}
		if body := rec.Body.String(); body == "" || !containsAll(body, `"disabled"`, `true`) {
			t.Errorf("disabled token response body = %q", body)
		}

		// Events route registration is verified by checking that a wrong method
		// returns 405 (not 404). We avoid GET because the SSE handler blocks
		// in streaming mode when no token auth is configured.
		rec = httptest.NewRecorder()
		req = httptest.NewRequest("POST", "/api/workspaces/test-ws/events", nil)
		mux.ServeHTTP(rec, req)

		if rec.Code == http.StatusNotFound {
			t.Error("events route should be registered even with nil sseTokens")
		}
	})
}

func TestModule_WrongMethod_Returns405(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Stop()

	tokens, err := realtime.NewTokenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer tokens.Stop()

	getMutations := func(_ string, _ string) ([]rpc.MutationEvent, error) { return nil, nil }
	wsFromCtx := func(_ context.Context) string { return "test-ws" }

	mod := NewModule(hub, getMutations, wsFromCtx, nil, tokens)

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/test-ws/events/token", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST .../events/token: expected 405, got %d", rec.Code)
	}
}

func TestModule_ActivatesWorkspaceOnTokenRoute(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Stop()

	tokens, err := realtime.NewTokenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer tokens.Stop()

	getMutations := func(_ string, _ string) ([]rpc.MutationEvent, error) { return nil, nil }
	wsFromCtx := func(_ context.Context) string { return "test-ws" }
	var activated []string
	activate := func(_ context.Context, wsID string) {
		activated = append(activated, wsID)
	}

	mod := NewModule(hub, getMutations, wsFromCtx, activate, tokens)

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/test-ws/events/token", nil)
	req = req.WithContext(middleware.WithUserIdentity(
		req.Context(),
		middleware.UserIdentity{UserID: "user-123", Email: "test@example.com"},
	))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("token route: expected 200, got %d", rec.Code)
	}
	if len(activated) != 1 || activated[0] != "test-ws" {
		t.Fatalf("activated = %v, want [test-ws]", activated)
	}
}

func TestModule_ActivatesResolvedWorkspacePerTokenRoute(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Stop()

	tokens, err := realtime.NewTokenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer tokens.Stop()

	getMutations := func(_ string, _ string) ([]rpc.MutationEvent, error) { return nil, nil }
	var activated []string
	activate := func(_ context.Context, wsID string) {
		activated = append(activated, wsID)
	}

	mod := NewModule(hub, getMutations, middleware.WorkspaceFromContext, activate, tokens)

	wsMux := http.NewServeMux()
	mod.Register(wsMux)
	mux := http.NewServeMux()
	mux.Handle("/api/workspaces/{ws}/", middleware.Workspace(func(id string) bool {
		return id == "ws-alpha" || id == "ws-beta"
	})(wsMux))

	for _, wsID := range []string{"ws-alpha", "ws-beta", "ws-alpha"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/workspaces/"+wsID+"/events/token", nil)
		req = req.WithContext(middleware.WithUserIdentity(
			req.Context(),
			middleware.UserIdentity{UserID: "user-123", Email: "test@example.com"},
		))
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("token route for %s: expected 200, got %d", wsID, rec.Code)
		}
	}

	want := []string{"ws-alpha", "ws-beta", "ws-alpha"}
	if strings.Join(activated, ",") != strings.Join(want, ",") {
		t.Fatalf("activated = %v, want %v", activated, want)
	}
}

func TestModule_DoesNotActivateEventsRouteBeforeTokenAuth(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Stop()

	tokens, err := realtime.NewTokenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer tokens.Stop()

	getMutations := func(_ string, _ string) ([]rpc.MutationEvent, error) { return nil, nil }
	wsFromCtx := func(_ context.Context) string { return "test-ws" }
	var activated []string
	activate := func(_ context.Context, wsID string) {
		activated = append(activated, wsID)
	}

	mod := NewModule(hub, getMutations, wsFromCtx, activate, tokens)

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/test-ws/events", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("events route without token: expected 401, got %d", rec.Code)
	}
	if len(activated) != 0 {
		t.Fatalf("activated = %v, want no activation before token auth", activated)
	}
}

func TestModule_ActivatesEachAuthorizedEventsClient(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	tokens, err := realtime.NewTokenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer tokens.Stop()

	getMutations := func(_ string, _ string) ([]rpc.MutationEvent, error) { return nil, nil }
	activated := make(chan string, 3)
	activate := func(_ context.Context, wsID string) {
		activated <- wsID
	}

	mod := NewModule(hub, getMutations, middleware.WorkspaceFromContext, activate, tokens)

	wsMux := http.NewServeMux()
	mod.Register(wsMux)
	mux := http.NewServeMux()
	mux.Handle("/api/workspaces/{ws}/", middleware.Workspace(func(id string) bool {
		return id == "ws-alpha" || id == "ws-beta"
	})(wsMux))

	for _, wsID := range []string{"ws-alpha", "ws-alpha", "ws-beta"} {
		token, err := tokens.Generate("user-123", wsID)
		if err != nil {
			t.Fatalf("Generate token for %s: %v", wsID, err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest(
			"GET",
			"/api/workspaces/"+wsID+"/events?token="+url.QueryEscape(token),
			nil,
		).WithContext(ctx)
		rec := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			defer close(done)
			mux.ServeHTTP(rec, req)
		}()

		select {
		case got := <-activated:
			if got != wsID {
				t.Fatalf("activated workspace = %q, want %q", got, wsID)
			}
		case <-time.After(time.Second):
			t.Fatalf("events route for %s did not activate", wsID)
		}

		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("events route for %s did not exit after request cancellation", wsID)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("events route for %s: expected 200, got %d", wsID, rec.Code)
		}
	}
}

func TestModule_ActivatesWorkspaceOnEventsRoute(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	getMutations := func(_ string, _ string) ([]rpc.MutationEvent, error) { return nil, nil }
	wsFromCtx := func(_ context.Context) string { return "test-ws" }
	var activated []string
	activate := func(_ context.Context, wsID string) {
		activated = append(activated, wsID)
	}

	mod := NewModule(hub, getMutations, wsFromCtx, activate, nil)

	mux := http.NewServeMux()
	mod.Register(mux)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/workspaces/test-ws/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		mux.ServeHTTP(rec, req)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("events route did not exit after request cancellation")
	}

	if len(activated) != 1 || activated[0] != "test-ws" {
		t.Fatalf("activated = %v, want [test-ws]", activated)
	}
}

func containsAll(s string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(s, needle) {
			return false
		}
	}
	return true
}
