package webui

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// handleExportSession returns a handler that exports the scrollback of a
// terminal session as a downloadable .txt or .md file. The content is
// captured via tmux capture-pane and ANSI escape codes are stripped.
//
// GET /api/workspaces/{ws}/terminal/sessions/{session}/export?format=txt|md
func handleExportSession(manager *TerminalManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.PathValue("session")
		if session == "" || !validTerminalSession.MatchString(session) {
			respondError(w, http.StatusBadRequest, "invalid session name")
			return
		}

		format := r.URL.Query().Get("format")
		if format == "" {
			format = "txt"
		}
		if format != "txt" && format != "md" {
			respondError(w, http.StatusBadRequest, "format must be txt or md")
			return
		}

		wsID := WorkspaceFromContext(r.Context())
		if wsID == "" {
			respondError(w, http.StatusBadRequest, "workspace context required")
			return
		}

		if !manager.SessionAlive(wsID, session) {
			respondError(w, http.StatusNotFound, "session not found")
			return
		}

		content, err := manager.ExportSession(wsID, session)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "failed to capture scrollback")
			return
		}

		// Strip ANSI escape codes for clean export.
		content = StripANSI(content)

		timestamp := time.Now().UTC().Format(time.RFC3339)
		filename := fmt.Sprintf("%s-%s.%s", session, time.Now().UTC().Format("20060102-150405"), format)

		var body string
		switch format {
		case "md":
			body = fmt.Sprintf("# Terminal Session: %s\n\nExported: %s\n\n```\n%s\n```\n", session, timestamp, content)
		default:
			body = fmt.Sprintf("Terminal Session: %s\nExported: %s\n\n%s\n", session, timestamp, content)
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}
}

// handleScrollbackInfo returns a handler that reports scrollback buffer
// statistics for a terminal session (line count, max lines, truncated count).
//
// GET /api/workspaces/{ws}/terminal/sessions/{session}/scrollback-info
func handleScrollbackInfo(manager *TerminalManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.PathValue("session")
		if session == "" || !validTerminalSession.MatchString(session) {
			respondError(w, http.StatusBadRequest, "invalid session name")
			return
		}

		wsID := WorkspaceFromContext(r.Context())
		if wsID == "" {
			respondError(w, http.StatusBadRequest, "workspace context required")
			return
		}

		buf := manager.LookupScrollbackBuffer(wsID, session)
		var lineCount int
		var truncatedCount int64
		if buf != nil {
			lineCount = buf.LineCount()
			truncatedCount = buf.TruncatedCount()
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"line_count":      lineCount,
			"max_lines":       manager.ScrollbackMaxLines(),
			"truncated_count": truncatedCount,
		})
	}
}

// ExportSession captures the full tmux scrollback buffer for a session in
// the given workspace. Uses `tmux capture-pane -p -S -` to get the complete
// history. wsID must be non-empty.
func (m *TerminalManager) ExportSession(wsID, name string) (string, error) {
	if wsID == "" {
		return "", fmt.Errorf("wsID must not be empty")
	}
	if !validSessionName.MatchString(name) {
		return "", fmt.Errorf("invalid session name %q", name)
	}

	internalName := m.tmuxName(wsID, name)

	if !m.tmuxHasSession(internalName) {
		return "", fmt.Errorf("tmux session %q not found", name)
	}

	// Capture the full scrollback using "-S -" (start of history).
	out, err := m.runTmuxCapture(internalName)
	if err != nil {
		return "", err
	}

	return strings.TrimRight(string(out), "\n"), nil
}

// runTmuxCapture runs tmux capture-pane for the full history of a session.
func (m *TerminalManager) runTmuxCapture(internalName string) ([]byte, error) {
	cmd := m.tmuxCmd("capture-pane", "-p", "-t", internalName, "-S", "-")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tmux capture-pane: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
