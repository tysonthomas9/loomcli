package terminal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// --- handleGetAgentTerminalInfo tests ---

// TestHandleGetAgentTerminalInfo_MissingName tests that a missing agent name returns 400.
func TestHandleGetAgentTerminalInfo_MissingName(t *testing.T) {
	svc := &mockAgentService{
		getTerminalInfoFunc: func(_ context.Context, _, agentName string) (*AgentTerminalInfoResult, error) {
			return nil, service.ErrValidation("missing agent name")
		},
	}
	handler := handleGetAgentTerminalInfo(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/agents//terminal/info", nil)
	req.SetPathValue("name", "")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	body := assertJSONResponse(t, w)
	assertEnvelopeError(t, body, "data")
}

// TestHandleGetAgentTerminalInfo_InvalidName tests that invalid agent names return 400.
func TestHandleGetAgentTerminalInfo_InvalidName(t *testing.T) {
	svc := &mockAgentService{
		getTerminalInfoFunc: func(_ context.Context, _, agentName string) (*AgentTerminalInfoResult, error) {
			return nil, service.ErrValidation("invalid agent name: must match [a-zA-Z0-9_-]+")
		},
	}
	handler := handleGetAgentTerminalInfo(svc)

	tests := []struct {
		name      string
		agentName string
	}{
		{"with slash", "bad/agent"},
		{"with dot", "bad.agent"},
		{"with space", "bad agent"},
		{"with special chars", "bad!@#$"},
		{"path traversal", "../../../etc/passwd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/agents/invalid/terminal/info", nil)
			req.SetPathValue("name", tt.agentName)
			req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d for agent %q", w.Code, http.StatusBadRequest, tt.agentName)
			}

			body := assertJSONResponse(t, w)
			assertEnvelopeError(t, body, "data")
		})
	}
}

// TestHandleGetAgentTerminalInfo_NilManager tests that unavailable terminal returns 503.
func TestHandleGetAgentTerminalInfo_NilManager(t *testing.T) {
	svc := &mockAgentService{
		getTerminalInfoFunc: func(_ context.Context, _, _ string) (*AgentTerminalInfoResult, error) {
			return nil, service.ErrUnavailable("terminal manager not initialized")
		},
	}
	handler := handleGetAgentTerminalInfo(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/ember/terminal/info", nil)
	req.SetPathValue("name", "ember")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	body := assertJSONResponse(t, w)
	assertEnvelopeError(t, body, "data")

	if errMsg, _ := body["error"].(string); errMsg != "terminal manager not initialized" {
		t.Errorf("error = %q, want %q", errMsg, "terminal manager not initialized")
	}
}

// TestHandleGetAgentTerminalInfo_ArchiveMode tests that archive mode is returned correctly.
func TestHandleGetAgentTerminalInfo_ArchiveMode(t *testing.T) {
	svc := &mockAgentService{
		getTerminalInfoFunc: func(_ context.Context, _, agentName string) (*AgentTerminalInfoResult, error) {
			return &AgentTerminalInfoResult{Agent: agentName, Mode: agentTerminalModeArchive}, nil
		},
	}
	handler := handleGetAgentTerminalInfo(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/nonexistent-agent-xyz/terminal/info", nil)
	req.SetPathValue("name", "nonexistent-agent-xyz")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	body := assertJSONResponse(t, w)
	assertEnvelopeSuccess(t, body)

	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatal("missing or invalid 'data' field in response")
	}

	if agent, _ := data["agent"].(string); agent != "nonexistent-agent-xyz" {
		t.Errorf("data.agent = %q, want %q", agent, "nonexistent-agent-xyz")
	}

	if mode, _ := data["mode"].(string); mode != agentTerminalModeArchive {
		t.Errorf("data.mode = %q, want %q", mode, agentTerminalModeArchive)
	}
}

// TestHandleGetAgentTerminalInfo_TmuxMode tests that tmux mode is returned correctly.
func TestHandleGetAgentTerminalInfo_TmuxMode(t *testing.T) {
	svc := &mockAgentService{
		getTerminalInfoFunc: func(_ context.Context, _, agentName string) (*AgentTerminalInfoResult, error) {
			return &AgentTerminalInfoResult{Agent: agentName, Mode: agentTerminalModeTmux}, nil
		},
	}
	handler := handleGetAgentTerminalInfo(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/spark/terminal/info", nil)
	req.SetPathValue("name", "spark")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	body := assertJSONResponse(t, w)
	assertEnvelopeSuccess(t, body)

	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatal("missing or invalid 'data' field in response")
	}
	if mode, _ := data["mode"].(string); mode != agentTerminalModeTmux {
		t.Errorf("data.mode = %q, want %q", mode, agentTerminalModeTmux)
	}
}

// TestHandleGetAgentTerminalInfo_ResponseShape verifies the complete JSON envelope shape.
func TestHandleGetAgentTerminalInfo_ResponseShape(t *testing.T) {
	svc := &mockAgentService{
		getTerminalInfoFunc: func(_ context.Context, _, agentName string) (*AgentTerminalInfoResult, error) {
			return &AgentTerminalInfoResult{Agent: agentName, Mode: agentTerminalModeArchive}, nil
		},
	}
	handler := handleGetAgentTerminalInfo(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/spark/terminal/info", nil)
	req.SetPathValue("name", "spark")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp agentTerminalInfoResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Errorf("Success = false, want true")
	}
	if resp.Error != "" {
		t.Errorf("Error = %q, want empty", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("Data = nil, want non-nil")
	}
	if resp.Data.Agent != "spark" {
		t.Errorf("Data.Agent = %q, want %q", resp.Data.Agent, "spark")
	}
	if resp.Data.Mode != agentTerminalModeArchive {
		t.Errorf("Data.Mode = %q, want %q", resp.Data.Mode, agentTerminalModeArchive)
	}
}

// TestHandleGetAgentTerminalInfo_ValidNames tests that various valid agent names are accepted.
func TestHandleGetAgentTerminalInfo_ValidNames(t *testing.T) {
	svc := &mockAgentService{
		getTerminalInfoFunc: func(_ context.Context, _, agentName string) (*AgentTerminalInfoResult, error) {
			return &AgentTerminalInfoResult{Agent: agentName, Mode: agentTerminalModeArchive}, nil
		},
	}
	handler := handleGetAgentTerminalInfo(svc)

	tests := []struct {
		name      string
		agentName string
	}{
		{"alphanumeric", "ember123"},
		{"with hyphen", "test-agent"},
		{"with underscore", "test_agent"},
		{"mixed case", "TestAgent-123"},
		{"single char", "a"},
		{"numbers only", "42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/agents/"+tt.agentName+"/terminal/info", nil)
			req.SetPathValue("name", tt.agentName)
			req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d for agent %q", w.Code, http.StatusOK, tt.agentName)
			}

			body := assertJSONResponse(t, w)
			assertEnvelopeSuccess(t, body)
		})
	}
}

