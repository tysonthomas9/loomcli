package terminal

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJSONRequestHandlersRejectTrailingValues(t *testing.T) {
	tests := []struct {
		name   string
		handle http.HandlerFunc
		method string
		path   string
		body   string
	}{
		{
			name:   "terminal setup",
			handle: HandleStartTerminalSetup(nil),
			method: http.MethodPost,
			path:   "/api/workspaces/TEST/terminal/setup",
			body:   `{"backend":"codex","action":"login"} {}`,
		},
		{
			name:   "patch terminal tab",
			handle: HandlePatchTerminalTab(nil),
			method: http.MethodPatch,
			path:   "/api/workspaces/TEST/terminal/tabs/test",
			body:   `{"label":"test"} {}`,
		},
		{
			name:   "put terminal tab",
			handle: HandlePutTerminalTab(nil),
			method: http.MethodPut,
			path:   "/api/workspaces/TEST/terminal/tabs/test",
			body:   `{"label":"test"} {}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			rec := httptest.NewRecorder()
			test.handle.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestTerminalSetupRejectsCallerSuppliedCommand(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/workspaces/TEST/terminal/setup",
		strings.NewReader(`{"backend":"codex","action":"login","command":"rm -rf /"}`),
	)
	rec := httptest.NewRecorder()
	HandleStartTerminalSetup(nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}
