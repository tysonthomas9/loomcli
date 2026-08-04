package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeadersAllowsWasmForTerminalRenderer(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler := SecurityHeaders(SecurityConfig{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	handler.ServeHTTP(rr, req)

	csp := rr.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src") {
		t.Fatalf("CSP missing script-src: %q", csp)
	}
	if !strings.Contains(csp, "'wasm-unsafe-eval'") {
		t.Fatalf("CSP script-src missing wasm-unsafe-eval: %q", csp)
	}
}
