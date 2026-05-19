package terminal

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nhooyr.io/websocket" //nolint:staticcheck

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
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

func TestTerminalWSAuthAndSmallHelpers(t *testing.T) {
	auth, err := realtime.NewTerminalAuth()
	if err != nil {
		t.Fatalf("NewTerminalAuth: %v", err)
	}
	t.Cleanup(auth.Stop)

	token, err := auth.GenerateToken("main", "WS", "user-1")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ws?token="+token, nil)
	if !authenticateTerminalSession(rec, req, auth, "main", "WS") {
		t.Fatalf("valid token rejected, status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/ws?token=bad", nil)
	if authenticateTerminalSession(rec, req, auth, "main", "WS") || rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token accepted or wrong status=%d body=%s", rec.Code, rec.Body.String())
	}

	var att fakeAttachment
	n, err := (attachmentWriter{a: &att}).Write([]byte("hello"))
	if err != nil || n != 5 || string(att.written) != "hello" {
		t.Fatalf("attachmentWriter.Write n=%d err=%v written=%q", n, err, att.written)
	}

	emitDisconnectSpan(context.Background(), "WS", "main", websocket.StatusInternalError)

	if got := workspaceNameFromStore(context.Background(), nil, "WS"); got != "" {
		t.Fatalf("nil store workspace name = %q", got)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "WS", Name: "Workspace One"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if got := workspaceNameFromStore(context.Background(), st, "WS"); got != "Workspace One" {
		t.Fatalf("workspaceNameFromStore = %q", got)
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

func TestHandleTerminalWSValidationBranch(t *testing.T) {
	handler := HandleTerminalWS(nil, nil, nil, "", nil, nil, nil, time.Now())
	req := httptest.NewRequest(http.MethodGet, "/ws?session=shell", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("HandleTerminalWS status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTerminalWSUpgradeFailureBranches(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ws?session=shell", nil)
	rec := httptest.NewRecorder()
	if conn, ok := upgradeTerminalWS(rec, req, nil); ok || conn != nil {
		t.Fatalf("upgradeTerminalWS succeeded unexpectedly")
	}
	if rec.Code != http.StatusUpgradeRequired {
		t.Fatalf("upgradeTerminalWS status = %d, want %d", rec.Code, http.StatusUpgradeRequired)
	}

	rec = httptest.NewRecorder()
	if conn, _, ok := upgradeTerminalWithSpan(rec, req, nil, "shell", "WS"); ok || conn != nil {
		t.Fatalf("upgradeTerminalWithSpan succeeded unexpectedly")
	}
	if rec.Code != http.StatusUpgradeRequired {
		t.Fatalf("upgradeTerminalWithSpan status = %d, want %d", rec.Code, http.StatusUpgradeRequired)
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

type fakeAttachment struct {
	written []byte
	out     chan []byte
}

func (f *fakeAttachment) ConnID() string { return "conn-1" }
func (f *fakeAttachment) Output() <-chan []byte {
	if f.out == nil {
		f.out = make(chan []byte)
	}
	return f.out
}
func (f *fakeAttachment) WriteInput(p []byte) (int, error) {
	f.written = append(f.written, p...)
	return len(p), nil
}
func (f *fakeAttachment) Scrollback() []byte { return nil }
func (f *fakeAttachment) Resize(string, uint16, uint16) error {
	return nil
}
func (f *fakeAttachment) ExitReason() string { return "" }