// --- handleGetAgentTerminalToken tests ---

// TestHandleGetAgentTerminalToken_MissingName tests that a missing agent name returns 400.
func TestHandleGetAgentTerminalToken_MissingName(t *testing.T) {
	svc := &mockAgentService{
		generateTerminalTokenFunc: func(_ context.Context, agentName, _ string) (string, error) {
			return "", service.ErrValidation("missing agent name")
		},
	}
	handler := handleGetAgentTerminalToken(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/agents//terminal/token", nil)
	req.SetPathValue("name", "")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	body := assertJSONResponse(t, w)
	assertEnvelopeError(t, body, "data")

	if errMsg, _ := body["error"].(string); errMsg != "missing agent name" {
		t.Errorf("error = %q, want %q", errMsg, "missing agent name")
	}
}

// TestHandleGetAgentTerminalToken_InvalidName tests that invalid agent names return 400.
func TestHandleGetAgentTerminalToken_InvalidName(t *testing.T) {
	svc := &mockAgentService{
		generateTerminalTokenFunc: func(_ context.Context, _ string, _ string) (string, error) {
			return "", service.ErrValidation("invalid agent name: must match [a-zA-Z0-9_-]+")
		},
	}
	handler := handleGetAgentTerminalToken(svc)

	tests := []struct {
		name      string
		agentName string
	}{
		{"with slash", "bad/agent"},
		{"with dot", "bad.agent"},
		{"with space", "bad agent"},
		{"with special chars", "bad!@#$"},
		{"path traversal", "../../../etc/passwd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/agents/invalid/terminal/token", nil)
			req.SetPathValue("name", tt.agentName)
			req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d for agent %q", w.Code, http.StatusBadRequest, tt.agentName)
			}

			body := assertJSONResponse(t, w)
			assertEnvelopeError(t, body, "data")

			if errMsg, _ := body["error"].(string); !strings.Contains(errMsg, "invalid agent name") {
				t.Errorf("error = %q, want error containing 'invalid agent name'", errMsg)
			}
		})
	}
}

