package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
	"github.com/tysonthomas9/loomcli/internal/workspace"
)

func TestAgentTerminalInfoAndTokenHandlers(t *testing.T) {
	svc := &fakeAgentTerminalService{
		info:  &service.AgentTerminalInfoResult{Agent: "nova", Mode: service.AgentTerminalModeTmux},
		token: "token-1",
	}

	req := httptest.NewRequest(http.MethodGet, "/agents/nova/terminal", nil)
	req.SetPathValue("name", "nova")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
	rec := httptest.NewRecorder()
	HandleGetAgentTerminalInfo(svc).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("info status=%d body=%s", rec.Code, rec.Body.String())
	}
	var infoBody agentTerminalInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &infoBody); err != nil {
		t.Fatalf("decode info: %v", err)
	}
	if !infoBody.Success || infoBody.Data == nil || infoBody.Data.Agent != "nova" || infoBody.Data.Mode != service.AgentTerminalModeTmux {
		t.Fatalf("info body = %#v", infoBody)
	}

	req = httptest.NewRequest(http.MethodPost, "/agents/nova/terminal/token", nil)
	req.SetPathValue("name", "nova")
	ctx := middleware.WithWorkspace(req.Context(), "WS")
	ctx = middleware.WithUserIdentity(ctx, middleware.UserIdentity{UserID: "user-1"})
	req = req.WithContext(ctx)
	rec = httptest.NewRecorder()
	HandleGetAgentTerminalToken(svc).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("token status=%d cache=%q body=%s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
	}
	if svc.lastWorkspace != "WS" || svc.lastAgent != "nova" || svc.lastUser != "user-1" {
		t.Fatalf("service call workspace=%q agent=%q user=%q", svc.lastWorkspace, svc.lastAgent, svc.lastUser)
	}
	var tokenBody agentTerminalTokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &tokenBody); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if !tokenBody.Success || tokenBody.Data == nil || tokenBody.Data.Token != "token-1" {
		t.Fatalf("token body = %#v", tokenBody)
	}
}

func TestAgentTerminalHandlersMapServiceErrors(t *testing.T) {
	for _, tt := range []struct {
		name     string
		infoErr  error
		tokenErr error
		handler  func(service.AgentService) http.HandlerFunc
		want     int
	}{
		{name: "info service error", infoErr: service.ErrNotFound("missing"), handler: HandleGetAgentTerminalInfo, want: http.StatusNotFound},
		{name: "token generic error", tokenErr: errors.New("boom"), handler: HandleGetAgentTerminalToken, want: http.StatusInternalServerError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeAgentTerminalService{infoErr: tt.infoErr, tokenErr: tt.tokenErr}
			req := httptest.NewRequest(http.MethodGet, "/agents/bad/terminal", nil)
			req.SetPathValue("name", "bad")
			req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
			rec := httptest.NewRecorder()
			tt.handler(svc).ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status=%d body=%s, want %d", rec.Code, rec.Body.String(), tt.want)
			}
		})
	}
}

func TestValidateAgentWSRequest(t *testing.T) {
	auth, err := realtime.NewTerminalAuth()
	if err != nil {
		t.Fatalf("NewTerminalAuth: %v", err)
	}
	token, err := auth.GenerateToken(agentLogTokenScope("nova"), "WS", "user-1")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	manager := &webuterminal.AgentTmuxManager{}

	for _, tt := range []struct {
		name       string
		agentName  string
		manager    *webuterminal.AgentTmuxManager
		auth       *realtime.TerminalAuth
		token      string
		wantStatus int
		wantOK     bool
	}{
		{name: "missing agent", manager: manager, auth: auth, token: token, wantStatus: http.StatusBadRequest},
		{name: "invalid agent", agentName: "../bad", manager: manager, auth: auth, token: token, wantStatus: http.StatusBadRequest},
		{name: "nil manager", agentName: "nova", auth: auth, token: token, wantStatus: http.StatusServiceUnavailable},
		{name: "nil auth", agentName: "nova", manager: manager, token: token, wantStatus: http.StatusServiceUnavailable},
		{name: "bad token", agentName: "nova", manager: manager, auth: auth, token: "bad", wantStatus: http.StatusUnauthorized},
		{name: "ok", agentName: "nova", manager: manager, auth: auth, token: token, wantOK: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/ws?token="+tt.token, nil)
			req.SetPathValue("name", tt.agentName)
			req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
			rec := httptest.NewRecorder()
			got, ok := validateAgentWSRequest(rec, req, tt.manager, tt.auth)
			if ok != tt.wantOK {
				t.Fatalf("ok=%v got=%q status=%d body=%s", ok, got, rec.Code, rec.Body.String())
			}
			if !tt.wantOK && rec.Code != tt.wantStatus {
				t.Fatalf("status=%d body=%s, want %d", rec.Code, rec.Body.String(), tt.wantStatus)
			}
		})
	}
}

