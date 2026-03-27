package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

// TestHandleListTerminalSessions_NilManager tests that a nil manager returns 503
// with an appropriate error message.
func TestHandleListTerminalSessions_NilManager(t *testing.T) {
	handler := handleListTerminalSessions(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/sessions", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["success"] != false {
		t.Error("expected success to be false")
	}

	if resp["error"] != "terminal manager not initialized" {
		t.Errorf("expected error 'terminal manager not initialized', got %q", resp["error"])
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}
}

// TestHandleListTerminalSessions_Success tests that the endpoint returns a real
// session with a non-zero created timestamp after attaching to it.
func TestHandleListTerminalSessions_Success(t *testing.T) {
	manager, err := NewTerminalManager("bash", testRunPrefix+"-testsuc", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()
	defer func() {
		// Ensure the underlying tmux session is killed even if Shutdown misses it.
		killCmd := exec.Command("tmux", "kill-session", "-t", "testsuc-talk-to-lead") //nolint:norawexec
		_ = killCmd.Run()
	}()

	// Create the talk-to-lead session by attaching to it.
	session, err := manager.Attach("talk-to-lead", "", 80, 24)
	if err != nil {
		t.Fatalf("failed to attach talk-to-lead session: %v", err)
	}
	defer manager.Detach(session.ConnID)

	handler := handleListTerminalSessions(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/sessions", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["success"] != true {
		t.Error("expected success to be true")
	}

	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatal("expected data to be an object")
	}

	sessions, ok := data["sessions"].([]interface{})
	if !ok {
		t.Fatal("expected sessions to be an array")
	}

	// Find talk-to-lead in the sessions list.
	var found bool
	for _, s := range sessions {
		sess, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		if sess["name"] == "talk-to-lead" {
			found = true
			created, ok := sess["created"].(float64)
			if !ok {
				t.Error("expected created to be a number")
			} else if created == 0 {
				t.Error("expected created to be non-zero for an active session")
			}
			if sess["label"] != "talk-to-lead" {
				t.Errorf("expected label 'talk-to-lead', got %q", sess["label"])
			}
			break
		}
	}
	if !found {
		t.Error("talk-to-lead session not found in response")
	}
}

// TestHandleListTerminalSessions_AlwaysIncludesTalkToLead tests that talk-to-lead
// is always present in the response even when no sessions have been created.
func TestHandleListTerminalSessions_AlwaysIncludesTalkToLead(t *testing.T) {
	manager, err := NewTerminalManager("bash", testRunPrefix+"-testttl", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleListTerminalSessions(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/sessions", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["success"] != true {
		t.Error("expected success to be true")
	}

	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatal("expected data to be an object")
	}

	sessions, ok := data["sessions"].([]interface{})
	if !ok {
		t.Fatal("expected sessions to be an array")
	}

	// talk-to-lead must appear with created=0 since it was never actually created.
	var found bool
	for _, s := range sessions {
		sess, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		if sess["name"] == "talk-to-lead" {
			found = true
			created, ok := sess["created"].(float64)
			if !ok {
				t.Error("expected created to be a number")
			} else if created != 0 {
				t.Errorf("expected created to be 0 for a placeholder session, got %v", created)
			}
			if sess["label"] != "talk-to-lead" {
				t.Errorf("expected label 'talk-to-lead', got %q", sess["label"])
			}
			break
		}
	}
	if !found {
		t.Error("talk-to-lead session not found in response")
	}
}

// TestHandleListTerminalSessions_FiltersOtherSessions tests that sessions created
// with a different prefix are not included in the response.
func TestHandleListTerminalSessions_FiltersOtherSessions(t *testing.T) {
	manager, err := NewTerminalManager("bash", testRunPrefix+"-testflt", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	// Manually create a tmux session with a different prefix.
	otherSession := testRunPrefix + "-other-something"
	createCmd := exec.Command("tmux", "new-session", "-d", "-s", otherSession) //nolint:norawexec
	if err := createCmd.Run(); err != nil {
		t.Fatalf("failed to create tmux session %q: %v", otherSession, err)
	}
	defer func() {
		killCmd := exec.Command("tmux", "kill-session", "-t", otherSession) //nolint:norawexec
		_ = killCmd.Run()
	}()

	handler := handleListTerminalSessions(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/sessions", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["success"] != true {
		t.Error("expected success to be true")
	}

	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatal("expected data to be an object")
	}

	sessions, ok := data["sessions"].([]interface{})
	if !ok {
		t.Fatal("expected sessions to be an array")
	}

	// Verify "other-something" does NOT appear (it has prefix "other", not "test").
	for _, s := range sessions {
		sess, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := sess["name"].(string)
		if name == otherSession || name == "something" {
			t.Errorf("session %q should not appear in results (wrong prefix)", name)
		}
	}
}

