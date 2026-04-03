package webui

import (
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// handleExportSession returns a handler that exports the scrollback of a terminal session.
func handleExportSession(svc TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.PathValue("session")

		format := r.URL.Query().Get("format")
		if format == "" {
			format = "txt"
		}
		if format != "txt" && format != "md" {
			respondError(w, http.StatusBadRequest, "format must be txt or md")
			return
		}

		content, err := svc.ExportSession(r.Context(), session)
		if err != nil {
			writeServiceError(w, err)
			return
		}

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

// handleScrollbackInfo returns a handler that reports scrollback buffer statistics.
func handleScrollbackInfo(svc TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.PathValue("session")

		result, err := svc.GetScrollbackInfo(r.Context(), session)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"line_count":      result.LineCount,
			"max_lines":       result.MaxLines,
			"truncated_count": result.TruncatedCount,
		})
	}
}

// ExportSession captures the full tmux scrollback buffer for a session.
// Uses `tmux capture-pane -p -S -` to get the complete history.
func (m *TerminalManager) ExportSession(name string) (string, error) {
	if !validSessionName.MatchString(name) {
		return "", fmt.Errorf("invalid session name %q", name)
	}

	internalName := m.tmuxName(name)

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
	cmd := exec.Command(m.tmuxPath, "capture-pane", "-p", "-t", internalName, "-S", "-") //nolint:gosec // tmuxPath from LookPath, name validated
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tmux capture-pane: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
