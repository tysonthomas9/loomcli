package misc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

func TestNotifySessionChange_ValidToken(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	handler := handleNotifySessionChange(hub, "secret-token-123")

	body := `{"task_id":"task-1","session_id":"sess-1","status":"completed","workspace_id":"ws-1"}`
	req := httptest.NewRequest(http.MethodPost, sessions.NotifyPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-token-123")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestNotifySessionChange_MissingAuthHeader(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	handler := handleNotifySessionChange(hub, "secret-token-123")

	body := `{"task_id":"task-1","session_id":"sess-1","status":"completed","workspace_id":"ws-1"}`
	req := httptest.NewRequest(http.MethodPost, sessions.NotifyPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestNotifySessionChange_WrongToken(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	handler := handleNotifySessionChange(hub, "correct-token")

	body := `{"task_id":"task-1","session_id":"sess-1","status":"completed","workspace_id":"ws-1"}`
	req := httptest.NewRequest(http.MethodPost, sessions.NotifyPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestNotifySessionChange_EmptyServerToken(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	handler := handleNotifySessionChange(hub, "")

	body := `{"task_id":"task-1","session_id":"sess-1","status":"completed","workspace_id":"ws-1"}`
	req := httptest.NewRequest(http.MethodPost, sessions.NotifyPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer any-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d (fail-closed when server token is empty)", rr.Code, http.StatusForbidden)
	}
}

func TestNotifySessionChange_InvalidJSON(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	handler := handleNotifySessionChange(hub, "valid-token")

	req := httptest.NewRequest(http.MethodPost, sessions.NotifyPath, strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer valid-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestNotifySessionChange_MissingTaskID(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	handler := handleNotifySessionChange(hub, "valid-token")

	// Missing task_id
	body := `{"session_id":"sess-1","status":"completed","workspace_id":"ws-1"}`
	req := httptest.NewRequest(http.MethodPost, sessions.NotifyPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer valid-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestNotifySessionChange_MissingSessionID(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	handler := handleNotifySessionChange(hub, "valid-token")

	// Missing session_id
	body := `{"task_id":"task-1","status":"completed","workspace_id":"ws-1"}`
	req := httptest.NewRequest(http.MethodPost, sessions.NotifyPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer valid-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}
