package misc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// assertJSONError asserts that rr carries the standard JSON error envelope:
// the expected status, Content-Type: application/json, and a body that parses
// as an object with a non-empty "error" key.
func assertJSONError(t *testing.T, rr *httptest.ResponseRecorder, wantStatus int) {
	t.Helper()
	if rr.Code != wantStatus {
		t.Errorf("status = %d, want %d", rr.Code, wantStatus)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (body=%q)", err, rr.Body.String())
	}
	msg, ok := body["error"].(string)
	if !ok || msg == "" {
		t.Errorf("body has no non-empty string \"error\" key: %v", body)
	}
}

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
	if rr.Body.Len() != 0 {
		t.Errorf("204 response has body %q, want empty", rr.Body.String())
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

	assertJSONError(t, rr, http.StatusForbidden)
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

	assertJSONError(t, rr, http.StatusForbidden)
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

	// Fail-closed when the server token is empty.
	assertJSONError(t, rr, http.StatusForbidden)
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

	assertJSONError(t, rr, http.StatusBadRequest)
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

	assertJSONError(t, rr, http.StatusBadRequest)
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

	assertJSONError(t, rr, http.StatusBadRequest)
}

func TestNotifySessionChange_NonBearerAuthHeader(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	handler := handleNotifySessionChange(hub, "secret-token-123")

	body := `{"task_id":"task-1","session_id":"sess-1","status":"completed","workspace_id":"ws-1"}`
	req := httptest.NewRequest(http.MethodPost, sessions.NotifyPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// Present, but not the "Bearer " scheme.
	req.Header.Set("Authorization", "Basic secret-token-123")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assertJSONError(t, rr, http.StatusForbidden)
}

// TestNotifySessionChange_AllRejectionsAreJSON pins the invariant that no
// rejection path on this route answers with plain text. It overlaps the
// per-branch tests above deliberately: those document each branch, this one
// fails if a future branch is added with http.Error.
func TestNotifySessionChange_AllRejectionsAreJSON(t *testing.T) {
	const validBody = `{"task_id":"task-1","session_id":"sess-1","status":"completed","workspace_id":"ws-1"}`

	cases := []struct {
		name        string
		serverToken string
		authHeader  string
		body        string
		wantStatus  int
	}{
		{"empty server token", "", "Bearer any-token", validBody, http.StatusForbidden},
		{"no auth header", "secret", "", validBody, http.StatusForbidden},
		{"non-bearer auth header", "secret", "Basic secret", validBody, http.StatusForbidden},
		{"wrong token", "secret", "Bearer wrong", validBody, http.StatusForbidden},
		{"malformed json", "secret", "Bearer secret", "not json", http.StatusBadRequest},
		{"empty body", "secret", "Bearer secret", "", http.StatusBadRequest},
		{"missing task_id", "secret", "Bearer secret", `{"session_id":"sess-1"}`, http.StatusBadRequest},
		{"missing session_id", "secret", "Bearer secret", `{"task_id":"task-1"}`, http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hub := realtime.NewHub()
			go hub.Run()
			defer hub.Stop()

			handler := handleNotifySessionChange(hub, tc.serverToken)

			req := httptest.NewRequest(http.MethodPost, sessions.NotifyPath, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			assertJSONError(t, rr, tc.wantStatus)
		})
	}
}
