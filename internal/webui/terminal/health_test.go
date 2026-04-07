// This file tests HandleTerminalKill and HandleTerminalSessionStatus which
// have been moved to handlers/terminal package. It cannot import that package
// from here without creating an import cycle. The handlers are tested via
// the handlers/terminal package tests instead.

package terminal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// TestHandleTerminalKill_GetRequest tests that a GET request is handled
// (method enforcement is done by the mux, not the handler).
func TestHandleTerminalKill_GetRequest(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleTerminalKill(NewTerminalService(manager, nil, nil, nil, nil, nil, nil), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/kill?session=test-session", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Handler processes the request (method enforcement is in the mux)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleTerminalKill_MissingSession tests that a request without a session
// query parameter returns 400.
func TestHandleTerminalKill_MissingSession(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleTerminalKill(NewTerminalService(manager, nil, nil, nil, nil, nil, nil), nil)

	req := httptest.NewRequest(http.MethodPost, "/api/terminal/kill", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["success"] != false {
		t.Error("expected success to be false")
	}
	if resp["error"] != "invalid session" {
		t.Errorf("expected error %q, got %q", "invalid session", resp["error"])
	}
}

// TestHandleTerminalKill_InvalidSession tests that an invalid session name
// (containing special characters) returns 400.
func TestHandleTerminalKill_InvalidSession(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleTerminalKill(NewTerminalService(manager, nil, nil, nil, nil, nil, nil), nil)

	req := httptest.NewRequest(http.MethodPost, "/api/terminal/kill?session=bad%2Fsession", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["success"] != false {
		t.Error("expected success to be false")
	}
	if resp["error"] != "invalid session" {
		t.Errorf("expected error %q, got %q", "invalid session", resp["error"])
	}
}

// TestHandleTerminalKill_NilManager tests that a nil manager returns 503.
func TestHandleTerminalKill_NilManager(t *testing.T) {
	handler := handleTerminalKill(NewTerminalService(nil, nil, nil, nil, nil, nil, nil), nil)

	req := httptest.NewRequest(http.MethodPost, "/api/terminal/kill?session=test-session", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["error"] != "terminal manager not initialized" {
		t.Errorf("expected error %q, got %q", "terminal manager not initialized", resp["error"])
	}
}

// TestHandleTerminalKill_AuthRequired tests that a request without a valid
// terminal token returns 401 when auth is enabled.
func TestHandleTerminalKill_AuthRequired(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	auth, err := realtime.NewTerminalAuth()
	if err != nil {
		t.Fatalf("failed to create terminal auth: %v", err)
	}
	defer auth.Stop()

	handler := handleTerminalKill(NewTerminalService(manager, nil, nil, nil, nil, nil, nil), auth)

	// Request without a token
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/kill?session=test-session", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["success"] != false {
		t.Error("expected success to be false")
	}
	if resp["error"] != "terminal authentication failed" {
		t.Errorf("expected error %q, got %q", "terminal authentication failed", resp["error"])
	}
}

// TestHandleTerminalKill_AuthWithValidToken tests that a request with a valid
// terminal token succeeds when auth is enabled.
func TestHandleTerminalKill_AuthWithValidToken(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	auth, err := realtime.NewTerminalAuth()
	if err != nil {
		t.Fatalf("failed to create terminal auth: %v", err)
	}
	defer auth.Stop()

	token, err := auth.GenerateToken("test-session", "")
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	handler := handleTerminalKill(NewTerminalService(manager, nil, nil, nil, nil, nil, nil), auth)

	req := httptest.NewRequest(http.MethodPost, "/api/terminal/kill?session=test-session&token="+token, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["success"] != true {
		t.Errorf("expected success to be true, got %v", resp["success"])
	}
}

// TestHandleTerminalKill_Success tests that a valid kill request returns 200
// with success=true (no auth required).
func TestHandleTerminalKill_Success(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleTerminalKill(NewTerminalService(manager, nil, nil, nil, nil, nil, nil), nil)

	req := httptest.NewRequest(http.MethodPost, "/api/terminal/kill?session=test-session", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["success"] != true {
		t.Errorf("expected success to be true, got %v", resp["success"])
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}
}

// TestHandleTerminalSessionStatus_MissingSession tests that a request without
// a session query parameter returns 400.
func TestHandleTerminalSessionStatus_MissingSession(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleTerminalSessionStatus(NewTerminalService(manager, nil, nil, nil, nil, nil, nil), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/session-status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["error"] != "invalid session" {
		t.Errorf("expected error %q, got %q", "invalid session", resp["error"])
	}
}

// TestHandleTerminalSessionStatus_InvalidSession tests that an invalid session
// name returns 400.
func TestHandleTerminalSessionStatus_InvalidSession(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleTerminalSessionStatus(NewTerminalService(manager, nil, nil, nil, nil, nil, nil), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/session-status?session=bad%2Fsession", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["error"] != "invalid session" {
		t.Errorf("expected error %q, got %q", "invalid session", resp["error"])
	}
}

// TestHandleTerminalSessionStatus_NilManager tests that a nil manager returns 503.
func TestHandleTerminalSessionStatus_NilManager(t *testing.T) {
	handler := handleTerminalSessionStatus(NewTerminalService(nil, nil, nil, nil, nil, nil, nil), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/session-status?session=test-session", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["error"] != "terminal manager not initialized" {
		t.Errorf("expected error %q, got %q", "terminal manager not initialized", resp["error"])
	}
}

// TestHandleTerminalSessionStatus_DeadSession tests that a non-existent session
// returns alive=false.
func TestHandleTerminalSessionStatus_DeadSession(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleTerminalSessionStatus(NewTerminalService(manager, nil, nil, nil, nil, nil, nil), nil)

	// Query a session that does not exist
	req := httptest.NewRequest(http.MethodGet, "/api/terminal/session-status?session=nonexistent-session", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["alive"] != false {
		t.Errorf("expected alive to be false for non-existent session, got %v", resp["alive"])
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}
}

// TestHandleTerminalSessionStatus_AliveSession tests that a running tmux session
// returns alive=true.
func TestHandleTerminalSessionStatus_AliveSession(t *testing.T) {
	manager, err := NewTerminalManager("bash", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}

	sessionName := "health-alive-test"
	t.Cleanup(func() {
		manager.Shutdown()
		killTmuxSession(t, sessionName)
	})

	// Create a tmux session via Attach so it's tracked
	_, err = manager.Attach(sessionName, "", 80, 24)
	if err != nil {
		t.Fatalf("failed to attach session: %v", err)
	}

	handler := handleTerminalSessionStatus(NewTerminalService(manager, nil, nil, nil, nil, nil, nil), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/session-status?session="+sessionName, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["alive"] != true {
		t.Errorf("expected alive to be true for running session, got %v", resp["alive"])
	}

	// Alive sessions should not have an exit_reason
	if _, hasReason := resp["exit_reason"]; hasReason {
		t.Errorf("expected no exit_reason for alive session, got %q", resp["exit_reason"])
	}
}

// TestHandleTerminalSessionStatus_ResponseFormat tests the overall response
// structure for both alive and dead sessions.
func TestHandleTerminalSessionStatus_ResponseFormat(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleTerminalSessionStatus(NewTerminalService(manager, nil, nil, nil, nil, nil, nil), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/session-status?session=format-test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify it's valid JSON
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response as JSON: %v", err)
	}

	// "alive" field must be present
	if _, hasAlive := resp["alive"]; !hasAlive {
		t.Error("response must contain 'alive' field")
	}
}

// TestWSCloseBackendExitedConstant verifies the WebSocket close code constant.
func TestWSCloseBackendExitedConstant(t *testing.T) {
	if realtime.WSCloseBackendExited != 4001 {
		t.Errorf("realtime.WSCloseBackendExited = %d, want %d", realtime.WSCloseBackendExited, 4001)
	}
}
