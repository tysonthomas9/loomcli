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
