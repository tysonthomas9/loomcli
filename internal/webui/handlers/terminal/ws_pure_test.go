package terminal

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"nhooyr.io/websocket" //nolint:staticcheck

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

func TestTerminalWSPureHelpers(t *testing.T) {
	if wsCloseReason(websocket.StatusNormalClosure) != wsDisconnectReasonClientClose ||
		wsCloseReason(websocket.StatusGoingAway) != wsDisconnectReasonServerClose ||
		wsCloseReason(websocket.StatusCode(realtime.WSCloseBackendExited)) != wsDisconnectReasonBackendExited ||
		wsCloseReason(websocket.StatusCode(realtime.WSCloseSessionKilled)) != wsDisconnectReasonSessionKilled ||
		wsCloseReason(websocket.StatusPolicyViolation) != wsDisconnectReasonError {
		t.Fatal("wsCloseReason mapping mismatch")
	}
	if !isUUIDTerminalSession("term_abc") || isUUIDTerminalSession("shell") {
		t.Fatal("uuid terminal detection mismatch")
	}
	if legacyLaunchSpecForSession("lead-codex-1") == nil {
		t.Fatal("expected legacy launch spec for lead session")
	}
	if got := mustUint16(42); got != 42 {
		t.Fatalf("mustUint16 = %d", got)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("mustUint16 out of range did not panic")
		}
	}()
	_ = mustUint16(1 << 20)
}

func TestClassifyAttachErr(t *testing.T) {
	tests := []struct {
		err    error
		status websocket.StatusCode
		reason string
	}{
		{errAgentLaunchSpecMissing, websocket.StatusInternalError, errAgentLaunchSpecMissing.Error()},
		{errTerminalLaunchMetaMissing, websocket.StatusInternalError, errTerminalLaunchMetaMissing.Error()},
		{webuterminal.ErrPTYMaxSessionsReached, websocket.StatusInternalError, webuterminal.ErrPTYMaxSessionsReached.Error()},
		{webuterminal.ErrPTYManagerClosed, websocket.StatusGoingAway, "workspace unavailable"},
		{webuterminal.ErrWorkspaceNotRegistered, websocket.StatusGoingAway, "workspace unavailable"},
		{errors.New("boom"), websocket.StatusInternalError, "boom"},
	}
	for _, tt := range tests {
		status, reason := classifyAttachErr(tt.err, "sess", "WS")
		if status != tt.status || reason != tt.reason {
			t.Fatalf("classifyAttachErr(%v) = %v/%q, want %v/%q", tt.err, status, reason, tt.status, tt.reason)
		}
	}
}

func TestValidateTerminalWSRequestFailures(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
	rec := httptest.NewRecorder()
	if _, _, ok := validateTerminalWSRequest(rec, req, nil, nil); ok || rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil manager ok=%v status=%d", ok, rec.Code)
	}

	manager := fakePTYSource{max: 1, count: 1}
	for _, tt := range []struct {
		name   string
		target string
		code   int
	}{
		{"missing session", "/ws", http.StatusBadRequest},
		{"bad session", "/ws?session=../bad", http.StatusBadRequest},
		{"max sessions", "/ws?session=new", http.StatusServiceUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
			_, _, ok := validateTerminalWSRequest(rec, req, manager, nil)
			if ok || rec.Code != tt.code {
				t.Fatalf("ok=%v status=%d body=%s", ok, rec.Code, rec.Body.String())
			}
		})
	}
	manager.has = true
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/ws?session=existing", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
	session, workspace, ok := validateTerminalWSRequest(rec, req, manager, nil)
	if !ok || session != "existing" || workspace != "WS" {
		t.Fatalf("session=%q workspace=%q ok=%v status=%d", session, workspace, ok, rec.Code)
	}
}

type fakePTYSource struct {
	has   bool
	count int
	max   int
}

func (f fakePTYSource) AttachSession(webuterminal.SessionKey, uint16, uint16, *webuterminal.LaunchSpec) (webuterminal.Attachment, bool, error) {
	return nil, false, nil
}
func (f fakePTYSource) Detach(webuterminal.SessionKey, string)  {}
func (f fakePTYSource) Kill(webuterminal.SessionKey) error      { return nil }
func (f fakePTYSource) HasSession(webuterminal.SessionKey) bool { return f.has }
func (f fakePTYSource) SessionClosed(webuterminal.SessionKey) bool {
	return false
}
func (f fakePTYSource) AttachmentCount(webuterminal.SessionKey) int { return 0 }
func (f fakePTYSource) SessionCount() int                           { return f.count }
func (f fakePTYSource) SessionCountFor(string) int                  { return f.count }
func (f fakePTYSource) MaxSessions() int                            { return f.max }
