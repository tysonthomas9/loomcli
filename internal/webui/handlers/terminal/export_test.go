package terminal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandleExportSession_Success tests that the export handler returns a
// downloadable text file for a valid session.
func TestHandleExportSession_Success(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("bash", testRunPrefix+"-testexport", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, testRunPrefix+"-testexport-testws-export-ok")
	})

	session, err := mgr.Attach("testws", "export-ok", "", 80, 24)
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	defer mgr.Detach(session.ConnID)

	handler := handleExportSession(NewTerminalService(mgr, nil, nil, nil, nil, nil, nil, nil))

	req := withWorkspaceCtx(
		httptest.NewRequest(http.MethodGet, "/api/terminal/sessions/export-ok/export?format=txt", nil),
		"testws",
	)
	req.SetPathValue("session", "export-ok")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// Check Content-Disposition header.
	cd := rr.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".txt") {
		t.Errorf("expected attachment .txt disposition, got %q", cd)
	}

	// Check body contains the header.
	body := rr.Body.String()
	if !strings.Contains(body, "Terminal Session: export-ok") {
		t.Errorf("body missing session name header")
	}
	if !strings.Contains(body, "Exported:") {
		t.Errorf("body missing Exported timestamp")
	}
}

// TestHandleExportSession_MarkdownFormat tests the .md export format.
func TestHandleExportSession_MarkdownFormat(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("bash", testRunPrefix+"-testexportmd", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, testRunPrefix+"-testexportmd-testws-export-md")
	})

	session, err := mgr.Attach("testws", "export-md", "", 80, 24)
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	defer mgr.Detach(session.ConnID)

	handler := handleExportSession(NewTerminalService(mgr, nil, nil, nil, nil, nil, nil, nil))

	req := withWorkspaceCtx(
		httptest.NewRequest(http.MethodGet, "/api/terminal/sessions/export-md/export?format=md", nil),
		"testws",
	)
	req.SetPathValue("session", "export-md")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "# Terminal Session: export-md") {
		t.Errorf("body missing markdown heading")
	}
	if !strings.Contains(body, "```") {
		t.Errorf("body missing code block")
	}

	// Check .md in filename.
	cd := rr.Header().Get("Content-Disposition")
	if !strings.Contains(cd, ".md") {
		t.Errorf("expected .md in disposition, got %q", cd)
	}
}

// TestHandleExportSession_SessionNotFound tests 404 for non-existent session.
func TestHandleExportSession_SessionNotFound(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("bash", testRunPrefix+"-testexport404", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	t.Cleanup(func() { mgr.Shutdown() })

	handler := handleExportSession(NewTerminalService(mgr, nil, nil, nil, nil, nil, nil, nil))

	req := withWorkspaceCtx(
		httptest.NewRequest(http.MethodGet, "/api/terminal/sessions/nonexistent/export", nil),
		"testws",
	)
	req.SetPathValue("session", "nonexistent")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

// TestHandleExportSession_InvalidFormat tests 400 for invalid format.
func TestHandleExportSession_InvalidFormat(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("bash", testRunPrefix+"-testexportfmt", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	t.Cleanup(func() { mgr.Shutdown() })

	handler := handleExportSession(NewTerminalService(mgr, nil, nil, nil, nil, nil, nil, nil))

	req := withWorkspaceCtx(
		httptest.NewRequest(http.MethodGet, "/api/terminal/sessions/any/export?format=pdf", nil),
		"testws",
	)
	req.SetPathValue("session", "any")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

// TestHandleExportSession_InvalidSessionName tests 400 for invalid session name.
func TestHandleExportSession_InvalidSessionName(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("bash", testRunPrefix+"-testexportinv", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	t.Cleanup(func() { mgr.Shutdown() })

	handler := handleExportSession(NewTerminalService(mgr, nil, nil, nil, nil, nil, nil, nil))

	req := withWorkspaceCtx(
		httptest.NewRequest(http.MethodGet, "/api/terminal/sessions/inv@lid/export", nil),
		"testws",
	)
	req.SetPathValue("session", "inv@lid")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

// TestHandleScrollbackInfo_WithBuffer tests that scrollback info returns
// correct statistics when a buffer exists.
func TestHandleScrollbackInfo_WithBuffer(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("bash", testRunPrefix+"-testsbinfo", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	t.Cleanup(func() { mgr.Shutdown() })

	// Create a scrollback buffer and add some data.
	buf := mgr.GetScrollbackBuffer("testws", "sbinfo-test")
	buf.Append([]byte("line1\nline2\nline3\n"))

	handler := handleScrollbackInfo(NewTerminalService(mgr, nil, nil, nil, nil, nil, nil, nil))

	req := withWorkspaceCtx(
		httptest.NewRequest(http.MethodGet, "/api/terminal/sessions/sbinfo-test/scrollback-info", nil),
		"testws",
	)
	req.SetPathValue("session", "sbinfo-test")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp struct {
		LineCount      int   `json:"line_count"`
		MaxLines       int   `json:"max_lines"`
		TruncatedCount int64 `json:"truncated_count"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.LineCount != 3 {
		t.Errorf("line_count = %d, want 3", resp.LineCount)
	}
	if resp.MaxLines != defaultScrollbackMaxLines {
		t.Errorf("max_lines = %d, want %d", resp.MaxLines, defaultScrollbackMaxLines)
	}
	if resp.TruncatedCount != 0 {
		t.Errorf("truncated_count = %d, want 0", resp.TruncatedCount)
	}
}

// TestHandleScrollbackInfo_NoBuffer tests that scrollback info returns
// zeroed stats when no buffer has been created for the session.
func TestHandleScrollbackInfo_NoBuffer(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("bash", testRunPrefix+"-testsbinfonb", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	t.Cleanup(func() { mgr.Shutdown() })

	handler := handleScrollbackInfo(NewTerminalService(mgr, nil, nil, nil, nil, nil, nil, nil))

	req := withWorkspaceCtx(
		httptest.NewRequest(http.MethodGet, "/api/terminal/sessions/no-buffer/scrollback-info", nil),
		"testws",
	)
	req.SetPathValue("session", "no-buffer")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp struct {
		LineCount      int   `json:"line_count"`
		MaxLines       int   `json:"max_lines"`
		TruncatedCount int64 `json:"truncated_count"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// LookupScrollbackBuffer returns nil for unknown sessions, so 0 lines.
	if resp.LineCount != 0 {
		t.Errorf("line_count = %d, want 0", resp.LineCount)
	}
	if resp.MaxLines != defaultScrollbackMaxLines {
		t.Errorf("max_lines = %d, want %d", resp.MaxLines, defaultScrollbackMaxLines)
	}
}
