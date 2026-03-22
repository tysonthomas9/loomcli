package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- handleGetAgentTerminalInfo tests ---

// TestHandleGetAgentTerminalInfo_MissingName tests that a missing agent name returns 400.
func TestHandleGetAgentTerminalInfo_MissingName(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleGetAgentTerminalInfo(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/agents//terminal/info", nil)
	req.SetPathValue("name", "")
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

// TestHandleGetAgentTerminalInfo_InvalidName tests that invalid agent names return 400.
func TestHandleGetAgentTerminalInfo_InvalidName(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleGetAgentTerminalInfo(manager)

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

// TestHandleGetAgentTerminalInfo_NilManager tests that nil manager returns 503.
func TestHandleGetAgentTerminalInfo_NilManager(t *testing.T) {
	handler := handleGetAgentTerminalInfo(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/ember/terminal/info", nil)
	req.SetPathValue("name", "ember")
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

// TestHandleGetAgentTerminalInfo_ArchiveMode tests that a valid agent without a live
// tmux session returns archive mode.
func TestHandleGetAgentTerminalInfo_ArchiveMode(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleGetAgentTerminalInfo(manager)

	// Use a valid agent name that will not match any running tmux session
	req := httptest.NewRequest(http.MethodGet, "/api/agents/nonexistent-agent-xyz/terminal/info", nil)
	req.SetPathValue("name", "nonexistent-agent-xyz")
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

// TestHandleGetAgentTerminalInfo_ResponseShape verifies the complete JSON envelope shape
// for a successful response.
func TestHandleGetAgentTerminalInfo_ResponseShape(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleGetAgentTerminalInfo(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/spark/terminal/info", nil)
	req.SetPathValue("name", "spark")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Decode into the concrete response type to verify the shape matches
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
	// Mode should be either "tmux" or "archive"
	if resp.Data.Mode != agentTerminalModeTmux && resp.Data.Mode != agentTerminalModeArchive {
		t.Errorf("Data.Mode = %q, want %q or %q", resp.Data.Mode, agentTerminalModeTmux, agentTerminalModeArchive)
	}
}

// TestHandleGetAgentTerminalInfo_ValidNames tests that various valid agent names
// are accepted.
func TestHandleGetAgentTerminalInfo_ValidNames(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleGetAgentTerminalInfo(manager)

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
	ta := newTestTerminalAuth()
	defer ta.Stop()

	handler := handleGetAgentTerminalToken(ta)

	req := httptest.NewRequest(http.MethodGet, "/api/agents//terminal/token", nil)
	req.SetPathValue("name", "")
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
	ta := newTestTerminalAuth()
	defer ta.Stop()

	handler := handleGetAgentTerminalToken(ta)

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

// TestHandleGetAgentTerminalToken_NilAuth tests that nil auth returns 503.
func TestHandleGetAgentTerminalToken_NilAuth(t *testing.T) {
	handler := handleGetAgentTerminalToken(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/ember/terminal/token", nil)
	req.SetPathValue("name", "ember")
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
	ta := newTestTerminalAuth()
	defer ta.Stop()

	handler := handleGetAgentTerminalToken(ta)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/ember/terminal/token", nil)
	req.SetPathValue("name", "ember")
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

	// Token should have payload.signature format (one dot)
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		t.Errorf("token should have format payload.signature, got %d parts", len(parts))
	}
}

// TestHandleGetAgentTerminalToken_TokenIsValid tests that the returned token validates
// for the correct agent scope.
func TestHandleGetAgentTerminalToken_TokenIsValid(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	handler := handleGetAgentTerminalToken(ta)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/spark/terminal/token", nil)
	req.SetPathValue("name", "spark")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp agentTerminalTokenResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Data == nil || resp.Data.Token == "" {
		t.Fatal("expected non-empty token in response")
	}

	// The token should validate against the agent's scoped session
	scope := agentLogTokenScope("spark")
	if err := ta.ValidateToken(resp.Data.Token, scope); err != nil {
		t.Errorf("returned token should be valid for scope %q: %v", scope, err)
	}
}

// TestHandleGetAgentTerminalToken_TokenScopeIsolation tests that a token generated
// for one agent cannot be used for another.
func TestHandleGetAgentTerminalToken_TokenScopeIsolation(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	handler := handleGetAgentTerminalToken(ta)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/ember/terminal/token", nil)
	req.SetPathValue("name", "ember")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp agentTerminalTokenResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Token for "ember" should NOT validate against "spark" scope
	wrongScope := agentLogTokenScope("spark")
	if err := ta.ValidateToken(resp.Data.Token, wrongScope); err == nil {
		t.Error("token for 'ember' should not validate against 'spark' scope")
	}
}

// TestHandleGetAgentTerminalToken_UniqueTokensPerRequest tests that each request
// generates a unique token.
func TestHandleGetAgentTerminalToken_UniqueTokensPerRequest(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	handler := handleGetAgentTerminalToken(ta)

	tokens := make(map[string]bool, 5)
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/agents/ember/terminal/token", nil)
		req.SetPathValue("name", "ember")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want %d", i, w.Code, http.StatusOK)
		}

		var resp agentTerminalTokenResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("request %d: failed to decode: %v", i, err)
		}

		tok := resp.Data.Token
		if tokens[tok] {
			t.Errorf("request %d: duplicate token %q", i, tok)
		}
		tokens[tok] = true
	}
}

// TestHandleGetAgentTerminalToken_ValidNames tests that various valid agent names
// produce tokens.
func TestHandleGetAgentTerminalToken_ValidNames(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	handler := handleGetAgentTerminalToken(ta)

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
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d for agent %q", w.Code, http.StatusOK, tt.agentName)
			}
		})
	}
}

// --- handleAgentTerminalWS tests ---

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
		{"with space", "bad agent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/agents/invalid/terminal/ws", nil)
			req.SetPathValue("name", tt.agentName)
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
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["success"] != false {
		t.Error("expected success to be false")
	}

	if resp["error"] != "terminal manager not initialized" {
		t.Errorf("error = %q, want %q", resp["error"], "terminal manager not initialized")
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
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["error"] != "terminal authentication not initialized" {
		t.Errorf("error = %q, want %q", resp["error"], "terminal authentication not initialized")
	}
}

// TestHandleAgentTerminalWS_AuthNoToken tests that a request without a token returns 401.
func TestHandleAgentTerminalWS_AuthNoToken(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/api/agents/ember/terminal/ws", nil)
	req.SetPathValue("name", "ember")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["success"] != false {
		t.Error("expected success to be false")
	}

	if resp["error"] != "terminal authentication failed" {
		t.Errorf("error = %q, want %q", resp["error"], "terminal authentication failed")
	}
}

// TestHandleAgentTerminalWS_AuthInvalidToken tests that an invalid token returns 401.
func TestHandleAgentTerminalWS_AuthInvalidToken(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/api/agents/ember/terminal/ws?token=bogus.token", nil)
	req.SetPathValue("name", "ember")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// TestHandleAgentTerminalWS_NoActiveSession tests that a valid token but no active
// tmux session returns 404.
func TestHandleAgentTerminalWS_NoActiveSession(t *testing.T) {
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

	agentName := "nonexistent-agent-xyz"
	scope := agentLogTokenScope(agentName)
	token, err := ta.GenerateToken(scope)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/"+agentName+"/terminal/ws?token="+token, nil)
	req.SetPathValue("name", agentName)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["error"] != "no active terminal session for agent" {
		t.Errorf("error = %q, want %q", resp["error"], "no active terminal session for agent")
	}
}

// TestHandleAgentTerminalWS_TokenScopeMismatch tests that a valid token generated
// for one agent cannot be used to connect to a different agent's terminal.
func TestHandleAgentTerminalWS_TokenScopeMismatch(t *testing.T) {
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

	// Generate a token scoped to "ember"
	token, err := ta.GenerateToken(agentLogTokenScope("ember"))
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	// Try to use it for "spark" — scope mismatch should fail auth
	req := httptest.NewRequest(http.MethodGet, "/api/agents/spark/terminal/ws?token="+token, nil)
	req.SetPathValue("name", "spark")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["success"] != false {
		t.Error("expected success to be false")
	}

	if resp["error"] != "terminal authentication failed" {
		t.Errorf("error = %q, want %q", resp["error"], "terminal authentication failed")
	}
}

// TestHandleAgentTerminalWS_NonWebSocketRequest tests that a valid token and
// existing (or non-existing) session but a plain HTTP request (not a WebSocket
// upgrade) results in the handler rejecting the upgrade. Since the agent has no
// active tmux session, it returns 404 before reaching the upgrade. This test
// verifies the full pre-upgrade validation pipeline with a scope-correct token.
func TestHandleAgentTerminalWS_NonWebSocketRequest(t *testing.T) {
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

	agentName := "nonexistent-agent-ws"
	token, err := ta.GenerateToken(agentLogTokenScope(agentName))
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	// Plain HTTP GET (no WebSocket upgrade headers). Auth passes, but there
	// is no active tmux session so the handler returns 404 before attempting
	// the WebSocket upgrade.
	req := httptest.NewRequest(http.MethodGet, "/api/agents/"+agentName+"/terminal/ws?token="+token, nil)
	req.SetPathValue("name", agentName)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Should NOT be 401 — the token is valid for this agent
	if w.Code == http.StatusUnauthorized {
		t.Fatalf("status = 401; valid scoped token should pass auth")
	}

	// Expect 404 because there is no tmux session for this agent
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["error"] != "no active terminal session for agent" {
		t.Errorf("error = %q, want %q", resp["error"], "no active terminal session for agent")
	}
}

// TestHandleAgentTerminalWS_ReusedTokenFails tests that a token consumed by
// the WS handler cannot be reused on a subsequent request.
func TestHandleAgentTerminalWS_ReusedTokenFails(t *testing.T) {
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

	agentName := "reuse-agent"
	token, err := ta.GenerateToken(agentLogTokenScope(agentName))
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	urlPath := "/api/agents/" + agentName + "/terminal/ws?token=" + token

	// First request: token is consumed (request will hit 404 since no tmux session)
	req1 := httptest.NewRequest(http.MethodGet, urlPath, nil)
	req1.SetPathValue("name", agentName)
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)

	if w1.Code == http.StatusUnauthorized {
		t.Fatalf("first request should pass auth, got 401")
	}

	// Second request with same token: should be rejected
	req2 := httptest.NewRequest(http.MethodGet, urlPath, nil)
	req2.SetPathValue("name", agentName)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	if w2.Code != http.StatusUnauthorized {
		t.Errorf("second request status = %d, want %d (token already used)", w2.Code, http.StatusUnauthorized)
	}
}

// --- handleAgentTerminalWS additional coverage tests ---

// TestHandleAgentTerminalWS_ValidAuthNoSession_ResponseShape tests the full JSON
// envelope shape when auth passes but no tmux session exists. This verifies that
// the 404 response includes success=false and a descriptive error string.
func TestHandleAgentTerminalWS_ValidAuthNoSession_ResponseShape(t *testing.T) {
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

	agentName := "shape-test-agent"
	token, err := ta.GenerateToken(agentLogTokenScope(agentName))
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/"+agentName+"/terminal/ws?token="+token, nil)
	req.SetPathValue("name", agentName)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusNotFound, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Verify all expected fields are present
	if resp["success"] != false {
		t.Errorf("success = %v, want false", resp["success"])
	}
	errMsg, ok := resp["error"].(string)
	if !ok {
		t.Fatalf("error field is not a string: %T", resp["error"])
	}
	if errMsg != "no active terminal session for agent" {
		t.Errorf("error = %q, want %q", errMsg, "no active terminal session for agent")
	}
}

// TestHandleAgentTerminalWS_ValidAuthMultipleAgents tests that valid tokens for
// different agents each reach the session-lookup phase independently.
func TestHandleAgentTerminalWS_ValidAuthMultipleAgents(t *testing.T) {
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

	agents := []string{"alpha", "beta", "gamma"}
	for _, agentName := range agents {
		t.Run(agentName, func(t *testing.T) {
			token, err := ta.GenerateToken(agentLogTokenScope(agentName))
			if err != nil {
				t.Fatalf("GenerateToken() error = %v", err)
			}

			req := httptest.NewRequest(http.MethodGet, "/api/agents/"+agentName+"/terminal/ws?token="+token, nil)
			req.SetPathValue("name", agentName)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			// Each agent should pass auth and reach the session lookup (returning 404)
			if w.Code != http.StatusNotFound {
				t.Errorf("status = %d, want %d for agent %q", w.Code, http.StatusNotFound, agentName)
			}

			var resp map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to parse response: %v", err)
			}
			if resp["error"] != "no active terminal session for agent" {
				t.Errorf("error = %q, want %q", resp["error"], "no active terminal session for agent")
			}
		})
	}
}

