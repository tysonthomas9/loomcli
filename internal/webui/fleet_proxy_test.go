package webui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewFleetDBProxy_DisabledWhenEmptyOrInvalid(t *testing.T) {
	if NewFleetDBProxy("", nil) != nil {
		t.Error("empty URL must disable the proxy (nil)")
	}
	if NewFleetDBProxy("://nope", nil) != nil {
		t.Error("invalid URL must disable the proxy (nil)")
	}
	if NewFleetDBProxy("just-a-path", nil) != nil {
		t.Error("URL without scheme/host must disable the proxy (nil)")
	}
}

// TestFleetDBProxy_ForwardsCallerCredential is the security contract: the proxy
// passes the caller's own X-API-Key / X-Actor through to fleet-db verbatim and
// substitutes NO identity of its own — so fleet-db's RBAC authorizes the
// caller's scoped key. It also preserves the full /api/v1/... path and method.
func TestFleetDBProxy_ForwardsCallerCredential(t *testing.T) {
	var gotKey, gotActor, gotPath, gotMethod, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		gotActor = r.Header.Get("X-Actor")
		gotPath = r.URL.Path
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	proxy := NewFleetDBProxy(upstream.URL, nil)
	if proxy == nil {
		t.Fatal("proxy unexpectedly nil for a valid URL")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/E2E/issues", strings.NewReader(`{"title":"x"}`))
	req.Header.Set("X-API-Key", "sk-developer-scoped")
	req.Header.Set("X-Actor", "sandbox:E2E:coder:1")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotKey != "sk-developer-scoped" {
		t.Errorf("upstream X-API-Key = %q, want the caller's key (no substitution)", gotKey)
	}
	if gotActor != "sandbox:E2E:coder:1" {
		t.Errorf("upstream X-Actor = %q, want the caller's actor", gotActor)
	}
	if gotPath != "/api/v1/E2E/issues" {
		t.Errorf("upstream path = %q, want /api/v1/E2E/issues (path preserved)", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("upstream method = %q, want POST", gotMethod)
	}
	if gotBody != `{"title":"x"}` {
		t.Errorf("upstream body = %q, want the forwarded body", gotBody)
	}
}