func TestResolveAgentSessionWithFakeTmux(t *testing.T) {
	dir := t.TempDir()
	tmuxPath := writeHandlerFakeTmux(t, dir)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", filepath.Dir(tmuxPath)+string(os.PathListSeparator)+oldPath)
	wsPrefix := workspace.ShortWorkspaceID("WORKSPACE-123")
	sessions := filepath.Join(dir, "sessions")
	if err := os.WriteFile(sessions, []byte(strings.Join([]string{
		"loom-" + wsPrefix + "-lead-nova-10\t10",
		"loom-" + wsPrefix + "-worker-nova-20\t20",
		"loom-other-worker-nova-99\t99",
	}, "\n")), 0600); err != nil {
		t.Fatalf("write sessions: %v", err)
	}
	t.Setenv("TMUX_SESSIONS", sessions)
	manager, err := webuterminal.NewAgentTmuxManager(10)
	if err != nil {
		t.Fatalf("NewAgentTmuxManager: %v", err)
	}

	rec := httptest.NewRecorder()
	got, err := resolveAgentSession(rec, manager, "WORKSPACE-123", "nova")
	if err != nil || got != "loom-"+wsPrefix+"-worker-nova-20" {
		t.Fatalf("resolveAgentSession = %q err=%v body=%s", got, err, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	if _, err := resolveAgentSession(rec, manager, "WORKSPACE-123", "missing"); err == nil || rec.Code != http.StatusNotFound {
		t.Fatalf("missing resolve err=%v status=%d body=%s", err, rec.Code, rec.Body.String())
	}
	t.Setenv("TMUX_LIST_MODE", "hard-error")
	manager, err = webuterminal.NewAgentTmuxManager(10)
	if err != nil {
		t.Fatalf("NewAgentTmuxManager hard-error: %v", err)
	}
	rec = httptest.NewRecorder()
	if _, err := resolveAgentSession(rec, manager, "WORKSPACE-123", "nova"); err == nil || rec.Code != http.StatusInternalServerError {
		t.Fatalf("error resolve err=%v status=%d body=%s", err, rec.Code, rec.Body.String())
	}
}

func TestAgentTerminalPureHelpers(t *testing.T) {
	if got := agentLogTokenScope("nova"); got != "agent:nova:logs" {
		t.Fatalf("agentLogTokenScope = %q", got)
	}
	emitAgentDisconnectSpan(context.Background(), "WS", "nova", "tmux-session", realtime.WSCloseSessionKilled)
}

type fakeAgentTerminalService struct {
	info          *service.AgentTerminalInfoResult
	infoErr       error
	token         string
	tokenErr      error
	lastWorkspace string
	lastAgent     string
	lastUser      string
}

func (f *fakeAgentTerminalService) GetTerminalInfo(_ context.Context, wsID, agentName string) (*service.AgentTerminalInfoResult, error) {
	f.lastWorkspace, f.lastAgent = wsID, agentName
	if f.infoErr != nil {
		return nil, f.infoErr
	}
	return f.info, nil
}
func (f *fakeAgentTerminalService) GenerateTerminalToken(_ context.Context, wsID, agentName, userID string) (string, error) {
	f.lastWorkspace, f.lastAgent, f.lastUser = wsID, agentName, userID
	return f.token, f.tokenErr
}
func (f *fakeAgentTerminalService) GetLog(context.Context, string, string, int, int64) (*service.AgentLogResult, error) {
	return nil, nil
}
func (f *fakeAgentTerminalService) GetDiffStat(context.Context, string, string) (*service.AgentDiffStatResult, error) {
	return nil, nil
}
func (f *fakeAgentTerminalService) GitPush(context.Context, string, string, string) (*ops.GitPushResult, error) {
	return nil, nil
}
func (f *fakeAgentTerminalService) GitPushAll(context.Context, string) (*service.GitPushAllResult, error) {
	return nil, nil
}
func (f *fakeAgentTerminalService) GitPull(context.Context, string, string, string) (*ops.GitPullResult, error) {
	return nil, nil
}
func (f *fakeAgentTerminalService) GitSync(context.Context, string, string) (*service.GitSyncResult, error) {
	return nil, nil
}
func (f *fakeAgentTerminalService) CreatePR(context.Context, string, string, string) (*ops.GitPRResult, error) {
	return nil, nil
}
func (f *fakeAgentTerminalService) GitReset(context.Context, string, string, string, bool, bool) (*ops.GitResetResult, error) {
	return nil, nil
}
func (f *fakeAgentTerminalService) GitStatus(context.Context, string, string) (*ops.GitStatusResult, error) {
	return nil, nil
}
func (f *fakeAgentTerminalService) SetTargetBranch(context.Context, string, string, string) error {
	return nil
}
func (f *fakeAgentTerminalService) ListAgents(context.Context, string) ([]*domain.Agent, error) {
	return nil, nil
}
func (f *fakeAgentTerminalService) CreateAgent(context.Context, service.AgentCreateInput) (*domain.Agent, error) {
	return nil, nil
}
func (f *fakeAgentTerminalService) UpdateAgent(context.Context, string, string, service.AgentUpdateInput) (*domain.Agent, error) {
	return nil, nil
}
func (f *fakeAgentTerminalService) RequestAgentLifecycle(context.Context, string, string, service.AgentLifecycleInput) (*domain.Agent, error) {
	return nil, nil
}
func (f *fakeAgentTerminalService) DeleteAgent(context.Context, string, string) error {
	return nil
}

func writeHandlerFakeTmux(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "tmux")
	content := `#!/bin/sh
case "$1" in
  list-sessions)
    case "$TMUX_LIST_MODE" in
      hard-error) echo "tmux failed" >&2; exit 2 ;;
    esac
    cat "$TMUX_SESSIONS"
    exit 0
    ;;
esac
exit 0
`
	if err := os.WriteFile(path, []byte(content), 0700); err != nil {
		t.Fatalf("write tmux: %v", err)
	}
	return path
}