// TestHandleGetAgentTerminalToken_NilAuth tests that unavailable auth returns 503.
func TestHandleGetAgentTerminalToken_NilAuth(t *testing.T) {
	svc := &mockAgentService{
		generateTerminalTokenFunc: func(_ context.Context, _, _ string) (string, error) {
			return "", service.ErrUnavailable("terminal authentication not initialized")
		},
	}
	handler := handleGetAgentTerminalToken(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/ember/terminal/token", nil)
	req.SetPathValue("name", "ember")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	body := assertJSONResponse(t, w)
	assertEnvelopeError(t, body, "data")

	if errMsg, _ := body["error"].(string); errMsg != "terminal authentication not initialized" {
		t.Errorf("error = %q, want %q", errMsg, "terminal authentication not initialized")
	}
}

// TestHandleGetAgentTerminalToken_Success tests that a valid request returns a token.
func TestHandleGetAgentTerminalToken_Success(t *testing.T) {
	svc := &mockAgentService{
		generateTerminalTokenFunc: func(_ context.Context, _, _ string) (string, error) {
			return "test-token-abc.sig123", nil
		},
	}
	handler := handleGetAgentTerminalToken(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/ember/terminal/token", nil)
	req.SetPathValue("name", "ember")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify Cache-Control header is set to no-store
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-store")
	}

	body := assertJSONResponse(t, w)
	assertEnvelopeSuccess(t, body)

	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatal("missing or invalid 'data' field in response")
	}

	token, ok := data["token"].(string)
	if !ok || token == "" {
		t.Fatal("response should contain non-empty 'data.token' field")
	}
}

// TestHandleGetAgentTerminalToken_ValidNames tests that various valid agent names produce tokens.
func TestHandleGetAgentTerminalToken_ValidNames(t *testing.T) {
	svc := &mockAgentService{
		generateTerminalTokenFunc: func(_ context.Context, _, _ string) (string, error) {
			return "test-token", nil
		},
	}
	handler := handleGetAgentTerminalToken(svc)

	tests := []struct {
		name      string
		agentName string
	}{
		{"alphanumeric", "ember123"},
		{"with hyphen", "test-agent"},
		{"with underscore", "test_agent"},
		{"mixed case", "TestAgent-123"},
		{"single char", "a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/agents/"+tt.agentName+"/terminal/token", nil)
			req.SetPathValue("name", tt.agentName)
			req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d for agent %q", w.Code, http.StatusOK, tt.agentName)
			}
		})
	}
}

// --- handleAgentTerminalWS tests ---
// These tests remain unchanged — handleAgentTerminalWS still takes *TerminalManager, *realtime.TerminalAuth directly.

// TestHandleAgentTerminalWS_MissingName tests that a missing agent name returns 400.
func TestHandleAgentTerminalWS_MissingName(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	ta := newTestTerminalAuth()
	defer ta.Stop()

	handler := handleAgentTerminalWS(manager, ta, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/agents//terminal/ws", nil)
	req.SetPathValue("name", "")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["error"] != "missing agent name" {
		t.Errorf("error = %q, want %q", resp["error"], "missing agent name")
	}
}

// TestHandleAgentTerminalWS_InvalidName tests that invalid agent names return 400.
func TestHandleAgentTerminalWS_InvalidName(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	ta := newTestTerminalAuth()
	defer ta.Stop()

	handler := handleAgentTerminalWS(manager, ta, nil)

	tests := []struct {
		name      string
		agentName string
	}{
		{"with slash", "bad/agent"},
		{"with dot", "bad.agent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/agents/invalid/terminal/ws", nil)
			req.SetPathValue("name", tt.agentName)
			req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d for agent %q", w.Code, http.StatusBadRequest, tt.agentName)
			}
		})
	}
}

// TestHandleAgentTerminalWS_NilManager tests that nil manager returns 503.
func TestHandleAgentTerminalWS_NilManager(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	handler := handleAgentTerminalWS(nil, ta, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/ember/terminal/ws", nil)
	req.SetPathValue("name", "ember")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

// TestHandleAgentTerminalWS_NilAuth tests that nil auth returns 503.
func TestHandleAgentTerminalWS_NilAuth(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleAgentTerminalWS(manager, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/ember/terminal/ws", nil)
	req.SetPathValue("name", "ember")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

// TestHandleAgentTerminalWS_InvalidToken tests that invalid tokens return 401.
func TestHandleAgentTerminalWS_InvalidToken(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	ta := newTestTerminalAuth()
	defer ta.Stop()

	handler := handleAgentTerminalWS(manager, ta, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/ember/terminal/ws?token=invalid-token", nil)
	req.SetPathValue("name", "ember")
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}
