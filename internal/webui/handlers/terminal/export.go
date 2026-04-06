package terminal

import (
	"fmt"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// HandleExportSession returns a handler that exports the scrollback of a terminal session.
func HandleExportSession(svc service.TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.PathValue("session")

		format := r.URL.Query().Get("format")
		if format == "" {
			format = "txt"
		}
		if format != "txt" && format != "md" {
			handler.RespondError(w, http.StatusBadRequest, "format must be txt or md")
			return
		}

		content, err := svc.ExportSession(r.Context(), session)
		if err != nil {
			handler.HandleServiceError(w, err)
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

// HandleScrollbackInfo returns a handler that reports scrollback buffer statistics.
func HandleScrollbackInfo(svc service.TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.PathValue("session")

		result, err := svc.GetScrollbackInfo(r.Context(), session)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"line_count":      result.LineCount,
			"max_lines":       result.MaxLines,
			"truncated_count": result.TruncatedCount,
		})
	}
}
