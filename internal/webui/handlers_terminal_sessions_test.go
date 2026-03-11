package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
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
	manager, err := NewTerminalManager("bash", "testsuc", 0)
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
	manager, err := NewTerminalManager("bash", "testttl", 0)
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
	manager, err := NewTerminalManager("bash", "testflt", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	// Manually create a tmux session with a different prefix.
	otherSession := "other-something"
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

	// Create a session. With empty prefix, the tmux session name is the raw name.
	session, err := manager.Attach("nopfx-session", "", 80, 24)
	if err != nil {
		t.Fatalf("failed to attach session: %v", err)
	}
	defer func() {
		manager.Detach(session.ConnID)
		manager.KillSessionByName("nopfx-session")
	}()

	sessions, err := manager.ListActiveSessions()
	if err != nil {
		t.Fatalf("ListActiveSessions failed: %v", err)
	}

	// With no prefix, all tmux sessions are returned as-is.
	// We should find our session with its original name.
	var found bool
	for _, s := range sessions {
		if s.Name == "nopfx-session" {
			found = true
			if s.Created == 0 {
				t.Error("expected created to be non-zero for an active session")
			}
			if s.Label != "nopfx-session" {
				t.Errorf("expected label 'nopfx-session', got %q", s.Label)
			}
			break
		}
	}
	if !found {
		t.Errorf("session 'nopfx-session' not found in results; got %+v", sessions)
	}

	// talk-to-lead should also still be present.
	var hasTalkToLead bool
	for _, s := range sessions {
		if s.Name == "talk-to-lead" {
			hasTalkToLead = true
			break
		}
	}
	if !hasTalkToLead {
		t.Error("talk-to-lead not found in results when prefix is empty")
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

	_, err := NewTerminalManager("bash", "test", 0)
	if !errors.Is(err, ErrTmuxNotFound) {
		t.Errorf("expected ErrTmuxNotFound, got: %v", err)
	}
}
