package misc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Compile-time assertion: *FileModule implements the module interface.
var _ module = (*FileModule)(nil)

func TestFileModule_RegisterRoutes(t *testing.T) {
	mod := NewFileModule(&stubFileService{})

	mux := http.NewServeMux()
	mod.Register(mux)

	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/workspaces/test-ws/files/capabilities"},
		{"GET", "/api/workspaces/test-ws/files/git-status"},
		{"GET", "/api/workspaces/test-ws/files/checkouts"},
		{"POST", "/api/workspaces/test-ws/files/checkouts/repair"},
		{"GET", "/api/workspaces/test-ws/files/diff"},
		{"GET", "/api/workspaces/test-ws/files/history"},
		{"GET", "/api/workspaces/test-ws/files/blame"},
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

func TestFileModule_NilDeps(t *testing.T) {
	mod := NewFileModule(nil)

	mux := http.NewServeMux()
	mod.Register(mux) // must not panic during registration
}

func TestFileModule_ViewerCapabilities(t *testing.T) {
	accessCfg := middleware.FileAccessConfig{
		RemoteAuth: true,
		ResolveRole: func(context.Context, string, middleware.UserIdentity) (string, error) {
			return "viewer", nil
		},
	}
	mod := NewFileModule(&stubFileService{}, accessCfg)
	mux := http.NewServeMux()
	mod.Register(mux)

	capReq := authorizedFileModuleRequest(http.MethodGet, "/api/workspaces/ws/files/capabilities")
	capRR := httptest.NewRecorder()
	mux.ServeHTTP(capRR, capReq)
	if capRR.Code != http.StatusOK {
		t.Fatalf("capabilities status=%d body=%s", capRR.Code, capRR.Body.String())
	}
	var capabilities service.FileCapabilities
	if err := json.NewDecoder(capRR.Body).Decode(&capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities != (service.FileCapabilities{Read: true}) {
		t.Fatalf("capabilities=%+v", capabilities)
	}
}

func authorizedFileModuleRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	ctx := middleware.WithWorkspace(req.Context(), "ws")
	ctx = middleware.WithUserIdentity(ctx, middleware.UserIdentity{UserID: "viewer-1"})
	return req.WithContext(ctx)
}