// TestListActiveSessions_NoPrefixFallback tests that when sessionPrefix is empty,
// all sessions are returned with their original names.
func TestListActiveSessions_NoPrefixFallback(t *testing.T) {
	manager, err := NewTerminalManager("bash", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	// Create a session with a PID-scoped name to avoid collisions across parallel runs.
	sessName := testRunPrefix + "-nopfx"
	session, err := manager.Attach(sessName, "", 80, 24)
	if err != nil {
		t.Fatalf("failed to attach session: %v", err)
	}
	defer func() {
		manager.Detach(session.ConnID)
		manager.KillSessionByName(sessName)
	}()

	sessions, err := manager.ListActiveSessions()
	if err != nil {
		t.Fatalf("ListActiveSessions failed: %v", err)
	}

	// With no prefix, all tmux sessions are returned as-is.
	// We should find our session with its original name.
	var found bool
	for _, s := range sessions {
		if s.Name == sessName {
			found = true
			if s.Created == 0 {
				t.Error("expected created to be non-zero for an active session")
			}
			if s.Label != sessName {
				t.Errorf("expected label %q, got %q", sessName, s.Label)
			}
			break
		}
	}
	if !found {
		t.Errorf("session %q not found in results; got %+v", sessName, sessions)
	}
}

// TestNewTerminalManager_NoTmux_Sessions verifies that NewTerminalManager returns
// ErrTmuxNotFound when tmux is not available (using lookPathTmux override).
func TestNewTerminalManager_NoTmux_Sessions(t *testing.T) {
	orig := lookPathTmux
	lookPathTmux = func(file string) (string, error) {
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() { lookPathTmux = orig })

	_, err := NewTerminalManager("bash", testRunPrefix+"-test", 0)
	if !errors.Is(err, ErrTmuxNotFound) {
		t.Errorf("expected ErrTmuxNotFound, got: %v", err)
	}
}

// TestTruncate tests the truncate helper function.
func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{
			name:   "short string unchanged",
			input:  "hello",
			maxLen: 10,
			want:   "hello",
		},
		{
			name:   "exact length unchanged",
			input:  "hello",
			maxLen: 5,
			want:   "hello",
		},
		{
			name:   "over length truncated with ellipsis",
			input:  "hello world",
			maxLen: 5,
			want:   "hello...",
		},
		{
			name:   "empty string unchanged",
			input:  "",
			maxLen: 10,
			want:   "",
		},
		{
			name:   "maxLen zero truncates everything",
			input:  "hello",
			maxLen: 0,
			want:   "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

// TestFormatSeedPrompt tests the formatSeedPrompt function with various inputs.
func TestFormatSeedPrompt(t *testing.T) {
	t.Run("all fields present", func(t *testing.T) {
		req := &seedRequest{
			IssueID:     "PROJ-123",
			Title:       "Fix login bug",
			Description: "Users cannot log in when using SSO.",
			Design:      "Use OAuth2 flow instead.",
			Blockers: []seedBlocker{
				{ID: "PROJ-100", Title: "Auth service down"},
				{ID: "PROJ-101", Title: "Token refresh broken"},
			},
		}

		got := formatSeedPrompt(req)

		if !strings.Contains(got, "I need help with issue PROJ-123: Fix login bug") {
			t.Errorf("expected header line, got: %s", got)
		}
		if !strings.Contains(got, "Description: Users cannot log in when using SSO.") {
			t.Errorf("expected description, got: %s", got)
		}
		if !strings.Contains(got, "Design: Use OAuth2 flow instead.") {
			t.Errorf("expected design, got: %s", got)
		}
		if !strings.Contains(got, "Blockers:") {
			t.Errorf("expected blockers header, got: %s", got)
		}
		if !strings.Contains(got, "- PROJ-100: Auth service down") {
			t.Errorf("expected first blocker, got: %s", got)
		}
		if !strings.Contains(got, "- PROJ-101: Token refresh broken") {
			t.Errorf("expected second blocker, got: %s", got)
		}
	})

	t.Run("missing optional fields", func(t *testing.T) {
		req := &seedRequest{
			IssueID: "PROJ-456",
			Title:   "Add feature X",
		}

		got := formatSeedPrompt(req)

		if got != "I need help with issue PROJ-456: Add feature X" {
			t.Errorf("expected only header line, got: %q", got)
		}
		if strings.Contains(got, "Description:") {
			t.Error("should not contain Description when empty")
		}
		if strings.Contains(got, "Design:") {
			t.Error("should not contain Design when empty")
		}
		if strings.Contains(got, "Blockers:") {
			t.Error("should not contain Blockers when empty")
		}
	})

	t.Run("long description truncated at 800 chars", func(t *testing.T) {
		longDesc := strings.Repeat("a", 900)
		req := &seedRequest{
			IssueID:     "PROJ-789",
			Title:       "Test",
			Description: longDesc,
		}

		got := formatSeedPrompt(req)

		// The truncated description should be 800 chars + "..."
		expectedTruncated := strings.Repeat("a", 800) + "..."
		if !strings.Contains(got, "Description: "+expectedTruncated) {
			t.Errorf("expected description truncated to 800 chars with ellipsis")
		}
	})

	t.Run("long design truncated at 500 chars", func(t *testing.T) {
		longDesign := strings.Repeat("b", 600)
		req := &seedRequest{
			IssueID: "PROJ-101",
			Title:   "Test",
			Design:  longDesign,
		}

		got := formatSeedPrompt(req)

		expectedTruncated := strings.Repeat("b", 500) + "..."
		if !strings.Contains(got, "Design: "+expectedTruncated) {
			t.Errorf("expected design truncated to 500 chars with ellipsis")
		}
	})

	t.Run("max 5 blockers", func(t *testing.T) {
		blockers := make([]seedBlocker, 7)
		for i := range blockers {
			blockers[i] = seedBlocker{
				ID:    fmt.Sprintf("B-%d", i+1),
				Title: fmt.Sprintf("Blocker %d", i+1),
			}
		}
		req := &seedRequest{
			IssueID:  "PROJ-999",
			Title:    "Test",
			Blockers: blockers,
		}

		got := formatSeedPrompt(req)

		// Should include blockers 1-5
		for i := 1; i <= 5; i++ {
			expected := fmt.Sprintf("- B-%d: Blocker %d", i, i)
			if !strings.Contains(got, expected) {
				t.Errorf("expected blocker %d (%q) in output", i, expected)
			}
		}
		// Should NOT include blockers 6-7
		for i := 6; i <= 7; i++ {
			excluded := fmt.Sprintf("- B-%d: Blocker %d", i, i)
			if strings.Contains(got, excluded) {
				t.Errorf("blocker %d should be excluded (max 5), got: %s", i, got)
			}
		}
	})

	t.Run("description at exact limit not truncated", func(t *testing.T) {
		exactDesc := strings.Repeat("c", 800)
		req := &seedRequest{
			IssueID:     "PROJ-800",
			Title:       "Test",
			Description: exactDesc,
		}

		got := formatSeedPrompt(req)

		if strings.Contains(got, "...") {
			t.Error("description at exact limit should not be truncated")
		}
		if !strings.Contains(got, "Description: "+exactDesc) {
			t.Error("expected full description at exact limit")
		}
	})

	t.Run("exactly 5 blockers all included", func(t *testing.T) {
		blockers := make([]seedBlocker, 5)
		for i := range blockers {
			blockers[i] = seedBlocker{
				ID:    fmt.Sprintf("X-%d", i+1),
				Title: fmt.Sprintf("Issue %d", i+1),
			}
		}
		req := &seedRequest{
			IssueID:  "PROJ-555",
			Title:    "Test",
			Blockers: blockers,
		}

		got := formatSeedPrompt(req)

		for i := 1; i <= 5; i++ {
			expected := fmt.Sprintf("- X-%d: Issue %d", i, i)
			if !strings.Contains(got, expected) {
				t.Errorf("expected blocker %d in output", i)
			}
		}
	})
}

// ── ScheduleSessionKill handler tests ──────────────────────────────────────────

// TestHandleScheduleSessionKill_ValidSession verifies the handler returns 200
// for a valid session name.
func TestHandleScheduleSessionKill_ValidSession(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	t.Cleanup(func() { mgr.Shutdown() })

	handler := handleScheduleSessionKill(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/terminal/sessions/test-session/kill", nil)
	req.SetPathValue("session", "test-session")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
}

// TestHandleScheduleSessionKill_InvalidSession verifies the handler returns 400
// for an invalid session name.
func TestHandleScheduleSessionKill_InvalidSession(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	t.Cleanup(func() { mgr.Shutdown() })

	handler := handleScheduleSessionKill(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/terminal/sessions/invalid%20name/kill", nil)
	req.SetPathValue("session", "invalid name")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

// TestHandleScheduleSessionKill_NilManager verifies the handler returns 503
// when the terminal manager is nil.
func TestHandleScheduleSessionKill_NilManager(t *testing.T) {
	handler := handleScheduleSessionKill(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/terminal/sessions/test-session/kill", nil)
	req.SetPathValue("session", "test-session")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
}

// ── SeedTerminalSession handler tests ─────────────────────────────────────────

// TestHandleSeedTerminalSession_MissingName tests that the seed handler returns
// 400 when the session name path parameter is empty.
func TestHandleSeedTerminalSession_NilManager(t *testing.T) {
	handler := handleSeedTerminalSession(nil)

	body := strings.NewReader(`{"issue_id":"X-1","title":"Test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/sessions/{name}/seed", body)
	req.SetPathValue("name", "test-session")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["error"] != "terminal manager not initialized" {
		t.Errorf("expected 'terminal manager not initialized' error, got %q", resp["error"])
	}
}

func TestHandleSeedTerminalSession_MissingName(t *testing.T) {
	// Use a non-nil manager so the nil guard doesn't trip
	mgr := &TerminalManager{}
	handler := handleSeedTerminalSession(mgr)

	body := strings.NewReader(`{"issue_id":"X-1","title":"Test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/sessions//seed", body)
	// PathValue("name") returns "" when name is not set in the route pattern.
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["error"] != "missing session name" {
		t.Errorf("expected 'missing session name' error, got %q", resp["error"])
	}
}

// TestHandleSeedTerminalSession_InvalidJSON tests that the seed handler returns
// 400 for malformed JSON.
func TestHandleSeedTerminalSession_InvalidJSON(t *testing.T) {
	mgr := &TerminalManager{}
	handler := handleSeedTerminalSession(mgr)

	body := strings.NewReader(`{invalid json}`)
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/sessions/{name}/seed", body)
	req.SetPathValue("name", "test-session")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	errMsg, _ := resp["error"].(string)
	if !strings.HasPrefix(errMsg, "invalid JSON body:") {
		t.Errorf("expected 'invalid JSON body:' prefix, got %q", errMsg)
	}
}

// TestHandleSeedTerminalSession_MissingRequiredFields tests that the seed handler
// returns 400 when issue_id or title are missing.
func TestHandleSeedTerminalSession_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"missing issue_id", `{"title":"Test"}`},
		{"missing title", `{"issue_id":"X-1"}`},
		{"both missing", `{}`},
	}

	mgr := &TerminalManager{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := handleSeedTerminalSession(mgr)

			body := strings.NewReader(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/api/terminal/sessions/{name}/seed", body)
			req.SetPathValue("name", "test-session")
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
			}

			var resp map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to parse response: %v", err)
			}

			if resp["error"] != "issue_id and title are required" {
				t.Errorf("expected 'issue_id and title are required', got %q", resp["error"])
			}
		})
	}
}
