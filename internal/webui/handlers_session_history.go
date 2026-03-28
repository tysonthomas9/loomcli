package webui

import (
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
)

// handleListSessionHistory returns session history records for an issue.
// GET /api/workspaces/{ws}/issues/{issueId}/sessions
func handleListSessionHistory(store *sessionhistory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"success": false,
				"error":   "session history not available (no Redis)",
			})
			return
		}

		wsID := WorkspaceFromContext(r.Context())
		issueID := r.PathValue("issueId")
		if err := sessionhistory.ValidateIssueID(issueID); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		records, err := store.List(r.Context(), wsID, issueID)
		if err != nil {
			log.Printf("Failed to list session history for %s: %v", issueID, err)
			respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false,
				"error":   "failed to list session history",
			})
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"data":    records,
		})
	}
}

// readScrollbackFile reads and validates a scrollback file, returning its content.
// Returns (content, lineCount, httpStatus, errorMessage).
func readScrollbackFile(path string) (string, int, int, string) {
	homeDir, _ := os.UserHomeDir()
	expectedPrefix := filepath.Clean(homeDir+"/.loom/session-scrollback") + string(os.PathSeparator)
	cleanPath := filepath.Clean(path)
	if !strings.HasPrefix(cleanPath+string(os.PathSeparator), expectedPrefix) {
		return "", 0, http.StatusBadRequest, "invalid scrollback path"
	}

	f, err := os.Open(cleanPath) //nolint:gosec // path cleaned and prefix-validated above
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, http.StatusNotFound, "scrollback file not found"
		}
		log.Printf("Failed to open scrollback file %s: %v", path, err)
		return "", 0, http.StatusInternalServerError, "failed to read scrollback"
	}
	defer f.Close()

	content, err := io.ReadAll(f)
	if err != nil {
		log.Printf("Failed to read scrollback file %s: %v", path, err)
		return "", 0, http.StatusInternalServerError, "failed to read scrollback"
	}

	text := string(content)
	lines := 0
	if text != "" {
		lines = strings.Count(text, "\n") + 1
	}
	return text, lines, http.StatusOK, ""
}

// handleGetSessionScrollback returns the scrollback content for a completed session.
// GET /api/workspaces/{ws}/issues/{issueId}/sessions/{recordId}/scrollback
func handleGetSessionScrollback(store *sessionhistory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"success": false,
				"error":   "session history not available (no Redis)",
			})
			return
		}

		wsID := WorkspaceFromContext(r.Context())
		issueID := r.PathValue("issueId")
		if err := sessionhistory.ValidateIssueID(issueID); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		recordID := r.PathValue("recordId")
		if recordID == "" {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "record ID is required",
			})
			return
		}

		records, err := store.List(r.Context(), wsID, issueID)
		if err != nil {
			log.Printf("Failed to get session history for scrollback %s: %v", issueID, err)
			respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false,
				"error":   "failed to get session history",
			})
			return
		}

		var found *sessionhistory.SessionRecord
		for i := range records {
			if records[i].ID == recordID {
				found = &records[i]
				break
			}
		}

		if found == nil {
			respondJSON(w, http.StatusNotFound, map[string]interface{}{
				"success": false,
				"error":   "session record not found",
			})
			return
		}

		if found.ScrollbackPath == "" {
			respondJSON(w, http.StatusNotFound, map[string]interface{}{
				"success": false,
				"error":   "no scrollback available for this session",
			})
			return
		}

		text, lines, status, errMsg := readScrollbackFile(found.ScrollbackPath)
		if errMsg != "" {
			respondJSON(w, status, map[string]interface{}{
				"success": false,
				"error":   errMsg,
			})
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"content": text,
				"lines":   lines,
			},
		})
	}
}