// TestHandleAgentTerminalWS_ExpiredTokenFails tests that an expired token
// (consumed by a prior request) returns 401.
func TestHandleAgentTerminalWS_ExpiredTokenFails(t *testing.T) {
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

	agentName := "expire-test"
	token, err := ta.GenerateToken(agentLogTokenScope(agentName))
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	// First request consumes the token
	req1 := httptest.NewRequest(http.MethodGet, "/api/agents/"+agentName+"/terminal/ws?token="+token, nil)
	req1.SetPathValue("name", agentName)
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)

	// Token should have been consumed (first request passes auth, hits 404)
	if w1.Code == http.StatusUnauthorized {
		t.Fatalf("first request should pass auth")
	}

	// Second request: token is now consumed/expired
	req2 := httptest.NewRequest(http.MethodGet, "/api/agents/"+agentName+"/terminal/ws?token="+token, nil)
	req2.SetPathValue("name", agentName)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	if w2.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d (token consumed)", w2.Code, http.StatusUnauthorized)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["success"] != false {
		t.Error("expected success=false for expired token")
	}
	if resp["error"] != "terminal authentication failed" {
		t.Errorf("error = %q, want %q", resp["error"], "terminal authentication failed")
	}
}

// --- agentLogTokenScope tests ---

// TestAgentLogTokenScope tests the token scope format for agent names.
func TestAgentLogTokenScope(t *testing.T) {
	tests := []struct {
		agent string
		want  string
	}{
		{"ember", "agent:ember:logs"},
		{"spark", "agent:spark:logs"},
		{"test-agent_123", "agent:test-agent_123:logs"},
	}

	for _, tt := range tests {
		t.Run(tt.agent, func(t *testing.T) {
			got := agentLogTokenScope(tt.agent)
			if got != tt.want {
				t.Errorf("agentLogTokenScope(%q) = %q, want %q", tt.agent, got, tt.want)
			}
		})
	}
}
