package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// testModule is a simple Module that registers a single GET route.
type testModule struct {
	pattern string
	body    string
}

func (m *testModule) Register(mux *http.ServeMux) {
	body := m.body
	mux.HandleFunc(m.pattern, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, body)
	})
}

// emptyModule is a Module with a no-op Register.
type emptyModule struct{}

func (emptyModule) Register(_ *http.ServeMux) {}

func TestModuleInterface_RegisterAddsRoutes(t *testing.T) {
	mod := &testModule{pattern: "GET /test-ping", body: "pong"}
	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test-ping", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "pong" {
		t.Fatalf("expected body %q, got %q", "pong", rec.Body.String())
	}
}

func TestModuleInterface_MultipleModules(t *testing.T) {
	modA := &testModule{pattern: "GET /mod-a", body: "a"}
	modB := &testModule{pattern: "GET /mod-b", body: "b"}

	mux := http.NewServeMux()
	modA.Register(mux)
	modB.Register(mux)

	tests := []struct {
		path     string
		wantCode int
		wantBody string
	}{
		{"/mod-a", http.StatusOK, "a"},
		{"/mod-b", http.StatusOK, "b"},
		{"/mod-c", http.StatusNotFound, ""},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, tt.path, nil)
		mux.ServeHTTP(rec, req)

		if rec.Code != tt.wantCode {
			t.Errorf("%s: expected status %d, got %d", tt.path, tt.wantCode, rec.Code)
		}
		if tt.wantBody != "" && rec.Body.String() != tt.wantBody {
			t.Errorf("%s: expected body %q, got %q", tt.path, tt.wantBody, rec.Body.String())
		}
	}
}

func TestModuleInterface_EmptyRegister(t *testing.T) {
	mod := emptyModule{}
	mux := http.NewServeMux()
	mod.Register(mux) // must not panic
}
