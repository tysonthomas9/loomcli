package terminal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestTerminalSimpleHandlers(t *testing.T) {
	svc := &fakeTerminalService{activeTab: "main"}
	req := httptest.NewRequest(http.MethodGet, "/token?session=main", nil)
	ctx := middleware.WithWorkspace(req.Context(), "WS1")
	ctx = middleware.WithUserIdentity(ctx, middleware.UserIdentity{UserID: "user-1"})
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	HandleTerminalToken(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("token status=%d headers=%v body=%s", rr.Code, rr.Header(), rr.Body.String())
	}
	if svc.tokenWS != "WS1" || svc.tokenSession != "main" || svc.tokenUser != "user-1" {
		t.Fatalf("token call ws=%q session=%q user=%q", svc.tokenWS, svc.tokenSession, svc.tokenUser)
	}
	var token map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &token); err != nil || token["token"] != "token" {
		t.Fatalf("token response = %#v err=%v", token, err)
	}

	req = httptest.NewRequest(http.MethodGet, "/state", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS1"))
	rr = httptest.NewRecorder()
	HandleGetTerminalState(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"active_tab":"main"`) {
		t.Fatalf("get state status=%d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodPatch, "/state", strings.NewReader(`{"active_tab":"logs"}`))
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS1"))
	rr = httptest.NewRecorder()
	HandlePatchTerminalState(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || svc.patchWS != "WS1" || svc.patchTab != "logs" {
		t.Fatalf("patch state status=%d ws=%q tab=%q body=%s", rr.Code, svc.patchWS, svc.patchTab, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	HandleListSessionsByIssue(svc).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/by-issue", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "ISSUE-1") {
		t.Fatalf("sessions by issue status=%d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/setup", strings.NewReader(`{"backend":"codex","action":"login"}`))
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS1"))
	rr = httptest.NewRecorder()
	HandleStartTerminalSetup(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || svc.setupWS != "WS1" || svc.setupReq.Backend != "codex" || svc.setupReq.Action != "login" {
		t.Fatalf("setup status=%d ws=%q req=%+v body=%s", rr.Code, svc.setupWS, svc.setupReq, rr.Body.String())
	}
}

func TestTerminalSimpleHandlerErrorsAndOriginHosts(t *testing.T) {
	svc := &fakeTerminalService{err: service.ErrValidation("bad terminal")}

	req := httptest.NewRequest(http.MethodPost, "/setup", strings.NewReader(`not-json`))
	rr := httptest.NewRecorder()
	HandleStartTerminalSetup(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid setup status = %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodPatch, "/state", strings.NewReader(`not-json`))
	rr = httptest.NewRecorder()
	HandlePatchTerminalState(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid patch status = %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	HandleGetTerminalState(svc).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/state", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("service error status = %d", rr.Code)
	}

	hosts := originHosts([]string{"http://localhost:3000", "https://example.com/path", "://bad", "no-host"})
	if len(hosts) != 2 || hosts[0] != "localhost:3000" || hosts[1] != "example.com" {
		t.Fatalf("origin hosts = %#v", hosts)
	}
	if got := originHosts(nil); got != nil {
		t.Fatalf("nil origins = %#v, want nil", got)
	}
}

func TestTabModuleRegistersSimpleRoutes(t *testing.T) {
	svc := &fakeTerminalService{activeTab: "main"}
	mux := http.NewServeMux()
	NewTabModule(svc).Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/WS1/terminal/state", nil)
	req.SetPathValue("ws", "WS1")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS1"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "main") {
		t.Fatalf("registered state route status=%d body=%s", rr.Code, rr.Body.String())
	}
}
