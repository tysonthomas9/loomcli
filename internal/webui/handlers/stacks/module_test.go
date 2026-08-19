package stacks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type stubStackService struct{}

func (s stubStackService) ListStacks(context.Context, string) (*service.WorkspaceStacksResult, error) {
	return &service.WorkspaceStacksResult{Stacks: []service.WorkspaceStack{}}, nil
}

func TestModuleRegisterRoutes(t *testing.T) {
	mod := NewModule(stubStackService{})
	mux := http.NewServeMux()
	mod.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws/stacks", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "ws"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatal("GET /stacks was not registered")
	}
	if rec.Code == http.StatusMethodNotAllowed {
		t.Fatal("GET /stacks registered with wrong method")
	}
}
