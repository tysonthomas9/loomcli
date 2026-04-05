package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// Compile-time assertion: *SSEModule implements Module.
var _ Module = (*SSEModule)(nil)

func TestSSEModule_RegisterRoutes(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Stop()

	tokens, err := realtime.NewTokenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer tokens.Stop()

	getMutations := func(_ string, _ int64) []rpc.MutationEvent { return nil }
	wsFromCtx := func(_ context.Context) string { return "test-ws" }

	mod := NewSSEModule(hub, getMutations, wsFromCtx, tokens)

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

func TestSSEModule_ConditionalRoutes(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Stop()

	getMutations := func(_ string, _ int64) []rpc.MutationEvent { return nil }
	wsFromCtx := func(_ context.Context) string { return "test-ws" }

	t.Run("nil sseTokens omits token route", func(t *testing.T) {
		mod := NewSSEModule(hub, getMutations, wsFromCtx, nil)

		mux := http.NewServeMux()
		mod.Register(mux)

		// Token route should NOT be registered
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/workspaces/test-ws/events/token", nil)
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("token route with nil sseTokens: expected 404, got %d", rec.Code)
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

func TestSSEModule_WrongMethod_Returns405(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Stop()

	tokens, err := realtime.NewTokenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer tokens.Stop()

	getMutations := func(_ string, _ int64) []rpc.MutationEvent { return nil }
	wsFromCtx := func(_ context.Context) string { return "test-ws" }

	mod := NewSSEModule(hub, getMutations, wsFromCtx, tokens)

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/test-ws/events/token", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST .../events/token: expected 405, got %d", rec.Code)
	}
}
