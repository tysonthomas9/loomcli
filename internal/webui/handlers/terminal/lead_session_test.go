package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// mockLeadService implements TerminalService for HandleCreateLeadSession tests.
// Only CreateLeadSession is used; other methods panic.
type mockLeadService struct {
	leadFunc func(ctx context.Context, wsID string, params *LeadSessionParams) (*LeadSessionResult, error)
}

func (m *mockLeadService) CreateLeadSession(ctx context.Context, wsID string, params *LeadSessionParams) (*LeadSessionResult, error) {
	if m.leadFunc != nil {
		return m.leadFunc(ctx, wsID, params)
	}
	return nil, service.ErrUnavailable("not configured")
}

func (m *mockLeadService) SpawnSession(_ context.Context, _ string, _ *SpawnParams) (*SpawnResult, error) {
	panic("not implemented")
}
func (m *mockLeadService) GenerateToken(_ context.Context, _, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockLeadService) RestartSession(_ context.Context, _, _ string) (*TerminalRestartResult, error) {
	panic("not implemented")
}
func (m *mockLeadService) KillSession(_ context.Context, _ string) error {
	panic("not implemented")
}
func (m *mockLeadService) GetSessionStatus(_ context.Context, _ string) (*TerminalStatusResult, error) {
	panic("not implemented")
}
func (m *mockLeadService) ListSessions(_ context.Context, _ string) ([]TerminalSessionInfo, error) {
	panic("not implemented")
}
func (m *mockLeadService) SeedSession(_ context.Context, _ string, _ *SeedParams) error {
	panic("not implemented")
}
func (m *mockLeadService) ScheduleKill(_ context.Context, _ string) error {
	panic("not implemented")
}
func (m *mockLeadService) CloseAllSessions(_ context.Context, _ string) (*CloseAllResult, error) {
	panic("not implemented")
}
func (m *mockLeadService) ExportSession(_ context.Context, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockLeadService) GetScrollbackInfo(_ context.Context, _ string) (*ScrollbackInfoResult, error) {
	panic("not implemented")
}
func (m *mockLeadService) GetScrollback(_ context.Context, _ string) (*ScrollbackResult, error) {
	panic("not implemented")
}
func (m *mockLeadService) ListTabs(_ context.Context, _ string) ([]tabmeta.TabMetadata, error) {
	panic("not implemented")
}
func (m *mockLeadService) GetTab(_ context.Context, _, _ string) (*tabmeta.TabMetadata, error) {
	panic("not implemented")
}
func (m *mockLeadService) PatchTab(_ context.Context, _, _ string, _ map[string]string) (*PatchTabResult, error) {
	panic("not implemented")
}
func (m *mockLeadService) PutTab(_ context.Context, _ string, _ *tabmeta.TabMetadata) error {
	panic("not implemented")
}
func (m *mockLeadService) DeleteTab(_ context.Context, _, _ string) error {
	panic("not implemented")
}
func (m *mockLeadService) ListSessionsByIssue(_ context.Context) (map[string][]string, error) {
	panic("not implemented")
}
func (m *mockLeadService) GetTerminalState(_ context.Context, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockLeadService) PatchTerminalState(_ context.Context, _, _ string) error {
	panic("not implemented")
}

func postLeadSession(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/terminal/lead-session", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestHandleCreateLeadSession_HappyPath(t *testing.T) {
	var captured *LeadSessionParams
	svc := &mockLeadService{
		leadFunc: func(_ context.Context, _ string, p *LeadSessionParams) (*LeadSessionResult, error) {
			captured = p
			return &LeadSessionResult{SessionName: "lead-claude-123", Backend: "claude"}, nil
		},
	}
	h := HandleCreateLeadSession(svc)

	rr := postLeadSession(h, `{"message":"add dark mode","backend":"claude"}`)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	var resp leadSessionResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success || resp.Data == nil {
		t.Fatalf("expected success, got error=%q", resp.Error)
	}
	if resp.Data.SessionName != "lead-claude-123" || resp.Data.Backend != "claude" {
		t.Errorf("unexpected data: %+v", resp.Data)
	}
	if captured == nil || captured.Message != "add dark mode" || captured.Backend != "claude" {
		t.Errorf("unexpected captured params: %+v", captured)
	}
}

func TestHandleCreateLeadSession_MalformedJSON(t *testing.T) {
	h := HandleCreateLeadSession(&mockLeadService{})
	rr := postLeadSession(h, `{not json`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateLeadSession_ValidationError(t *testing.T) {
	svc := &mockLeadService{
		leadFunc: func(_ context.Context, _ string, _ *LeadSessionParams) (*LeadSessionResult, error) {
			return nil, service.ErrValidation("message is required")
		},
	}
	h := HandleCreateLeadSession(svc)
	rr := postLeadSession(h, `{"backend":"claude"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateLeadSession_BackendValidation(t *testing.T) {
	svc := &mockLeadService{
		leadFunc: func(_ context.Context, _ string, _ *LeadSessionParams) (*LeadSessionResult, error) {
			return nil, service.ErrValidation("invalid backend \"bogus\"")
		},
	}
	h := HandleCreateLeadSession(svc)
	rr := postLeadSession(h, `{"message":"hi","backend":"bogus"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateLeadSession_Conflict(t *testing.T) {
	svc := &mockLeadService{
		leadFunc: func(_ context.Context, _ string, _ *LeadSessionParams) (*LeadSessionResult, error) {
			return nil, service.ErrConflict("session already exists")
		},
	}
	h := HandleCreateLeadSession(svc)
	rr := postLeadSession(h, `{"message":"hi","backend":"claude"}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusConflict)
	}
}

func TestHandleCreateLeadSession_ServiceUnavailable(t *testing.T) {
	h := HandleCreateLeadSession(&mockLeadService{})
	rr := postLeadSession(h, `{"message":"hi","backend":"claude"}`)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleCreateLeadSession_SpawnError(t *testing.T) {
	svc := &mockLeadService{
		leadFunc: func(_ context.Context, _ string, _ *LeadSessionParams) (*LeadSessionResult, error) {
			return nil, service.ErrInternal("failed to spawn lead session", errors.New("tmux failed"))
		},
	}
	h := HandleCreateLeadSession(svc)
	rr := postLeadSession(h, `{"message":"hi","backend":"claude"}`)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}

func TestHandleCreateLeadSession_BodyTooLarge(t *testing.T) {
	h := HandleCreateLeadSession(&mockLeadService{})
	payload := make([]byte, handler.MaxRequestBody+1024)
	for i := range payload {
		payload[i] = 'x'
	}
	body := fmt.Sprintf(`{"message":%q,"backend":"claude"}`, payload)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/terminal/lead-session", bytes.NewReader([]byte(body)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusRequestEntityTooLarge)
	}
}
